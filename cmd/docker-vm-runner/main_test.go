package main

import (
	"context"
	"os"
	"syscall"
	"testing"
)

func TestRootContextWatchesInterruptAndSIGTERM(t *testing.T) {
	original := signalNotifyContext
	defer func() { signalNotifyContext = original }()
	var got []os.Signal
	signalNotifyContext = func(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
		got = append(got, signals...)
		return context.WithCancel(parent)
	}

	_, stop := rootContext()
	stop()

	if len(got) != 2 || got[0] != os.Interrupt || got[1] != syscall.SIGTERM {
		t.Fatalf("signals = %#v", got)
	}
}
