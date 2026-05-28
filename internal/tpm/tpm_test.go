package tpm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/munenick/docker-vm-runner/internal/process"
)

func TestStartDisabledNoop(t *testing.T) {
	called := false
	supervisor := NewSupervisor(t.TempDir())
	supervisor.StartProcess = func(context.Context, process.Command) (Process, error) {
		called = true
		return fakeProcess{}, nil
	}

	result, err := supervisor.Start(context.Background(), Request{Enabled: false, VMName: "test-vm"})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if result.Started {
		t.Fatalf("Started = true")
	}
	if called {
		t.Fatalf("StartProcess was called")
	}
}

func TestStartBuildsCommandAndCreatesStateDir(t *testing.T) {
	stateDir := t.TempDir()
	var got process.Command
	supervisor := NewSupervisor(stateDir)
	supervisor.Sleep = func(context.Context, time.Duration) error { return nil }
	supervisor.StartProcess = func(ctx context.Context, cmd process.Command) (Process, error) {
		got = cmd
		return fakeProcess{running: true}, nil
	}

	result, err := supervisor.Start(context.Background(), Request{Enabled: true, VMName: "test-vm"})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	tpmDir := filepath.Join(stateDir, "tpm", "test-vm")
	sockPath := filepath.Join(tpmDir, "swtpm-sock")
	wantArgs := []string{
		"socket",
		"--tpmstate", "dir=" + tpmDir,
		"--ctrl", "type=unixio,path=" + sockPath,
		"--tpm2",
	}
	if got.Name != "swtpm" {
		t.Fatalf("Name = %q", got.Name)
	}
	if len(got.Args) != len(wantArgs) {
		t.Fatalf("Args = %#v", got.Args)
	}
	for i := range wantArgs {
		if got.Args[i] != wantArgs[i] {
			t.Fatalf("Args[%d] = %q want %q\n%#v", i, got.Args[i], wantArgs[i], got.Args)
		}
	}
	if result.SocketPath != sockPath {
		t.Fatalf("SocketPath = %q", result.SocketPath)
	}
	if !result.Started {
		t.Fatalf("Started = false")
	}
	if _, err := os.Stat(tpmDir); err != nil {
		t.Fatalf("tpm dir stat: %v", err)
	}
}

func TestStartMissingSwtpm(t *testing.T) {
	supervisor := NewSupervisor(t.TempDir())
	supervisor.StartProcess = func(context.Context, process.Command) (Process, error) {
		return nil, os.ErrNotExist
	}

	_, err := supervisor.Start(context.Background(), Request{Enabled: true, VMName: "test-vm"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "swtpm not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartFailureAfterLaunch(t *testing.T) {
	supervisor := NewSupervisor(t.TempDir())
	supervisor.Sleep = func(context.Context, time.Duration) error { return nil }
	supervisor.StartProcess = func(context.Context, process.Command) (Process, error) {
		return fakeProcess{running: false, stderr: "failed"}, nil
	}

	_, err := supervisor.Start(context.Background(), Request{Enabled: true, VMName: "test-vm"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "swtpm failed to start: failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartSleepCancellation(t *testing.T) {
	supervisor := NewSupervisor(t.TempDir())
	supervisor.StartProcess = func(context.Context, process.Command) (Process, error) {
		return fakeProcess{running: true}, nil
	}
	supervisor.Sleep = func(context.Context, time.Duration) error {
		return context.Canceled
	}

	_, err := supervisor.Start(context.Background(), Request{Enabled: true, VMName: "test-vm"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

type fakeProcess struct {
	running bool
	stderr  string
}

func (p fakeProcess) Running() bool {
	return p.running
}

func (p fakeProcess) Stderr() string {
	return p.stderr
}

func (p fakeProcess) Stop() error {
	return nil
}
