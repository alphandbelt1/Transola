# Transola
Fast. Modern. Effortless Transfers.

## Design
See `docs/design.md` for the proposed architecture, API surface, and next steps.

## Quickstart (dev)
- Run server: `go run ./cmd/server` (env: `TRANSOLA_HTTP_ADDR=:8080`, `TRANSOLA_DATA_ROOT=./data`, `TRANSOLA_DB_PATH=./transola.db`, `TRANSOLA_JWT_SECRET=dev-secret-change-me`).
- Default admin (seeded on first run): `TRANSOLA_ADMIN_EMAIL=admin@example.com`, `TRANSOLA_ADMIN_PASSWORD=admin123` (change in env).
- Health check via CLI: `go run ./cmd/cli health --url http://127.0.0.1:8080`.
- Login + save token: `go run ./cmd/cli login --url http://127.0.0.1:8080 --email admin@example.com --password admin123`.
- Upload: `go run ./cmd/cli upload --file path/to/file`.
- List with filters: `go run ./cmd/cli list --type image --since 2024-01-01T00:00:00Z`.
- Download: `go run ./cmd/cli download --id <file-id> --out ./dest`.
- Issue signed link: `go run ./cmd/cli link --id <file-id>`.
- Go version: targets Go 1.15 for now; upgrade later as needed.

Notes:
- Metadata uses SQLite (cgo via `github.com/mattn/go-sqlite3`).

## Build & Run (compiled binaries)
- Build server: `go build -o bin/transola-server ./cmd/server`
- Build CLI: `go build -o bin/transola ./cmd/cli`
- Run server with env set (adjust paths/secrets as needed):
  - `TRANSOLA_HTTP_ADDR=:8080 TRANSOLA_DATA_ROOT=./data TRANSOLA_DB_PATH=./transola.db TRANSOLA_JWT_SECRET=change-me TRANSOLA_ADMIN_EMAIL=admin@example.com TRANSOLA_ADMIN_PASSWORD=admin123 ./bin/transola-server`
- CLI flow (after server is up):
  - `./bin/transola health --url http://127.0.0.1:8080`
  - `./bin/transola login --url http://127.0.0.1:8080 --email admin@example.com --password admin123`
  - `./bin/transola upload --file ./path/to/file`
  - `./bin/transola list --type image --limit 20`
  - `./bin/transola link --id <file-id>` (prints a token URL)
  - `./bin/transola download --id <file-id> --out ./dest`
