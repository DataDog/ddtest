.DEFAULT_GOAL := build
.PHONY: clean fmt vet build run
clean:
	go clean -i -x
fmt:
	go fmt ./...
vet: fmt
	go vet ./...
build: vet
	go build -o ddruntest main.go
run:
	go run main.go
