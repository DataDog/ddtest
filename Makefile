.DEFAULT_GOAL := build
.PHONY: clean fmt vet lint build run
clean:
	go clean -i -x
fmt:
	go fmt ./...
vet: fmt
	go vet ./...
lint: vet
	golangci-lint run --timeout=5m
test: lint
	go test ./...
build: test
	go build -o ddruntest main.go
run:
	go run main.go
