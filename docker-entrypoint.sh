#!/bin/sh
set -e

CERT_SRC="${SERVER_TLS_CERT:-/certs/server.crt}"
KEY_SRC="${SERVER_TLS_KEY:-/certs/server.key}"
CERT_WORK_DIR="/app/certs"
CERT_DST="${CERT_WORK_DIR}/server.crt"
KEY_DST="${CERT_WORK_DIR}/server.key"

# Copy TLS material into a location owned by opsdrop so it can be read even
# when mounted from host with restrictive permissions.
if [ "${SERVER_TLS_ENABLED:-true}" = "true" ] && [ -f "$CERT_SRC" ] && [ -f "$KEY_SRC" ]; then
	mkdir -p "$CERT_WORK_DIR"
	cp "$CERT_SRC" "$CERT_DST"
	cp "$KEY_SRC" "$KEY_DST"
	chown opsdrop:opsdrop "$CERT_DST" "$KEY_DST"
	chmod 600 "$KEY_DST"
	export SERVER_TLS_CERT="$CERT_DST"
	export SERVER_TLS_KEY="$KEY_DST"
fi

if [ "$1" = "" ] || [ "${1#-}" != "$1" ]; then
	set -- opsdrop-server "$@"
fi

# If already running as non-root (e.g. Kubernetes securityContext), skip su-exec.
if [ "$(id -u)" != "0" ]; then
	exec "$@"
fi

exec su-exec opsdrop:opsdrop "$@"

