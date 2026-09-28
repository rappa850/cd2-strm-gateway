# AGENTS.md

## Project

CD2 STRM Gateway is a small self-hosted service that generates STRM files from CloudDrive2 and redirects playback requests to CloudDrive2 direct URLs.

## Required Stack

### Backend
- Go 1.24+
- CloudDrive2 official gRPC API
- SQLite
- Server-side sessions
- AES-GCM for sensitive local configuration
- Prefer standard library or lightweight dependencies

### Frontend
- Vue 3
- Vite
- TypeScript
- UnoCSS
- Lucide Vue
- Build lightweight project-local base components
- Do not add Element Plus or any other full UI component library

The frontend is intentionally small. Prefer simple local components for buttons, inputs, selects, switches, modals, cards, tables, badges, alerts, form fields, tabs, and the directory picker. Avoid heavy UI frameworks and unnecessary client-side dependencies.

### Deployment
- Docker
- Docker Compose
- Single runtime container
- Frontend built to static assets and served by Go
- Persistent application data under /data

## Authentication

Do not use the CloudDrive2 API Token for Web login.

The application owns its own Admin Token:
- Generate a cryptographically secure Admin Token on first startup.
- Print the original token once to logs.
- Store only a secure verifier/hash.
- Exchange a valid Admin Token for an HttpOnly session cookie.
- Do not store Admin Token in localStorage.
- Do not use JWT for sensitive session state.
- Support logout and Admin Token rotation.

CloudDrive2 address and API Token are system settings available only after application login.

## CloudDrive2 Rules

Read and follow the official documentation before implementation:

https://www.clouddrive2.com/api/CloudDrive2_gRPC_API_Guide.html

Do not guess RPC names, fields, permissions, metadata requirements, or protobuf behavior.

The production implementation must:
- Validate gRPC connectivity.
- Validate the configured API Token using real RPC calls.
- Expose only masked token values to the Web UI.
- Never log the complete CloudDrive2 API Token.
- Use the minimum permissions required by the actual RPC calls.
- Browse directories through CloudDrive2 API instead of relying on local FUSE mounts.

## STRM Model

Each configured job has:
- name
- enabled flag
- CloudDrive2 source directory
- local STRM output directory
- media extension filters
- source-deletion behavior
- scan mode
- optional schedule

Preserve source-relative directory structure.

Do not expose source paths in generated STRM URLs.

Maintain a persistent mapping table:
- mapping id (prefer ULID)
- job id
- CloudDrive2 source path
- relative path
- output path
- source size
- source modified time
- timestamps

STRM content must use a stable gateway URL:

https://host/r/{mapping_id}

Do not write temporary CloudDrive2 direct URLs into STRM files.

## Playback Redirect

GET /r/{mapping_id} must:
1. Resolve the mapping in SQLite.
2. Obtain the source path.
3. Call CloudDrive2 GetDownloadUrlPath with direct URL mode enabled according to the official API.
4. Use the returned direct URL for an HTTP redirect.
5. Never download and re-stream media through this service.

If CloudDrive2 returns userAgent or additionalHeaders requirements, verify actual client compatibility. Do not silently fall back to proxying media.

Direct URL cache may use expiresIn with a safety margin.

## Scope Boundaries

Do not implement:
- 115 Cookie authentication
- 115 private APIs
- 115 reverse engineering
- media proxying
- media transcoding
- NFO scraping
- TMDB integration
- poster/artwork management
- Emby/Jellyfin/Plex plugins
- media renaming/organization
- Element Plus or another full UI component library

## Storage

Use SQLite with:
- WAL
- foreign_keys
- busy_timeout
- migrations

Expected logical tables:
- settings
- sessions
- strm_jobs
- strm_mapping
- scan_runs
- event_logs

Sensitive settings such as the CloudDrive2 API Token must not be stored as plaintext. Use a standard authenticated encryption scheme such as AES-GCM with a locally persisted application secret.

## File Safety

All generated/deleted STRM paths must be validated as descendants of the configured output directory.

Never:
- recursively delete an output root because of a malformed path
- allow ../ traversal
- build shell commands from file paths
- expose arbitrary file read/write endpoints

Use standard path APIs.

## Development Priority

### Phase 1
- Go server
- SQLite and migrations
- Vue application
- Docker
- Admin Token
- session login

### Phase 2
- CloudDrive2 gRPC client
- token validation
- directory browser
- direct URL proof of concept

### Phase 3
- STRM job CRUD
- scanner
- mappings
- STRM generation/deletion

### Phase 4
- /r/{id}
- direct URL cache
- HTTP redirect

### Phase 5
- scheduled scans
- CloudDrive2 push/incremental synchronization

Do not let Phase 5 block a usable MVP.

## Verification

Before considering work complete:
- go test ./...
- go vet ./...
- frontend TypeScript check passes
- frontend production build passes
- Docker image builds
- Docker container boots with persistent /data
- credentials are not printed in normal logs
- media traffic is not proxied by the gateway
