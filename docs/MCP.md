# MCP Tooling

OpsDrop's server exposes a [Model Context Protocol](https://modelcontextprotocol.io)
endpoint so AI agents can create drops directly. It is served by the **same
server binary** as the REST API — there is no separate process or deploy
artifact.

## Overview

- **Endpoint:** `POST/GET /mcp` on the OpsDrop server.
- **Transport:** [Streamable HTTP](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)
  (the successor to the deprecated HTTP+SSE transport).
- **Auth:** optional. A request carrying a valid `Authorization: Bearer <jwt>`
  acts as that user; without one, it operates anonymously.
- **Discovery:** the capabilities endpoint advertises `"mcp": true` and
  `"mcp_endpoint": "/mcp"` (see [Server Capabilities](../README.md#server-capabilities)).

Because the endpoint is remote, tools take their data **inline** — the server
cannot read a file path on the agent's machine.

## Tools

### `push`

Upload inline content as a drop and return a shareable URL and token.

**When authenticated** (valid bearer token), the drop is **private** to that
user — it uses the private TTL, is listed by `opsdrop list`, and is retrievable
only with the user's credentials at `/api/v1/files/{id}`.

**When anonymous** (no token), the drop is **public** — it uses the public TTL
and is retrievable by anyone with the returned token at `/public/{token}`.

#### Input

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `content` | string | one of | UTF-8 text to upload. |
| `content_base64` | string | one of | Base64-encoded bytes; use for binary content. |
| `filename` | string | yes | Name for the drop, e.g. `note.txt` or `report.pdf`. |
| `retention_days` | integer | no | Retention in days; clamped to the server maximum for the visibility used. |

Provide **exactly one** of `content` or `content_base64`. Supplying both, or
neither, returns a tool error.

#### Output (`structuredContent`)

| Field | Description |
|-------|-------------|
| `id` | Server-assigned drop id. |
| `filename` | Stored filename. |
| `size` | Size in bytes. |
| `public` | Whether the drop is publicly accessible. |
| `url` | Absolute URL to retrieve the drop. |
| `token` | Public token for anonymous retrieval (present only for public drops). |
| `expires_at` | RFC3339 expiry timestamp. |
| `checksum` | SHA-256 checksum of the content. |

## Connecting an agent

The endpoint speaks standard MCP, so any Streamable-HTTP-capable client works.

### Claude Code

```sh
# anonymous (public drops)
claude mcp add --transport http opsdrop https://opsdrop.hemp0r.dev/mcp

# authenticated (private drops) — pass a bearer token from `opsdrop auth login`
claude mcp add --transport http opsdrop https://opsdrop.hemp0r.dev/mcp \
  --header "Authorization: Bearer $OPSDROP_TOKEN"
```

### Generic client config

```json
{
  "mcpServers": {
    "opsdrop": {
      "type": "http",
      "url": "https://opsdrop.hemp0r.dev/mcp",
      "headers": { "Authorization": "Bearer <jwt>" }
    }
  }
}
```

The JWT is the same token `opsdrop auth login` stores in
`~/.opsdrop/config.json`. Omit the `Authorization` header to push anonymously.

## Raw protocol walkthrough

Useful for debugging. Streamable HTTP is JSON-RPC; responses come back as
Server-Sent Events (`data:` lines). Send `Accept: application/json,
text/event-stream` on every request.

```sh
BASE=http://localhost:8080

# 1. initialize — capture the session id from the response headers
curl -s -D - -o /dev/null \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1.0"}}}' \
  $BASE/mcp | grep -i mcp-session-id

SID=<value from above>

# 2. complete the handshake
curl -s -o /dev/null \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -H "Mcp-Session-Id: $SID" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' $BASE/mcp

# 3. list tools
curl -s -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -H "Mcp-Session-Id: $SID" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' $BASE/mcp

# 4. push a drop (anonymous → public)
curl -s -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -H "Mcp-Session-Id: $SID" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"push","arguments":{"content":"hello from mcp","filename":"note.txt"}}}' \
  $BASE/mcp
```

To push as an authenticated user, add `-H "Authorization: Bearer $JWT"` to the
`initialize` request (and subsequent calls in the session).

## Notes & limitations

- Public uploads are never encrypted (matching the REST API). Client-side
  encryption is not available over MCP today.
- Private drops returned by `push` are only retrievable with the user's bearer
  token; they are not anonymously downloadable.
- The `push` tool is the first of a planned set; `pull`/`list` tooling may
  follow. `pull` currently would directly support code injection and other nasty stuff, which opsdrop currently not scan or handle. For awareness / security reasons its not exposed via `mcp` currently. 
