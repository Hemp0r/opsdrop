# OpsDrop

CLI-first ephemeral file and text sharing.

OpsDrop lets you quickly push and pull files, directories, text, and clipboard content between machines or people through a simple HTTPS backend.

### Think:

- temporary transfer, not storage
- push / pull, not manage / organize
- frictionless by default
- self-hostable when needed
- Core Principles
- Ephemeral by default
- CLI-first
- One core model: a drop
- One core workflow: push / pull
- No file management platform behavior
- No tagging, browsing, or long-term organization

### What OpsDrop Is

OpsDrop is built for fast temporary transfer of:

- files
- directories
- text
- clipboard content
- piped stdin

A directory is archived locally before upload and treated as a single drop.

### What OpsDrop Is Not

OpsDrop is intentionally not:

- a file hosting platform
- a sync tool
- a cloud drive
- a document management system
- a general IT operations companion

It solves one problem: ***fast, temporary data transfer***

## Install opsdrop

### Via script

```sh
curl -fsSL https://raw.githubusercontent.com/Hemp0r/opsdrop/refs/heads/main/install.sh | sh
```

or

```sh
wget -qO- https://raw.githubusercontent.com/Hemp0r/opsdrop/refs/heads/main/install.sh | sh
```

### Via Homebrew

```sh
brew tap hemp0r/opsdrop
brew install opsdrop
```

---

## Project Layout

```
cmd/
  server/     # HTTPS backend
  opsdrop/    # CLI client
internal/
  auth/       # JWT + password helpers
  config/     # Server configuration loader
  db/         # SQLite persistence layer
  server/     # HTTP handlers and routing
  client/     # Shared API client (CLI + TUI)
  tui/        # Interactive terminal UI
```

## Server Configuration

The server reads environment variables (shown with defaults):

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_ADDRESS` | `:8443` (`:8080` when TLS disabled) | Listen address |
| `SERVER_TLS_ENABLED` | `true` | Set to `false` to run plain HTTP (e.g. behind a reverse proxy) |
| `SERVER_TLS_CERT` | `certs/server.crt` | TLS certificate path (ignored when TLS disabled) |
| `SERVER_TLS_KEY` | `certs/server.key` | TLS private key path (ignored when TLS disabled) |
| `SERVER_DATABASE` | `data/server.db` | SQLite storage |
| `SERVER_STORAGE_DIR` | `data/storage` | Directory for uploaded files |
| `SERVER_JWT_SECRET` | _(required)_ | HMAC secret for JWT signing (min 32 chars) |
| `REGISTRATION_ENABLED` | `true` | Set `false` to disable self-service account registration |
| `MAX_UPLOAD_SIZE_BYTES` | `0` (unlimited) | Maximum upload size in bytes; `0` means no limit |
| `DEFAULT_PRIVATE_TTL` | `14d` | Default expiration time for private uploads (e.g., `7d`, `168h`) |
| `MAX_PRIVATE_TTL` | `14d` | Maximum expiration time users can set for private uploads |
| `DEFAULT_PUBLIC_TTL` | `48h` | Default expiration time for public uploads (e.g., `2d`, `48h`) |
| `MAX_PUBLIC_TTL` | `48h` | Maximum expiration time users can set for public uploads |

`SERVER_JWT_SECRET` **must** be supplied (e.g. using `openssl rand -hex 32`).

For detailed information on expiration configuration, see [docs/EXPIRATION.md](docs/EXPIRATION.md).

### Server Capabilities

The server exposes `GET /.well-known/opsdrop-capabilities` (no auth required) which returns a JSON object describing:

- `auth_enabled`, `anonymous_uploads`, `private_uploads`, `public_shares`
- `self_service_registration` (from `REGISTRATION_ENABLED`)
- `max_upload_size_bytes` (from `MAX_UPLOAD_SIZE_BYTES`)
- `default_ttl_seconds`, `max_ttl_seconds` (private upload expiration)
- `default_public_ttl_seconds`, `max_public_ttl_seconds` (public upload expiration)

The CLI fetches and caches these on `opsdrop remote set` and `opsdrop remote refresh` to improve help output and UX hints. `opsdrop remote set` requires this endpoint to respond successfully — if capabilities cannot be fetched, the new remote URL is rejected and the existing configuration is left unchanged, since a server that doesn't expose this endpoint is not a compatible OpsDrop remote.

### Self-signed certificate (development)

```
mkdir -p certs
openssl req -x509 -newkey rsa:4096 -keyout certs/server.key -out certs/server.crt \
  -sha256 -days 365 -nodes -subj "/CN=localhost"
```

### Running the server

```
SERVER_JWT_SECRET=$(openssl rand -hex 32) \
SERVER_TLS_CERT=certs/server.crt \
SERVER_TLS_KEY=certs/server.key \
go run ./cmd/server
```

## CLI Usage

The CLI stores its configuration in `~/.opsdrop/config.json` (includes random machine ID for auditing, remote URL, auth token, and cached server capabilities).

By default, `opsdrop` uses the public endpoint at `https://opsdrop.hemp0r.dev`. To use a self-hosted instance, configure a custom remote with `opsdrop remote set`.

The remote URL is resolved in this order: `SERVER_URL` environment variable → configured remote in `~/.opsdrop/config.json` → default public endpoint.

### Global flags

| Flag | Description |
|------|-------------|
| `--insecure` | Skip TLS certificate verification (useful with self-signed certs) |
| `--config <path>` | Override the config file location |
| `--no-checksum` | Skip checksum computation and verification |

### Quick start (no login required)

Push a file as a 48h public share and get a pull token:

```
opsdrop push ./report.txt
```

Pull it back on another machine using the public token:

```
opsdrop pull <token>
```

### Remote configuration

```
opsdrop remote set https://myserver:8443          # configure a custom remote
opsdrop remote set https://myserver:8443 --insecure  # persist TLS skip for self-signed certs
opsdrop remote show                                # display active remote and cached capabilities
opsdrop remote refresh                             # re-fetch server capabilities
opsdrop remote reset                               # revert to default public endpoint
```

### Authentication

```
opsdrop auth register --username ops-user          # create account (prompts for password)
opsdrop auth login --username ops-user             # authenticate (prompts for password)
opsdrop auth whoami                                # show current identity (local JWT decode)
opsdrop auth logout                                # clear stored token
```

### Push files and clipboard content

```
opsdrop push ./report.txt                          # public upload (no login)
opsdrop push ./report.txt --retention-days 7       # private upload (requires login)
opsdrop push ./report.txt --encrypt                # encrypted private upload (prompts for password)
opsdrop push ./project-dir/                        # auto-zips and uploads a directory
opsdrop push --clipboard                           # push clipboard/stdin content
printf "ssh key xyz" | opsdrop push --clipboard    # push piped content
```

### Pull files and clipboard entries

```
opsdrop pull <token>                               # pull public file by token (no login)
opsdrop pull <id>                                  # pull private file by numeric ID (requires login)
opsdrop pull <id> -o ./output.txt                  # write to file instead of stdout
opsdrop pull <id> --clipboard                      # copy clipboard entry to system clipboard
```

### Management

```
opsdrop list                                       # list your files (requires login)
opsdrop list --clipboard                           # list clipboard entries
opsdrop delete <id>                                # delete an entry by ID
```

### Interactive TUI

```
opsdrop ui                                         # requires login
```

> For development against a self-signed certificate, use `--insecure` as a global flag on any command, or persist it with `opsdrop remote set <url> --insecure`.

## Auditing

All authenticated routes record an audit entry with:

- `user_id` (nullable for unauthenticated public downloads)
- `machine_id` supplied via the CLI's `X-Client-Machine` header
- action + resource identifiers
- optional metadata payload (e.g. file size, counts)

Extend `internal/db/audit.go` and `internal/server/server.go` to integrate with SIEM tooling or ship logs elsewhere.

## Building

```
go build ./cmd/server
go build ./cmd/opsdrop
```
