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
# wrapper that ddtest instrument provides to the projects it instruments, and
# gives them ddtest's version. It takes SemVer, so a version such as
# pre-release-<sha> becomes v0.0.0-pre-release-<sha>.
RELEASE_BUILD = go -C ddto run ./scripts/release -main .. -name ddtest -version $(if $(filter v%,$(VERSION)),$(VERSION),v0.0.0-$(VERSION)) -ldflags "-X github.com/DataDog/ddtest/internal/buildinfo.Version=$(VERSION)" -o ../dist
release:
	mkdir -p dist
	$(RELEASE_BUILD) -target linux/amd64
	$(RELEASE_BUILD) -target linux/arm64
	$(RELEASE_BUILD) -target darwin/amd64
	$(RELEASE_BUILD) -target darwin/arm64
	$(RELEASE_BUILD) -target windows/amd64
	$(RELEASE_BUILD) -target windows/arm64
