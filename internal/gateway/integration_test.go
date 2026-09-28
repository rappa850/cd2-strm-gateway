package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/rappa850/cd2-strm-gateway/internal/cloudrive"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeCloud struct {
	pb.UnimplementedCloudDriveFileSrvServer
	direct     bool
	authorized bool
}

func (f *fakeCloud) GetSystemInfo(context.Context, *emptypb.Empty) (*pb.CloudDriveSystemInfo, error) {
	return &pb.CloudDriveSystemInfo{}, nil
}
func (f *fakeCloud) GetApiTokenInfo(_ context.Context, request *pb.StringValue) (*pb.TokenInfo, error) {
	return &pb.TokenInfo{Token: request.GetValue(), RootDir: "/Media", Permissions: &pb.TokenPermissions{AllowList: true, AllowRead: true}}, nil
}
func (f *fakeCloud) GetSubFiles(request *pb.ListSubFileRequest, stream grpc.ServerStreamingServer[pb.SubFilesReply]) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	f.authorized = len(md.Get("authorization")) > 0 && md.Get("authorization")[0] == "Bearer test-token"
	if request.GetPath() == "/Media" {
		return stream.Send(&pb.SubFilesReply{SubFiles: []*pb.CloudDriveFile{{Name: "film.mkv", FullPathName: "/Media/film.mkv", Size: 123}}})
	}
	return nil
}
func (f *fakeCloud) GetDownloadUrlPath(_ context.Context, request *pb.GetDownloadUrlPathRequest) (*pb.DownloadUrlPathInfo, error) {
	f.direct = request.GetGetDirectUrl()
	url := "https://cdn.example.test/video?secret=short-lived"
	expires := uint64(90)
	return &pb.DownloadUrlPathInfo{DirectUrl: &url, ExpiresIn: &expires}, nil
}

func TestCloudScanAndRedirect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	fake := &fakeCloud{}
	pb.RegisterCloudDriveFileSrvServer(server, fake)
	go server.Serve(listener)
	defer server.Stop()
	app, _, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	address := "http://" + listener.Addr().String()
	client, err := dialCloud(address, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = client.validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.close()
	if !fake.authorized {
		t.Fatal("listing did not include Bearer token")
	}
	encrypted, err := app.encrypt("test-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = app.setSetting("cloud_address", address); err != nil {
		t.Fatal(err)
	}
	if err = app.setSetting("cloud_token", encrypted); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "strm")
	job := Job{ID: newID(), Name: "Media", Enabled: true, SourceDir: "/Media", OutputDir: root, Extensions: []string{".mkv"}, ScanMode: "manual", BaseURL: "https://gateway.example.test"}
	ext := "[\".mkv\"]"
	_, err = app.db.Exec(`INSERT INTO strm_jobs(id,name,enabled,source_dir,output_dir,extensions,delete_missing,scan_mode,schedule,base_url,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, job.ID, job.Name, 1, job.SourceDir, root, ext, 0, "manual", "", job.BaseURL, now(), now())
	if err != nil {
		t.Fatal(err)
	}
	count, written, err := app.scan(context.Background(), job)
	if err != nil || count != 1 || written != 1 {
		t.Fatalf("scan: %d %d %v", count, written, err)
	}
	content, err := os.ReadFile(filepath.Join(root, "film.strm"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(content), "https://gateway.example.test/r/") || strings.Contains(string(content), "/Media/") {
		t.Fatalf("invalid STRM content: %s", content)
	}
	id := strings.TrimSpace(strings.TrimPrefix(string(content), "https://gateway.example.test/r/"))
	r := httptest.NewRequest(http.MethodGet, "/r/"+id, nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, r)
	if w.Code != 302 || w.Header().Get("Location") != "https://cdn.example.test/video?secret=short-lived" || !fake.direct {
		t.Fatalf("redirect: status=%d location=%s direct=%v", w.Code, w.Header().Get("Location"), fake.direct)
	}
}
