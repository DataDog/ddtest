.DEFAULT_GOAL := build
.PHONY: clean fmt vet lint build run release
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/DataDog/ddtest/internal/buildinfo.Version=$(VERSION)
clean:
	go clean -i -x
fmt:
	go fmt ./...
vet: fmt
	go vet ./...
lint: vet
	golangci-lint run --timeout=5m
test:
	go test ./...
build: test lint
	go build -ldflags="$(LDFLAGS)" -o ddtest main.go
run:
	go run main.go
release:
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/ddtest-linux-amd64 main.go
	GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/ddtest-linux-arm64 main.go
	GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/ddtest-darwin-amd64 main.go
	GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/ddtest-darwin-arm64 main.go
	GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/ddtest-windows-amd64.exe main.go
	GOOS=windows GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/ddtest-windows-arm64.exe main.go
