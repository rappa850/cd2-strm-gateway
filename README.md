# CD2 STRM Gateway

[中文](README.md) | [English](README.en.md)

一个基于 CloudDrive2 官方 gRPC API 的轻量级自托管 STRM 生成与直链重定向服务。

## 项目目标

通过 CloudDrive2 官方 gRPC API：

1. 浏览并扫描指定 CloudDrive2 目录中的媒体文件。
2. 保持原始目录结构生成本地 `.strm` 文件。
3. 播放时通过 `GetDownloadUrlPath(get_direct_url=true)` 动态获取云盘直链。
4. 使用 HTTP 302 将播放器重定向到真实云盘/CDN 地址。
5. 媒体数据不经过本服务中转。

本项目**不代理媒体流量、不转码、不刮削元数据、不调用 115 私有 API，也不需要 115 Cookie**。

## 架构

```text
CloudDrive2
    │
    │ gRPC API + API Token
    ▼
CD2 STRM Gateway
    ├── Web UI
    ├── STRM 任务管理
    ├── SQLite 映射
    ├── 扫描 / 监听
    └── GET /r/{mapping_id}
              │
              │ GetDownloadUrlPath(get_direct_url=true)
              ▼
         HTTP 302 Redirect
              │
              ▼
          云盘 / CDN
```

## 技术栈

### 后端

- Go 1.24+
- CloudDrive2 gRPC API
- SQLite
- 标准 `net/http` 或轻量 HTTP 框架
- 服务端 Session 认证
- AES-GCM 加密敏感本地配置

### 前端

- Vue 3
- Vite
- TypeScript
- UnoCSS
- Lucide Vue
- 自建轻量基础组件，不引入完整 UI 组件库

### 部署

- Docker
- Docker Compose
- 单容器运行
- 前端编译为静态资源，由 Go 后端统一托管
- 持久化数据统一存放于 `/data`

## 登录与认证

Web 管理页面使用本服务自有的 **Admin Token**，与 CloudDrive2 API Token 完全分离。

首次启动时，服务会生成一个高强度 Admin Token，并仅在应用日志中输出一次。浏览器使用该 Token 登录成功后，换取 HttpOnly Session Cookie。

CloudDrive2 地址与 API Token 仅作为登录后的系统配置，不作为 Web 登录凭证。

## 核心能力

- Admin Token 与 Session 登录
- CloudDrive2 连接和 API Token 校验
- CloudDrive2 目录浏览
- 多个 STRM 生成任务
- 全量扫描与增量同步
- 稳定的内部 Mapping ID
- STRM 生命周期管理
- Direct URL 动态解析
- HTTP 302 播放重定向
- 基于 `expiresIn` 的直链缓存
- Web Dashboard 与运行日志

## 不做什么

本项目不会实现：

- 115 Cookie 认证
- 115 私有 API
- 媒体代理
- 媒体转码
- NFO 刮削
- TMDB 集成
- 海报 / 图片管理
- Emby / Jellyfin / Plex 插件
- 媒体重命名或整理
- 引入 Element Plus 等完整 UI 组件库

## STRM 示例

CloudDrive2 源文件：

```text
/115/Media/Movies/Dune Part Two (2024)/Dune Part Two (2024).mkv
```

生成：

```text
/strm/Movies/Dune Part Two (2024)/Dune Part Two (2024).strm
```

STRM 内容：

```text
https://strm.example.com/r/01KABCDEFG123456789
```

播放流程：

```text
播放器
  -> GET /r/{mapping_id}
  -> Gateway 查询 CloudDrive2 源路径
  -> CloudDrive2 GetDownloadUrlPath(get_direct_url=true)
  -> Gateway 返回 HTTP 302
  -> 播放器直接连接云盘 / CDN
```

## CloudDrive2 API

官方文档：

https://www.clouddrive2.com/api/CloudDrive2_gRPC_API_Guide.html

实现必须以 CloudDrive2 官方 API 文档为准，不猜测未公开行为。

## 当前状态

项目架构、技术栈与实现约束已确定，核心功能正在开发中。

## License

MIT
