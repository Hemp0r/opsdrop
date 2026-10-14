# 0002. Request-scoped MCP authentication

- **Status:** Accepted
- **Refines:** [0001](0001-mcp-transport-and-placement.md)

## Context

The SDK's stateful Streamable HTTP handler reuses the MCP server created during
initialization. Capturing a user in that server's tool closure therefore retains
an identity across requests, without checking later logout, expiry or credentials.
OpsDrop's push-only MCP API does not require persistent server-side sessions or
server-initiated messages.

## Decision

- Use stateless Streamable HTTP and disable generated session IDs explicitly.
- Resolve optional authentication before every MCP request. Missing credentials
  intentionally mean an anonymous public upload; supplied invalid, expired,
  malformed or revoked credentials fail closed with HTTP 401. Authentication
  backend failure produces HTTP 500.
- Pass the resolved identity through request context to the request-local tool.
- Reject all requests carrying a legacy `Mcp-Session-Id` with HTTP 400. Do not
  silently reinterpret an old private session as an anonymous public request.
- Keep POST response streaming. Standalone GET/SSE and DELETE session management
  are not supported; GET returns HTTP 405.

## Consequences

Clients must reconnect without legacy session IDs and supply the bearer token on
all requests that should act privately. Clients cannot use a session ID as an
alternative credential. A standard SDK client can still initialize, discover and
call tools using Streamable HTTP without a persistent session. Anonymous public
uploads remain supported. Future server-initiated features would require a new,
explicitly authenticated session design.

This decision does not solve the separately tracked MCP request-body resource
limit, proxy trust or upload storage issues.
