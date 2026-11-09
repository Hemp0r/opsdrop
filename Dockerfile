############################
# Build stage
############################
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

# Cross-compile natively on the build host (no QEMU needed for Go). GOARM is
# only meaningful for 32-bit ARM (linux/arm/v6, linux/arm/v7).
RUN set -eu; \
    if [ "$TARGETARCH" = "arm" ]; then export GOARM="${TARGETVARIANT#v}"; fi; \
    LDFLAGS="-X opsdrop/internal/version.Version=${VERSION} -X opsdrop/internal/version.Commit=${COMMIT} -X opsdrop/internal/version.Date=${DATE}"; \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "$LDFLAGS" -o /out/opsdrop-server ./cmd/server; \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "$LDFLAGS" -o /out/opsdrop ./cmd/opsdrop

############################
# Runtime stage
############################
FROM alpine:latest

WORKDIR /app

RUN apk add --no-cache ca-certificates su-exec \
    && addgroup -S opsdrop && adduser -S opsdrop -G opsdrop

COPY --from=builder /out/opsdrop-server /usr/local/bin/opsdrop-server
COPY --from=builder /out/opsdrop /usr/local/bin/opsdrop
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod +x /usr/local/bin/docker-entrypoint.sh \
    && mkdir -p /data/storage /certs /app/certs \
    && chown -R opsdrop:opsdrop /data /app/certs

ENV SERVER_ADDRESS=:8443 \
    SERVER_TLS_ENABLED=true \
    SERVER_TLS_CERT=/certs/server.crt \
    SERVER_TLS_KEY=/certs/server.key \
    SERVER_DATABASE=/data/server.db \
    SERVER_STORAGE_DIR=/data/storage \
    REGISTRATION_ENABLED=true \
    MAX_UPLOAD_SIZE_BYTES=0

EXPOSE 8443 8080

VOLUME ["/data", "/certs"]

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["opsdrop-server"]
