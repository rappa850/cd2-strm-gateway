package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	pb "github.com/rappa850/cd2-strm-gateway/internal/cloudrive"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

type cloudClient struct {
	conn  *grpc.ClientConn
	api   pb.CloudDriveFileSrvClient
	token string
}

func dialCloud(address, token string) (*cloudClient, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("CloudDrive2 address must be an http(s) origin")
	}
	var opts grpc.DialOption
	if u.Scheme == "https" {
		opts = grpc.WithTransportCredentials(credentials.NewTLS(nil))
	} else {
		opts = grpc.WithTransportCredentials(insecure.NewCredentials())
	}
	conn, err := grpc.NewClient(u.Host, opts)
	if err != nil {
		return nil, err
	}
	return &cloudClient{conn: conn, api: pb.NewCloudDriveFileSrvClient(conn), token: token}, nil
}
func (c *cloudClient) close() { _ = c.conn.Close() }
func (c *cloudClient) auth(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token)
}
func (c *cloudClient) validate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := c.api.GetSystemInfo(ctx, &emptypb.Empty{}); err != nil {
		if strings.Contains(err.Error(), "unexpected HTTP status code") || strings.Contains(err.Error(), "error reading server preface") {
			return errors.New("此地址未提供原生 gRPC（HTTP/2）服务；请填写 CloudDrive2 主机的直连地址，例如 http://主机IP:19798，或为反向代理配置原生 gRPC 转发")
		}
		return fmt.Errorf("gRPC connectivity: %w", err)
	}
	info, err := c.api.GetApiTokenInfo(ctx, &pb.StringValue{Value: c.token})
	if err != nil {
		return fmt.Errorf("API token validation: %w", err)
	}
	if info.GetToken() != c.token {
		return errors.New("API token validation returned a different token")
	}
	p := info.GetPermissions()
	if p == nil || !p.GetAllowList() || !p.GetAllowRead() {
		return errors.New("API token requires allow_list and allow_read permissions")
	}
	root := info.GetRootDir()
	if root == "" {
		root = "/"
	}
	if !validCloudPath(root) {
		return errors.New("API token has invalid root directory")
	}
	_, err = c.list(ctx, root)
	if err != nil {
		return fmt.Errorf("authenticated directory listing: %w", err)
	}
	return nil
}
func (c *cloudClient) list(ctx context.Context, path string) ([]*pb.CloudDriveFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stream, err := c.api.GetSubFiles(c.auth(ctx), &pb.ListSubFileRequest{Path: path})
	if err != nil {
		return nil, err
	}
	files := make([]*pb.CloudDriveFile, 0)
	for {
		reply, err := stream.Recv()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		files = append(files, reply.GetSubFiles()...)
	}
}
func (c *cloudClient) directURL(ctx context.Context, path string) (string, uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info, err := c.api.GetDownloadUrlPath(c.auth(ctx), &pb.GetDownloadUrlPathRequest{Path: path, GetDirectUrl: true})
	if err != nil {
		return "", 0, err
	}
	direct := info.GetDirectUrl()
	u, err := url.Parse(direct)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return "", 0, errors.New("CloudDrive2 did not return a usable direct URL")
	}
	if info.GetUserAgent() != "" || len(info.GetAdditionalHeaders()) > 0 {
		return "", 0, errors.New("direct URL requires request headers unsupported by plain HTTP redirect")
	}
	return strings.TrimSpace(direct), info.GetExpiresIn(), nil
}
func (a *App) configuredCloud() (*cloudClient, error) {
	address, err := a.setting("cloud_address")
	if err != nil {
		return nil, err
	}
	encrypted, err := a.setting("cloud_token")
	if err != nil {
		return nil, err
	}
	if address == "" || encrypted == "" {
		return nil, errors.New("CloudDrive2 is not configured")
	}
	token, err := a.decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	return dialCloud(address, token)
}
