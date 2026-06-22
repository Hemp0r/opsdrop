# OpsDrop Hosting & Proxy Security Guide

This guide covers security considerations that should be handled at the
infrastructure / reverse-proxy layer when deploying OpsDrop in production.

---

## 1. Reverse Proxy as the Security Perimeter

OpsDrop should **always** sit behind a reverse proxy (nginx, Caddy, Envoy, HAProxy,
Traefik, or a cloud load balancer). The proxy is the first line of defence and
handles concerns that belong to the network edge rather than the application.

```
Client ──TLS──▶ Reverse Proxy ──HTTP──▶ OpsDrop (:8080)
```

Set `SERVER_TLS_ENABLED=false` on the OpsDrop container when TLS is terminated
at the proxy.

---

## 2. TLS Termination

| Strategy | Description | When to Use |
|---|---|---|
| **Terminate at proxy** | Proxy holds the certificate. Internal traffic is plain HTTP. | Default for most deployments. Use network policies to protect internal traffic. |
| **Passthrough** | Proxy forwards raw TLS to OpsDrop, which terminates it. | Required by compliance (PCI-DSS, HIPAA) or when the internal network is untrusted. |
| **Re-encryption / mTLS** | Proxy terminates external TLS, then opens a new mTLS connection to the backend. | Service-mesh deployments (Istio, Linkerd). |

### Recommended TLS settings (proxy-side)

- Minimum TLS 1.2, prefer TLS 1.3.
- Disable weak ciphers (RC4, 3DES, export ciphers).
- Enable OCSP stapling.
- Use certificates from a trusted CA (e.g. Let's Encrypt via cert-manager or
  certbot).

---

## 3. HSTS (HTTP Strict Transport Security)

When TLS is terminated at the proxy, configure HSTS **at the proxy**:

```
Strict-Transport-Security: max-age=63072000; includeSubDomains; preload
```

The application also sets this header when `SERVER_TLS_ENABLED=true`, but in a
proxy-terminated setup, the proxy should be the one advertising it.

---

## 4. Rate Limiting

The application enforces its own rate limits on authentication and public upload
endpoints (per remote IP). However, the proxy should apply **additional**
coarse-grained rate limits to protect against volumetric attacks before requests
reach the application:

### Recommended proxy-level limits

| Endpoint pattern | Suggested limit | Purpose |
|---|---|---|
| All endpoints | 100 req/s per IP | General abuse prevention |
| `POST /api/v1/auth/*` | 10 req/min per IP | Defence-in-depth for brute-force |
| `POST /api/v1/public/files` | 5 req/min per IP | Prevent unauthenticated disk exhaustion |
| `GET /public/*` | 30 req/min per IP | Prevent public-link abuse |
| `/mcp` | 60 req/min per IP | MCP tooling — allows anonymous uploads, but a single session needs several requests (handshake + tool calls), so keep headroom |

The `/mcp` endpoint accepts anonymous uploads like `POST /api/v1/public/files`.
It is **not** covered by the strict 5/min upload limiter — the Streamable HTTP
handshake issues several JSON-RPC POSTs per session, so a tight per-request cap
would break normal use — but the application does apply a dedicated moderate
limiter (60/min per IP) to it. The proxy-level limit above is defense-in-depth.

#### nginx example

```nginx
limit_req_zone $binary_remote_addr zone=general:10m rate=100r/s;
limit_req_zone $binary_remote_addr zone=auth:10m rate=10r/m;
limit_req_zone $binary_remote_addr zone=upload:10m rate=5r/m;

server {
    location /api/v1/auth/ {
        limit_req zone=auth burst=5 nodelay;
        proxy_pass http://opsdrop:8080;
    }
    location /api/v1/public/files {
        limit_req zone=upload burst=2 nodelay;
        client_max_body_size 1g;
        proxy_pass http://opsdrop:8080;
    }
    location / {
        limit_req zone=general burst=20 nodelay;
        proxy_pass http://opsdrop:8080;
    }
}
```

---

## 5. Trusted Proxies & X-Forwarded-For

OpsDrop uses `chi/middleware.RealIP`, which trusts `X-Forwarded-For` and
`X-Real-IP` headers. **This is only safe when the proxy strips or overwrites
these headers for external requests.**

### Proxy configuration checklist

- [ ] The proxy sets `X-Forwarded-For` to the **actual** client IP.
- [ ] The proxy strips any pre-existing `X-Forwarded-For` from external clients
      (or appends to it after validating trusted hops).
- [ ] OpsDrop is **not** directly reachable from the internet — only from the
      proxy's internal network.

#### nginx example

```nginx
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;
```

> **Warning:** If OpsDrop is directly exposed without a proxy,
> `middleware.RealIP` allows clients to spoof their IP by setting
> `X-Forwarded-For`. In that deployment model, consider removing the middleware
> or binding to `127.0.0.1` and using a local proxy.

---

## 6. Request Size Limits

The application limits uploads to 1 GiB. The proxy should enforce a matching (or
lower) limit to reject oversized requests before they consume backend resources:

```nginx
client_max_body_size 1g;
```

For JSON-only endpoints (auth, clipboard), a tighter limit is appropriate:

```nginx
location /api/v1/auth/ {
    client_max_body_size 64k;
    proxy_pass http://opsdrop:8080;
}
```

---

## 7. Timeouts

Align proxy timeouts with the application's settings (15 s read, 15 s write,
60 s idle):

```nginx
proxy_connect_timeout 10s;
proxy_send_timeout    60s;
proxy_read_timeout    60s;
```

For file uploads, longer timeouts may be needed depending on expected file sizes
and upload speeds.

---

## 8. Network Segmentation

- Expose only the proxy to the public internet.
- Place OpsDrop and its database volume on an internal-only network.
- If using Docker Compose, use a dedicated bridge network and don't publish
  OpsDrop's port directly.
- If using Kubernetes, apply `NetworkPolicy` resources to restrict ingress
  to only the ingress controller and egress to only required services.

### Docker Compose example

```yaml
services:
  proxy:
    image: nginx:alpine
    ports:
      - "443:443"
    networks:
      - frontend
      - backend
  opsdrop:
    image: opsdrop/server:latest
    # Do NOT publish ports — only reachable via the proxy network
    networks:
      - backend
    environment:
      SERVER_TLS_ENABLED: "false"

networks:
  frontend:
  backend:
    internal: true
```

---

## 9. DDoS Protection

For internet-facing deployments, place a DDoS mitigation layer in front of the
proxy:

- **Cloud providers:** AWS Shield, Cloudflare, GCP Cloud Armor.
- **Self-hosted:** Fail2ban for SSH and application-layer bans,
  connection-level rate limiting at the firewall (iptables/nftables).

---

## 10. Logging & Monitoring

### Proxy-level logging

- Log all requests with timestamps, client IPs, response codes, and
  response times.
- Alert on spikes in 401/403/429 responses (brute-force indicators).
- Alert on sustained high upload volume to `/api/v1/public/files`.

### Application-level

OpsDrop writes audit logs to the `audit_logs` table for all security-relevant
actions (login, logout, register, upload, download, delete). Query this table
for forensic investigations.

---

## 11. Environment Variables Reference

| Variable | Default | Description |
|---|---|---|
| `SERVER_JWT_SECRET` | *(required, min 32 chars)* | HMAC key for JWT signing. Use `openssl rand -hex 32`. |
| `SERVER_ADDRESS` | `:8443` (TLS) / `:8080` | Listen address. |
| `SERVER_TLS_ENABLED` | `true` | Set `false` when terminating TLS at the proxy. |
| `SERVER_TLS_CERT` | `certs/server.crt` | Path to TLS certificate (passthrough mode). |
| `SERVER_TLS_KEY` | `certs/server.key` | Path to TLS private key (passthrough mode). |
| `SERVER_DATABASE` | `data/server.db` | SQLite database path. |
| `SERVER_STORAGE_DIR` | `data/storage` | Directory for uploaded files. |
| `REGISTRATION_ENABLED` | `true` | Set `false` to disable public account registration. |
| `MAX_UPLOAD_SIZE_BYTES` | `0` (unlimited) | Maximum upload size in bytes. `0` means no limit. |
