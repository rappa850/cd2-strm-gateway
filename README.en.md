# CD2 STRM Gateway

[中文](README.md) | [English](README.en.md)

A lightweight self-hosted STRM generator and direct-link redirect gateway built on the official CloudDrive2 gRPC API.

## Goal

Use the official CloudDrive2 gRPC API to:

1. Browse and scan media files from configured CloudDrive2 directories.
2. Generate local `.strm` files while preserving the original directory structure.
3. Resolve playback requests through `GetDownloadUrlPath(get_direct_url=true)`.
4. Return an HTTP 302 redirect to the cloud provider's direct URL.
5. Keep media traffic off the gateway itself.

The service does **not** proxy or transcode media, scrape metadata, call 115 private APIs, or require 115 cookies.

## Architecture

```text
CloudDrive2
    │
    │ gRPC API + API Token
    ▼
CD2 STRM Gateway
    ├── Web UI
    ├── STRM task manager
    ├── SQLite mappings
    ├── Scanner / watcher
    └── GET /r/{mapping_id}
              │
              │ GetDownloadUrlPath(get_direct_url=true)
              ▼
         HTTP 302 Redirect
              │
              ▼
       Cloud provider / CDN
```

## Technology Stack

### Backend

- Go 1.24+
- CloudDrive2 gRPC API
- SQLite
- Standard `net/http` or a lightweight HTTP framework
- Server-side session authentication
- AES-GCM for sensitive local configuration

### Frontend

- Vue 3
- Vite
- TypeScript
- UnoCSS
- Lucide Vue
- Lightweight in-house base components; no full UI component library

### Deployment

- Docker
- Docker Compose
- Single-container runtime
- Frontend compiled to static assets and served by the Go backend
- Persistent data under `/data`

## Authentication Model

The Web UI uses an application-owned **Admin Token**, completely separate from the CloudDrive2 API Token.

On first startup the service generates a cryptographically secure Admin Token and prints it once to the application log. The browser exchanges this token for an HttpOnly session cookie.

The CloudDrive2 address and API Token are system settings available only after login and are never used as Web login credentials.

## Core Responsibilities

- Admin Token and session login
- CloudDrive2 connection and API Token validation
- CloudDrive2 directory browser
- Multiple STRM generation jobs
- Full scan and incremental synchronization
- Stable internal mapping IDs
- STRM lifecycle management
- Direct URL resolution
- HTTP 302 playback redirect
- Direct URL caching based on `expiresIn`
- Web dashboard and operational logs

## Non-Goals

This project will not implement:

- 115 Cookie authentication
- 115 private APIs
- Media proxying
- Media transcoding
- NFO scraping
- TMDB integration
- Poster/artwork management
- Emby/Jellyfin/Plex plugins
- Media renaming or organization
- Full UI component libraries such as Element Plus

## STRM Example

CloudDrive2 source:

```text
/115/Media/Movies/Dune Part Two (2024)/Dune Part Two (2024).mkv
```

Generated file:

```text
/strm/Movies/Dune Part Two (2024)/Dune Part Two (2024).strm
```

STRM content:

```text
https://strm.example.com/r/01KABCDEFG123456789
```

Playback flow:

```text
Player
  -> GET /r/{mapping_id}
  -> Gateway resolves the CloudDrive2 source path
  -> CloudDrive2 GetDownloadUrlPath(get_direct_url=true)
  -> Gateway returns HTTP 302
  -> Player connects directly to the cloud/CDN URL
```

## CloudDrive2 API

Official documentation:

https://www.clouddrive2.com/api/CloudDrive2_gRPC_API_Guide.html

Implementation must follow the official CloudDrive2 API documentation rather than guessing undocumented behavior.

## Status

Project architecture, technology stack, and implementation constraints are defined. Core implementation is in progress.

## License

MIT
