.PHONY: build test fmt

build:
	go build -o bin/docker-vm-runner ./cmd/docker-vm-runner
	go build -o bin/guest-exec ./cmd/guest-exec

test:
	go test ./...

fmt:
	go fmt ./...
