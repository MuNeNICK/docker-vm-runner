package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/munenick/docker-vm-runner/internal/cli"
)

var signalNotify = signal.Notify
var signalStop = signal.Stop

func main() {
	ctx, stop := rootContext()
	defer stop()
	os.Exit(cli.Main(ctx, os.Args[1:]))
}

func rootContext() (context.Context, context.CancelFunc) {
	signals := make(chan os.Signal, 2)
	signalNotify(signals, os.Interrupt, syscall.SIGTERM)
	ctx, cancel := rootContextFromSignals(context.Background(), signals, os.Stderr, 3*time.Second, time.Now)
	return ctx, func() {
		signalStop(signals)
		cancel()
	}
}

func rootContextFromSignals(parent context.Context, signals <-chan os.Signal, stderr io.Writer, interruptWindow time.Duration, now func() time.Time) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if now == nil {
		now = time.Now
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var lastInterrupt time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-signals:
				if sig == syscall.SIGTERM {
					cancel()
					return
				}
				if sig != os.Interrupt {
					continue
				}
				current := now()
				if !lastInterrupt.IsZero() && current.Sub(lastInterrupt) <= interruptWindow {
					cancel()
					return
				}
				lastInterrupt = current
				if stderr != nil {
					fmt.Fprintln(stderr, "[WARN] VM is still running. Press Ctrl+C again within 3s to stop it.")
				}
			}
		}
	}()
	return ctx, func() {
		cancel()
		<-done
	}
}
