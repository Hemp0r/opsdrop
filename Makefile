build-cli:
	CGO_ENABLED=0 go build ./cmd/opsdrop/

build-all:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/opsdrop-linux-amd64 ./cmd/opsdrop/
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/opsdrop-linux-arm64 ./cmd/opsdrop/
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o dist/opsdrop-darwin-amd64 ./cmd/opsdrop/
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/opsdrop-darwin-arm64 ./cmd/opsdrop/
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/opsdrop-windows-amd64.exe ./cmd/opsdrop/
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -o dist/opsdrop-windows-arm64.exe ./cmd/opsdrop/

build-server:
	docker compose up --build

container-build:
	docker build -t hemp0r/opsdrop:latest .

container-push:
	docker push hemp0r/opsdrop:latest

container-release: container-build container-push

helm-release:
	helm package chart/opsdrop
	helm push opsdrop-*.tgz oci://ghcr.io/hemp0r/opsdrop