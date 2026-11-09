VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X opsdrop/internal/version.Version=$(VERSION) -X opsdrop/internal/version.Commit=$(COMMIT) -X opsdrop/internal/version.Date=$(DATE)

# Release targets (GOOS/GOARCH). linux/arm is built as ARMv7 (GOARM=7).
# Keep in sync with .github/workflows/release-cli.yml.
PLATFORMS := linux/amd64 linux/arm64 linux/arm darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

# Container image platforms for multi-arch builds (amd64, ARM64 incl. Apple
# Silicon / Graviton / Raspberry Pi 4+, and 32-bit ARMv7).
IMAGE               ?= hemp0r/opsdrop:latest
CONTAINER_PLATFORMS ?= linux/amd64,linux/arm64,linux/arm/v7

.PHONY: build-cli build-all build-server test container-build container-push container-release helm-release

build-cli:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" ./cmd/opsdrop/

# Cross-compiles the CLI and server for every release platform into dist/.
build-all:
	@mkdir -p dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; suffix=$$os-$$arch; ext=""; \
		if [ "$$arch" = "arm" ]; then suffix=$$os-armv7; fi; \
		if [ "$$os" = "windows" ]; then ext=.exe; fi; \
		echo "==> $$p"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=7 go build -ldflags "$(LDFLAGS)" -o dist/opsdrop-$$suffix$$ext ./cmd/opsdrop/; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=7 go build -ldflags "$(LDFLAGS)" -o dist/opsdrop-server-$$suffix$$ext ./cmd/server/; \
	done

test:
	go vet ./...
	go test ./...

build-server:
	docker compose up --build

# Builds an image for the host architecture only (arm64 on Apple Silicon).
container-build:
	docker build -t $(IMAGE) .

container-push:
	docker push $(IMAGE)

# Builds and pushes a multi-arch manifest. Plain `docker build` + push from an
# Apple Silicon Mac would publish an arm64-only image and break amd64 hosts.
container-release:
	docker buildx build --platform $(CONTAINER_PLATFORMS) \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
		-t $(IMAGE) --push .

helm-release:
	helm package chart/opsdrop
	helm push opsdrop-*.tgz oci://ghcr.io/hemp0r/opsdrop
