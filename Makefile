.PHONY: build test fmt

build:
	go build -o bin/docker-vm-runner ./cmd/docker-vm-runner

test:
	go test ./...

fmt:
	go fmt ./...
