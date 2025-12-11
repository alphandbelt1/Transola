# Transola Design

## Goals
- Single central server (no P2P) that accepts uploads/downloads from a CLI agent and a web UI.
- Store files on local disk; optionally mount/redirect to an S3-compatible backend. No hard size limit.
- Integrity: enforce checksums (SHA-256) end-to-end; resumable uploads for large files.
- Access control: uploads can target any user namespace; downloads require auth/ACL; time-limited links.
- UI: browse/filter by time, type, owner; upload widget; copyable download links; audit trail.

## Components
- **Server (Go)**: One binary with HTTP API + static assets; embeds UI build. Handles auth, metadata, storage, and signed URLs.
- **Agent CLI (Go)**: Authenticates via token; commands `login`, `upload`, `download`, `list`. Uses streaming/resumable protocol.
- **Web UI (SPA)**: Built with Vite/React; consumed from the same server origin for simplicity.
- **Storage**: Pluggable store interface with LocalFS first; optional S3-compatible via env toggle.
- **DB**: SQLite for metadata (users, files, tokens, audit). Can swap to Postgres later via driver flag.

## Tech Choices
- Language: Go (static binary, good HTTP performance, easy embedding of UI).
- HTTP API with JSON; streaming uploads via tus or S3 multipart compatible endpoints. Recommend `tus` for resumable semantics and low friction; can also expose `PUT /objects/:id` with chunked transfer for smaller files.
- Auth: JWT bearer tokens for CLI/UI; refresh tokens stored server-side; per-user API tokens for headless agents.
- Crypto/Integrity: SHA-256 checksums required on upload; server validates on finalize; optional size and content-type assertions.
- MIME detection: use `net/http.DetectContentType` + libmagic fallback if available.

## Data Model (DB)
- `users(id, email, display_name, role, password_hash, created_at, updated_at)`
- `api_tokens(id, user_id, name, hashed_secret, expires_at, created_at)`
- `files(id, owner_id, uploader_id, namespace, name, mime, size, sha256, storage_key, created_at, finalized_at)`
- `downloads(id, file_id, requester_id, expires_at, created_at, last_accessed_at)`
- `audits(id, actor_id, action, target_id, metadata_json, created_at)`
Notes: `namespace` lets an uploader place a file under another user logically. `storage_key` maps to physical path or S3 key.

## Storage Layout (LocalFS)
- Root configurable (e.g., `DATA_ROOT=/var/lib/transola`).
- Path scheme: `/data/{yyyy}/{mm}/{dd}/{random-prefix}_{file-id}` to avoid hot directories.
- Temp uploads staged under `/tmp/{upload-id}` until checksum verified, then moved to final location atomically.
- For S3-compatible mode, use bucket `transola`, key mirrors above scheme.

## API Surface (sketch)
- `POST /v1/auth/login` → {access_token, refresh_token}
- `POST /v1/auth/refresh`
- `POST /v1/tokens` (create API token; admin/user)
- `POST /v1/uploads` request upload: body {target_user, filename, size, sha256, namespace?} → {upload_id, upload_url, finalize_url, expires_at}
- `PATCH /v1/uploads/:id/finalize` verify checksum, commit, write metadata → {file_id}
- `GET /v1/files` list/filter by owner, type, date; supports pagination.
- `GET /v1/files/:id` metadata
- `POST /v1/files/:id/download-link` issue signed URL (short-lived) → {url, expires_at}
- `GET /v1/files/:id/content` gated direct download (for browsers)
- `DELETE /v1/files/:id` (role-based)
- `GET /v1/audit` (admin)

## Upload Flow (Agent/UI)
1) Client authenticates (JWT or API token).
2) Request upload: server reserves record, returns `upload_url` (tus or multipart) and `finalize_url`.
3) Client streams file; can resume if interrupted.
4) Client calls finalize with declared checksum; server validates stored SHA-256 vs provided and size; move from temp → final; emit audit event.
5) Server returns file metadata and `file_id`.

## Download Flow
- Client/UI requests download link for `file_id`; server enforces ACL (owner, admin, or delegated permission).
- Server returns time-limited signed URL (HMAC, embedded expiry + file_id) for direct GET; UI can also download via authenticated endpoint if preferred.
- Audit log records requester and time.

## Categorization/Filtering
- By time: use `created_at` and index.
- By type: store MIME and derive coarse category (image/video/doc/archive/other).
- By user/namespace: filters on `owner_id`, `namespace`.

## CLI Outline
- `transola login --url https://host --email ...` (stores token in OS keyring or config file).
- `transola upload <path> --as user@example.com --namespace team/x --note "desc"` → prints progress, file_id, download link.
- `transola download <file_id> -o ./dest` (accepts signed URL or id).
- `transola list --owner me --type image --since 7d`.

## Web UI Outline
- Pages: Login; Upload (drag/drop, resumable); File list with filters (date range, type, owner); File detail (metadata, checksum, download link copy); Audit view (admin).
- Use a design system (e.g., Radix + custom theming) and talk to the same API origin.

## Directory Layout (proposed)
- `cmd/server/main.go` (HTTP API, static UI embedding)
- `cmd/cli/main.go` (agent CLI)
- `internal/auth` (JWT, tokens)
- `internal/storage` (local, s3 compatible)
- `internal/db` (migrations, models)
- `internal/transfer` (upload session, checksum)
- `web/` (UI source; built into `web/dist` and embedded)
- `docs/` (design, API)

## Next Steps
- Initialize Go module, set up basic HTTP server + health endpoint.
- Define storage interface and LocalFS implementation with checksum-verified finalize.
- Pick upload protocol (tus recommended) and wire minimal handler.
- Implement auth (JWT) + token issuance; stub user store (seed admin).
- Build minimal UI shell (login, upload, list) and embed assets.
- Add integration tests for upload/finalize/download with checksum validation.
