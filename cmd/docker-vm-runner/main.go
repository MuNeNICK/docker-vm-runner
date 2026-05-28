package main

import (
	"context"
	"os"

	"github.com/munenick/docker-vm-runner/internal/cli"
)

func main() {
	os.Exit(cli.Main(context.Background(), os.Args[1:]))
}
