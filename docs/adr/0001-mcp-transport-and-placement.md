# 0001. MCP tooling transport and placement

- **Status:** Accepted
- **Date:** 2026-06-22

## Context

We want to expose OpsDrop to AI agents via the Model Context Protocol (MCP),
starting with a `push` tool that creates a drop. Several choices had to be made
together because they constrain one another:

- **Where the MCP server runs** — inside an existing binary or as a new one.
- **Which transport** — MCP defines stdio and HTTP-based transports; the HTTP+SSE
  transport was deprecated in the 2025-06-18 spec in favour of Streamable HTTP.
- **Which SDK** — the official `github.com/modelcontextprotocol/go-sdk` versus
  community libraries such as `github.com/mark3labs/mcp-go`.
- **How a tool receives a file** — a local path versus inline content.
- **How auth is handled** — anonymous only, or honouring an existing login.

OpsDrop is a single server binary plus a CLI that share `internal/` packages. The
project values minimal dependencies and a single deploy artifact. The server is
remote relative to any agent calling it.

## Decision

- Mount the MCP server as a **`/mcp` route on the existing `cmd/server` chi
  router**, using the official Go SDK's `NewStreamableHTTPHandler`. No second
  process or deploy artifact.
- Use **Streamable HTTP** as the transport (not stdio, not the deprecated SSE
  transport).
- Use the **official `modelcontextprotocol/go-sdk`** (pure Go, no CGO — consistent
  with the rest of the build).
- The `push` tool takes content **inline** — `content` (text) or `content_base64`
  (bytes) plus `filename` — never a filesystem path.
- The endpoint honours an **optional bearer token**: a valid token yields a
  private drop for that user, otherwise the drop is anonymous and public. This
  reuses `resolveUser`, the optional-auth core extracted from `authMiddleware`.
- Mount the route with `xferTimeout` (a context deadline only), **never**
  `middleware.Timeout`.
- Do **not** apply the strict `/api/v1/public/files` upload limiter (5/min) to
  `/mcp` — a single session needs several requests, so it would break the
  handshake. Instead apply a **dedicated moderate per-IP limiter** (`mcpLimiter`,
  60/min) that still caps anonymous-upload abuse with headroom to spare.

## Consequences

- Single binary and single deploy story are preserved; the MCP handler is just
  another `http.Handler` on the existing mux. The persistence path reuses
  `storeDrop`, the byte-based sibling of `consumeUpload`.
- A remote endpoint cannot read the caller's filesystem, so inline content is the
  only coherent input. Agents pass text or base64 bytes; large local files are
  not a first-class path over MCP today.
- `middleware.Timeout` buffers the response body, which would break a streaming
  transport — hence `xferTimeout`. This mirrors the existing rule for large file
  transfers documented in `CLAUDE.md`.
- `/mcp` accepts anonymous uploads (a disk-exhaustion vector). It is intentionally
  **not** behind the strict 5/min upload limiter — the Streamable HTTP handshake
  issues several JSON-RPC POSTs per session, so that cap would break normal use —
  but it **is** behind a dedicated moderate limiter (`mcpLimiter`, 60/min) so
  default deployments are protected even without a proxy. `docs/HOSTING.md` still
  recommends a coarse proxy-level limit as defense-in-depth.
- Adds one direct dependency (`modelcontextprotocol/go-sdk`) and its transitive
  set (jsonschema-go, etc.).

## Alternatives considered

- **A separate stdio MCP binary in the CLI (`opsdrop mcp`).** Stdio would give a
  co-located server filesystem access (paths would work), but it is a second
  process the user must wire into their agent, and it does not match the
  single-binary deployment we already have for the REST API. Rejected.
- **`mark3labs/mcp-go` (community SDK).** Mature ergonomics, but a third-party
  dependency where an official, co-maintained SDK exists; the official SDK's
  `NewStreamableHTTPHandler` returns a standard `http.Handler` that mounts
  straight into chi. Rejected in favour of the official SDK.
- **Path-based tool input.** Only meaningful if the server is co-located with the
  agent; incoherent for a remote endpoint. Rejected in favour of inline content.
- **Anonymous-only tool.** Simpler, but discards a logged-in caller's identity and
  the ability to create private drops. Rejected in favour of optional auth.
