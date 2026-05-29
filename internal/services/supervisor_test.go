package services

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/munenick/docker-vm-runner/internal/process"
)

func TestStartLibvirtBuildsCommandsAndCleansSockets(t *testing.T) {
	root := t.TempDir()
	var commands []process.Command
	var cleaned []string
	supervisor := NewSupervisor(Options{
		RunDir:        filepath.Join(root, "run-libvirt"),
		VarRunDir:     filepath.Join(root, "var-run-libvirt"),
		LibvirtdConf:  filepath.Join(root, "libvirtd.conf"),
		VirtlogdConf:  filepath.Join(root, "virtlogd.conf"),
		LibvirtdPath:  "/usr/sbin/libvirtd",
		VirtlogdPath:  "/usr/sbin/virtlogd",
		Runtime:       RuntimeInfo{Rootless: false},
		SocketCleaner: func(path string) error { cleaned = append(cleaned, path); return nil },
	})
	writeFile(t, supervisor.Options.LibvirtdConf, []byte("libvirtd"))
	writeFile(t, supervisor.Options.VirtlogdConf, []byte("virtlogd"))
	supervisor.Sleep = func(context.Context, time.Duration) error { return nil }
	supervisor.StartProcess = func(ctx context.Context, cmd process.Command) (Process, error) {
		commands = append(commands, cmd)
		return &fakeProcess{running: true}, nil
	}
	supervisor.WaitPath = func(context.Context, string, time.Duration) bool { return true }

	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if len(cleaned) != 4 {
		t.Fatalf("cleaned sockets = %#v", cleaned)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %#v", commands)
	}
	if commands[0].Name != "/usr/sbin/virtlogd" || commands[0].Args[0] != "-f" || commands[0].Args[1] != supervisor.Options.VirtlogdConf {
		t.Fatalf("virtlogd command = %#v", commands[0])
	}
	if commands[1].Name != "/usr/sbin/libvirtd" || commands[1].Args[0] != "-f" || commands[1].Args[1] != supervisor.Options.LibvirtdConf {
		t.Fatalf("libvirtd command = %#v", commands[1])
	}
	if len(supervisor.Processes) != 2 {
		t.Fatalf("Processes count = %d", len(supervisor.Processes))
	}
}

func TestStartLibvirtOmitsMissingConfig(t *testing.T) {
	root := t.TempDir()
	var commands []process.Command
	supervisor := NewSupervisor(Options{
		RunDir:       filepath.Join(root, "run"),
		VarRunDir:    filepath.Join(root, "var-run"),
		LibvirtdConf: filepath.Join(root, "missing-libvirtd.conf"),
		VirtlogdConf: filepath.Join(root, "missing-virtlogd.conf"),
		LibvirtdPath: "/usr/sbin/libvirtd",
		VirtlogdPath: "/usr/sbin/virtlogd",
	})
	supervisor.Sleep = func(context.Context, time.Duration) error { return nil }
	supervisor.StartProcess = func(ctx context.Context, cmd process.Command) (Process, error) {
		commands = append(commands, cmd)
		return &fakeProcess{running: true}, nil
	}
	supervisor.WaitPath = func(context.Context, string, time.Duration) bool { return true }

	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if len(commands[0].Args) != 0 || len(commands[1].Args) != 0 {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestStartLibvirtProcessExited(t *testing.T) {
	root := t.TempDir()
	supervisor := NewSupervisor(Options{
		RunDir:    filepath.Join(root, "run"),
		VarRunDir: filepath.Join(root, "var-run"),
		Runtime:   RuntimeInfo{Rootless: false},
	})
	supervisor.Sleep = func(context.Context, time.Duration) error { return nil }
	supervisor.StartProcess = func(ctx context.Context, cmd process.Command) (Process, error) {
		return &fakeProcess{running: false, exitCode: 1, stderr: "failed"}, nil
	}

	err := supervisor.Start(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "virtlogd exited prematurely") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartRollsBackStartedProcessesOnFailure(t *testing.T) {
	root := t.TempDir()
	virtlogd := &fakeProcess{running: true}
	supervisor := NewSupervisor(Options{
		RunDir:    filepath.Join(root, "run"),
		VarRunDir: filepath.Join(root, "var-run"),
		Runtime:   RuntimeInfo{Rootless: false},
	})
	supervisor.Sleep = func(context.Context, time.Duration) error { return nil }
	startCalls := 0
	supervisor.StartProcess = func(ctx context.Context, cmd process.Command) (Process, error) {
		startCalls++
		if startCalls == 1 {
			return virtlogd, nil
		}
		return nil, errors.New("libvirtd failed")
	}

	err := supervisor.Start(context.Background())
	if err == nil {
		t.Fatal("expected start error")
	}
	if virtlogd.terminateCalls != 1 {
		t.Fatalf("terminateCalls = %d", virtlogd.terminateCalls)
	}
}

func TestStartTerminatesProcessWhenPostStartSleepFails(t *testing.T) {
	root := t.TempDir()
	virtlogd := &fakeProcess{running: true}
	supervisor := NewSupervisor(Options{
		RunDir:    filepath.Join(root, "run"),
		VarRunDir: filepath.Join(root, "var-run"),
		Runtime:   RuntimeInfo{Rootless: false},
	})
	supervisor.StartProcess = func(ctx context.Context, cmd process.Command) (Process, error) {
		return virtlogd, nil
	}
	supervisor.Sleep = func(context.Context, time.Duration) error { return context.Canceled }

	err := supervisor.Start(context.Background())
	if err == nil {
		t.Fatal("expected start error")
	}
	if virtlogd.terminateCalls != 1 {
		t.Fatalf("terminateCalls = %d", virtlogd.terminateCalls)
	}
}

func TestWaitForLibvirtRootlessWarnsInsteadOfError(t *testing.T) {
	var warnings bytes.Buffer
	supervisor := NewSupervisor(Options{Runtime: RuntimeInfo{Rootless: true}, WarningWriter: &warnings})
	supervisor.WaitPath = func(context.Context, string, time.Duration) bool { return false }

	if err := supervisor.WaitForLibvirt(context.Background()); err != nil {
		t.Fatalf("WaitForLibvirt returned error: %v", err)
	}
	if !strings.Contains(warnings.String(), "libvirt socket did not appear") {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestWaitForLibvirtNonRootlessRaises(t *testing.T) {
	supervisor := NewSupervisor(Options{Runtime: RuntimeInfo{Rootless: false}})
	supervisor.WaitPath = func(context.Context, string, time.Duration) bool { return false }

	err := supervisor.WaitForLibvirt(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "libvirt socket did not appear") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "--privileged") || !strings.Contains(err.Error(), "--cgroupns=host") {
		t.Fatalf("error lacks runtime hints: %v", err)
	}
}

func TestCleanupStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "libvirt-sock")
	writeFile(t, path, []byte("socket-placeholder"))
	supervisor := NewSupervisor(Options{})
	supervisor.SocketProbe = func(string) SocketState { return SocketStale }

	if err := supervisor.CleanupSocket(path); err != nil {
		t.Fatalf("CleanupSocket returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket still exists or unexpected stat error: %v", err)
	}
}

func TestCleanupActiveSocketIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "libvirt-sock")
	writeFile(t, path, []byte("socket-placeholder"))
	supervisor := NewSupervisor(Options{})
	supervisor.SocketProbe = func(string) SocketState { return SocketActive }

	if err := supervisor.CleanupSocket(path); err != nil {
		t.Fatalf("CleanupSocket returned error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("socket was removed: %v", err)
	}
}

func TestStopTerminatesAndKillsOnTimeout(t *testing.T) {
	running := &fakeProcess{running: true, waitErr: ErrWaitTimeout}
	exited := &fakeProcess{running: false}
	supervisor := NewSupervisor(Options{})
	supervisor.Processes = []Process{running, exited}

	if err := supervisor.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if err := supervisor.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop returned error: %v", err)
	}
	if running.terminateCalls != 1 {
		t.Fatalf("terminateCalls = %d", running.terminateCalls)
	}
	if running.killCalls != 1 {
		t.Fatalf("killCalls = %d", running.killCalls)
	}
	if exited.terminateCalls != 0 {
		t.Fatalf("exited terminateCalls = %d", exited.terminateCalls)
	}
}

func TestStopReturnsWaitError(t *testing.T) {
	proc := &fakeProcess{running: true, waitErr: errors.New("wait failed")}
	supervisor := NewSupervisor(Options{})
	supervisor.Processes = []Process{proc}

	err := supervisor.Stop(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStartProcessIsStoppedExplicitlyNotByContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	proc, err := startProcess(ctx, process.Command{Name: "sleep", Args: []string{"2"}})
	if err != nil {
		t.Fatalf("startProcess returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = proc.Kill()
		_ = proc.Wait(context.Background(), time.Second)
	})

	cancel()
	time.Sleep(50 * time.Millisecond)

	if !proc.Running() {
		t.Fatal("process was stopped by context cancellation")
	}
	if err := proc.Terminate(); err != nil {
		t.Fatalf("Terminate returned error: %v", err)
	}
	_ = proc.Wait(context.Background(), time.Second)
}

type fakeProcess struct {
	running        bool
	exitCode       int
	stderr         string
	waitErr        error
	terminateCalls int
	killCalls      int
}

func (p *fakeProcess) Running() bool  { return p.running }
func (p *fakeProcess) ExitCode() int  { return p.exitCode }
func (p *fakeProcess) Stderr() string { return p.stderr }
func (p *fakeProcess) Terminate() error {
	p.terminateCalls++
	return nil
}
func (p *fakeProcess) Kill() error {
	p.killCalls++
	p.running = false
	return nil
}
func (p *fakeProcess) Wait(context.Context, time.Duration) error {
	if p.waitErr != nil {
		return p.waitErr
	}
	p.running = false
	return nil
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
