# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```sh
# Build (CGO is never needed — modernc.org/sqlite is pure Go)
make build-cli                 # builds ./cmd/opsdrop
make build-all                 # cross-compiles CLI into dist/
go build ./cmd/server          # builds the server binary

# Local dev server (TLS off, behind reverse proxy or for testing)
SERVER_JWT_SECRET=$(openssl rand -hex 32) \
SERVER_TLS_ENABLED=false \
go run ./cmd/server

# Containerised server (the dev path used most often)
SERVER_JWT_SECRET=$(openssl rand -hex 32) docker compose up --build

# Release plumbing
make container-release         # docker build + push hemp0r/opsdrop:latest
make helm-release              # package + push chart/opsdrop to GHCR OCI
```

There is no test suite — `go test ./...` returns no packages. CI is release-only
(`.github/workflows/release-*.yml`); there is no lint/test workflow. Build-time
version metadata is injected via `-ldflags "-X opsdrop/internal/version.{Version,Commit,Date}=..."`.

## Architecture

Two binaries share the same `internal/` packages:

- **`cmd/server`** — HTTPS backend. Bootstraps `config.Load()` → `db.New()` → `db.EnsureMigrations()` → `server.New()`, then starts an `http.Server` with 5/10 min read/write timeouts to accommodate large transfers. Graceful shutdown on SIGINT/SIGTERM.
- **`cmd/opsdrop`** — Cobra-based CLI. All HTTP interaction goes through `internal/client`, which the TUI (`internal/tui`) also consumes. `cmd/opsdrop/ui.go` is a thin entrypoint that constructs the Bubble Tea program.

### Server request flow (`internal/server/server.go`)

A single ~1000 line file wires `chi` routes onto `Server` methods. Two distinct upload paths reflect the core product model:

- `POST /api/v1/public/files` — anonymous, returns a UUID public token, uses `DefaultPublicTTL` / `MaxPublicTTL`.
- `POST /api/v1/files` — authenticated; `public=true` form field toggles whether a public token is also minted. Uses `DefaultPrivateTTL` / `MaxPrivateTTL`.
- `GET /public/{token}` — unauthenticated download by token.

Both upload handlers flow through `consumeUpload()`, which streams the multipart body to a UUID-named file under `<StorageDir>/{public,user_N}/`, computes a SHA-256 checksum during the copy, then persists metadata via `db.CreateFile`. The stored file is `os.Remove`'d on any subsequent error — the `cleanup` flag pattern prevents orphans.

Auth middleware checks the `revoked_tokens` table *before* verifying the JWT signature (logout writes a row that expires when the token would have expired anyway). `X-Client-Machine` is sanitized to alphanumerics + `-_.` and recorded on every audited action via `appendAudit`. `auth.HashPassword` SHA-256 pre-hashes before bcrypt to dodge bcrypt's 72-byte truncation; `CheckPassword` falls back to legacy direct bcrypt for old hashes — preserve that fallback when touching password code.

A goroutine started in `Server.New` runs `cleanupExpiredFiles` + `cleanupExpiredRevocations` hourly. Two `rateLimiter`s (10/min for auth, 5/min for uploads) live in `internal/server/ratelimit.go`. Long file transfers use a custom `transferTimeout` (10 min) that only sets a context deadline — never `middleware.Timeout`, which buffers the response body.

### Capabilities endpoint as protocol handshake

`GET /.well-known/opsdrop-capabilities` is unauthenticated and is *the* identifier of "this is an OpsDrop server". `opsdrop remote set` refuses to persist a URL whose capabilities probe fails — this is intentional (see commit `1e9097c`). Don't bypass this check; it is the only way the CLI distinguishes a real server from a redirect or unrelated HTTPS endpoint. The CLI caches the response in `~/.opsdrop/config.json` to drive help hints (e.g. registration-disabled warnings).

### MCP endpoint (`internal/server/mcp.go`)

`GET/POST /mcp` mounts the official `github.com/modelcontextprotocol/go-sdk`
Streamable HTTP handler onto the same chi router — single binary, no second
process. It's wired with `xferTimeout` (context-deadline only), **never**
`middleware.Timeout`, which would buffer and break the streaming transport. A
fresh `mcp.Server` is built **per request** in the `getServer` closure so the
`push` tool can bind to the caller's identity: `resolveUser(r)` (the optional-auth
core extracted from `authMiddleware`) returns the user for a valid bearer token,
or `(nil, nil)` for anonymous. The tool takes content **inline** (`content` or
`content_base64` + `filename`) — the server is remote and can't read a client
path — and routes through `storeDrop`, the byte-based sibling of `consumeUpload`
(authenticated ⇒ private/`user_N`, anonymous ⇒ public/`public` with a token).
`handleCapabilities` advertises `mcp` + `mcp_endpoint`. See `docs/MCP.md` for
usage and `docs/adr/0001-mcp-transport-and-placement.md` for the rationale behind
these choices. Non-obvious decisions are recorded as ADRs in `docs/adr/`.

### Database (`internal/db`)

SQLite via `modernc.org/sqlite` (pure Go, no CGO). `db.New` sets `SetMaxOpenConns(1)` — SQLite is single-writer and this is what avoids the deadlocks mentioned in commit `b988df6`; don't raise it. `db.go` holds the initial `CREATE TABLE IF NOT EXISTS` statements that cover a fresh DB; `migrations.go` holds incremental upgrades for existing DBs, each one a `PRAGMA table_info`-driven idempotent check (e.g. `addChecksumColumn`, `addEncryptionColumns`, `mergeClipboardIntoFiles`). When adding a column or table, add the create statement to **both** `db.go` (so fresh installs pick it up) and `migrations.go` (so existing DBs migrate), and wire it into `EnsureMigrations`'s sequence.

Files and clipboard entries share the same `files` table — `entry_type` discriminates them (`EntryTypeFile`, `EntryTypeClipboard`). `FileRecord` uses `sql.NullString`/`sql.NullInt64` for optional columns; convert carefully when building API responses.

### Client + TUI (`internal/client`, `internal/tui`)

`internal/client/client.go` is the single API client used by both the CLI and TUI — keep new HTTP calls here, not duplicated in `cmd/opsdrop`. Config lives at `~/.opsdrop/config.json` (`0600`, atomic write via `.tmp` rename). Server URL resolution order: explicit arg → `SERVER_URL` env → configured `RemoteURL` → `DefaultRemoteURL` (`https://opsdrop.hemp0r.dev`).

Client-side encryption: scrypt KDF (N=2¹⁵, r=8, p=1) → chacha20 stream. Salt + nonce are uploaded as form fields, returned on download as `X-Opsdrop-{Salt,Nonce}` headers; the server never sees the password. Public uploads cannot be encrypted (enforced both client- and server-side). Checksum verification is on by default; `--no-checksum` disables it.

Directories are zipped client-side in `UploadDirectory` and treated as a single drop — there is no server-side directory concept.

The TUI (`internal/tui`) is Bubble Tea with three sub-models (`filesModel`, `clipboardModel`, `uploadModel`) coordinated by the root `Model` in `tui.go`. `tab` switches views; `u` opens the upload overlay (files view only); `?` toggles help. Status messages are pushed via the `statusMsg` `tea.Msg`.

### Config (`internal/config`)

Env-only, no config file. `parseDuration` extends `time.ParseDuration` with a `Nd` (days) suffix — used for `*_TTL` settings. Validates that `DEFAULT_*_TTL <= MAX_*_TTL`. `SERVER_JWT_SECRET` is required and must be ≥32 chars. Container defaults live in the `Dockerfile`'s `ENV` block.