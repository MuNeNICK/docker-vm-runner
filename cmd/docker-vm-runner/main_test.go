package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRootContextCancelsOnSecondInterruptWithinWindow(t *testing.T) {
	signals := make(chan os.Signal, 2)
	var stderr bytes.Buffer
	now := time.Unix(0, 0)
	ctx, stop := rootContextFromSignals(context.Background(), signals, &stderr, time.Second, func() time.Time { return now })
	defer stop()

	signals <- os.Interrupt
	if !waitUntil(time.Second, func() bool { return stderr.String() != "" }) {
		t.Fatal("first interrupt did not print a warning")
	}
	assertContextActive(t, ctx)

	now = now.Add(500 * time.Millisecond)
	signals <- os.Interrupt
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled on second interrupt")
	}
}

func TestRootContextDoesNotCancelOnLateSecondInterrupt(t *testing.T) {
	signals := make(chan os.Signal, 2)
	var stderr bytes.Buffer
	now := time.Unix(0, 0)
	ctx, stop := rootContextFromSignals(context.Background(), signals, &stderr, time.Second, func() time.Time { return now })
	defer stop()

	signals <- os.Interrupt
	if !waitUntil(time.Second, func() bool { return stderr.String() != "" }) {
		t.Fatal("first interrupt did not print a warning")
	}
	now = now.Add(2 * time.Second)
	signals <- os.Interrupt
	time.Sleep(20 * time.Millisecond)
	assertContextActive(t, ctx)
}

func assertContextActive(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatal("context is canceled")
	default:
	}
}

func waitUntil(timeout time.Duration, check func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return check()
}

func TestRootContextCancelsOnSIGTERM(t *testing.T) {
	signals := make(chan os.Signal, 1)
	ctx, stop := rootContextFromSignals(context.Background(), signals, &bytes.Buffer{}, time.Second, time.Now)
	defer stop()

	signals <- syscall.SIGTERM
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled on SIGTERM")
	}
}

func TestImageKeepsVirshConsoleExecContract(t *testing.T) {
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	content := string(dockerfile)
	for _, want := range []string{
		"LIBVIRT_DEFAULT_URI=qemu:///system",
		"libvirt-clients",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("Dockerfile missing %q", want)
		}
	}
}
