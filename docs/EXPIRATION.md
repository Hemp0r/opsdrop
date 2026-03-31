# File Expiration Configuration

This document explains how to configure file expiration/TTL (Time To Live) settings for uploads in opsdrop.

## Overview

Opsdrop supports configurable expiration times for both private (authenticated) and public (anonymous) uploads. You can set default values and maximum values that users can specify.

## Configuration Options

All expiration settings can be configured using environment variables. They support both hours (`h`) and days (`d`) formats.

### Private Upload Settings

- **`DEFAULT_PRIVATE_TTL`** (default: `14d`)
  - Default expiration time for private (authenticated) uploads
  - Examples: `7d`, `24h`, `168h`, `14d`

- **`MAX_PRIVATE_TTL`** (default: `14d`)
  - Maximum expiration time that users can request for private uploads
  - Users can override the default up to this limit using the `retention_days` field
  - Examples: `30d`, `720h`, `14d`

### Public Upload Settings

- **`DEFAULT_PUBLIC_TTL`** (default: `48h`)
  - Default expiration time for public (anonymous) uploads
  - Examples: `48h`, `2d`, `72h`, `7d`

- **`MAX_PUBLIC_TTL`** (default: `48h`)
  - Maximum expiration time that users can request for public uploads
  - Users can override the default up to this limit using the `retention_days` field
  - Examples: `168h`, `7d`, `48h`

## Format Syntax

The expiration values support the following formats:

- **Hours**: Use `h` suffix (e.g., `24h`, `48h`, `168h`)
- **Days**: Use `d` suffix (e.g., `1d`, `7d`, `14d`, `30d`)
- **Standard Go durations**: Also supports Go's standard duration format (e.g., `1h30m`, `2h`)

## Usage Examples

### Docker / Docker Compose

```yaml
services:
  opsdrop:
    environment:
      - DEFAULT_PRIVATE_TTL=7d
      - MAX_PRIVATE_TTL=30d
      - DEFAULT_PUBLIC_TTL=24h
      - MAX_PUBLIC_TTL=72h
```

### Kubernetes / Helm

Update your `values.yaml`:

```yaml
env:
  - name: DEFAULT_PRIVATE_TTL
    value: "7d"
  - name: MAX_PRIVATE_TTL
    value: "30d"
  - name: DEFAULT_PUBLIC_TTL
    value: "24h"
  - name: MAX_PUBLIC_TTL
    value: "72h"
```

### Standalone Server

```bash
export DEFAULT_PRIVATE_TTL=7d
export MAX_PRIVATE_TTL=30d
export DEFAULT_PUBLIC_TTL=24h
export MAX_PUBLIC_TTL=72h
./server
```

## Client Override Behavior

When users upload files, they can specify `retention_days` to override the default expiration:

- For **private uploads**: The value is clamped between 1 day and `MAX_PRIVATE_TTL`
- For **public uploads**: The value is clamped between 1 day and `MAX_PUBLIC_TTL`

If the user-specified value exceeds the maximum, it will be automatically capped at the maximum value.

### Example API Call

```bash
# Upload with custom 7-day retention
curl -X POST https://server/api/v1/files \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@myfile.txt" \
  -F "retention_days=7"
```

## Server Capabilities

The server exposes these settings via the capabilities endpoint (`/.well-known/opsdrop-capabilities`):

```json
{
  "default_ttl_seconds": 1209600,           // Default private TTL
  "max_ttl_seconds": 1209600,               // Max private TTL
  "default_public_ttl_seconds": 172800,     // Default public TTL
  "max_public_ttl_seconds": 172800          // Max public TTL
}
```

Clients can query this endpoint to discover the server's expiration settings.

## Validation Rules

The configuration loader validates that:

1. `DEFAULT_PRIVATE_TTL` ≤ `MAX_PRIVATE_TTL`
2. `DEFAULT_PUBLIC_TTL` ≤ `MAX_PUBLIC_TTL`
3. All values must be positive durations

If validation fails, the server will not start and will log an error message.

## Configuration Logging

When the server starts, it logs the configured expiration settings:

```
Configuration:
  ...
  Default private TTL:  14 days
  Max private TTL:      14 days
  Default public TTL:   48 hours
  Max public TTL:       48 hours
```

This helps verify that your configuration is loaded correctly.
