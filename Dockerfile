############################
# Build stage
############################
FROM golang:1.26-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates upx

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-X opsdrop/internal/version.Version=${VERSION} -X opsdrop/internal/version.Commit=${COMMIT} -X opsdrop/internal/version.Date=${DATE}" \
    -o /out/opsdrop-server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-X opsdrop/internal/version.Version=${VERSION} -X opsdrop/internal/version.Commit=${COMMIT} -X opsdrop/internal/version.Date=${DATE}" \
    -o /out/opsdrop ./cmd/opsdrop

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
