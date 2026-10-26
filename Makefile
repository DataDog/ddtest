.DEFAULT_GOAL := build
.PHONY: clean fmt vet lint test build install run release
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/DataDog/ddtest/internal/buildinfo.Version=$(VERSION)
GOBIN ?= $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN := $(firstword $(subst :, ,$(shell go env GOPATH)))/bin
endif
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
install:
	GOBIN="$(GOBIN)" go install -ldflags="$(LDFLAGS)" .
	@echo "Installed ddtest to $(GOBIN)/ddtest"
run:
	go run main.go
# ddto's release builder links ddtest with the runtime sources and the tool
# wrapper that ddtest instrument provides to the projects it instruments.
release:
	mkdir -p dist
	go -C ddto run ./scripts/release -main .. -name ddtest -ldflags "-X github.com/DataDog/ddtest/internal/buildinfo.Version=$(VERSION)" -o ../dist
