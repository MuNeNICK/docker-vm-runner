package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/munenick/docker-vm-runner/internal/cli"
)

var signalNotifyContext = signal.NotifyContext

func main() {
	ctx, stop := rootContext()
	defer stop()
	os.Exit(cli.Main(ctx, os.Args[1:]))
}

func rootContext() (context.Context, context.CancelFunc) {
	return signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
