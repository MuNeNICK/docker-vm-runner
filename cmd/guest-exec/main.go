package main

import (
	"context"
	"os"

	"github.com/munenick/docker-vm-runner/internal/guestexec"
)

func main() {
	os.Exit(guestexec.Main(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
