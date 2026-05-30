package ipmi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/munenick/docker-vm-runner/internal/process"
)

func TestStartDisabledNoop(t *testing.T) {
	manager := NewManager(Options{StateDir: t.TempDir()})
	startCalled := false
	manager.StartProcess = func(context.Context, process.Command) (Process, error) {
		startCalled = true
		return &fakeProcess{}, nil
	}
	manager.RunCommand = func(context.Context, process.Command) (process.Result, error) {
		t.Fatal("RunCommand should not be called")
		return process.Result{}, nil
	}

	result, err := manager.Start(context.Background(), Request{Enabled: false})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if result.Started {
		t.Fatal("Started = true")
	}
	if startCalled {
		t.Fatal("StartProcess was called")
	}
}

func TestStartWritesConfigAndRunsVirtualBMCCommands(t *testing.T) {
	stateDir := t.TempDir()
	var started process.Command
	var commands []process.Command
	manager := NewManager(Options{StateDir: stateDir})
	manager.Sleep = func(context.Context, time.Duration) error { return nil }
	manager.StartProcess = func(_ context.Context, cmd process.Command) (Process, error) {
		started = cmd
		return &fakeProcess{running: true}, nil
	}
	manager.RunCommand = func(_ context.Context, cmd process.Command) (process.Result, error) {
		commands = append(commands, cmd)
		return process.Result{}, nil
	}

	result, err := manager.Start(context.Background(), Request{
		Enabled:    true,
		User:       "operator",
		Password:   "secret",
		Port:       6623,
		SystemID:   "vm1",
		LibvirtURI: "qemu:///system",
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !result.Started {
		t.Fatal("Started = false")
	}
	if started.Name != "vbmcd" {
		t.Fatalf("started command = %#v", started)
	}
	if !containsEnv(started.Env, "VIRTUALBMC_CONFIG="+result.ConfigPath) {
		t.Fatalf("vbmcd env missing config: %#v", started.Env)
	}
	if !containsEnv(started.Env, "HOME="+filepath.Join(stateDir, "ipmi", "home")) {
		t.Fatalf("vbmcd env missing home: %#v", started.Env)
	}

	configText := readText(t, result.ConfigPath)
	for _, needle := range []string{
		"[default]",
		"config_dir = " + filepath.Join(stateDir, "ipmi", "instances"),
		"pid_file = " + filepath.Join(stateDir, "ipmi", "master.pid"),
		"[ipmi]",
		"session_timeout = 10",
	} {
		if !strings.Contains(configText, needle) {
			t.Fatalf("config missing %q:\n%s", needle, configText)
		}
	}

	if len(commands) != 3 {
		t.Fatalf("commands = %#v", commands)
	}
	wantDelete := []string{"delete", "vm1"}
	wantAdd := []string{
		"add", "vm1",
		"--address", "0.0.0.0",
		"--port", "6623",
		"--username", "operator",
		"--password", "secret",
		"--libvirt-uri", "qemu:///system",
	}
	wantStart := []string{"start", "vm1"}
	assertCommand(t, commands[0], "vbmc", wantDelete)
	assertCommand(t, commands[1], "vbmc", wantAdd)
	assertCommand(t, commands[2], "vbmc", wantStart)
	for _, cmd := range commands {
		if !containsEnv(cmd.Env, "VIRTUALBMC_CONFIG="+result.ConfigPath) {
			t.Fatalf("vbmc env missing config: %#v", cmd.Env)
		}
	}
}

func TestStartRejectsDefaultPassword(t *testing.T) {
	manager := NewManager(Options{StateDir: t.TempDir()})
	manager.StartProcess = func(context.Context, process.Command) (Process, error) {
		t.Fatal("StartProcess should not be called")
		return nil, nil
	}

	_, err := manager.Start(context.Background(), Request{Enabled: true})
	if err == nil {
		t.Fatal("expected default password error")
	}
	if !strings.Contains(err.Error(), "password must be changed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartMissingVirtualBMC(t *testing.T) {
	manager := NewManager(Options{StateDir: t.TempDir()})
	manager.StartProcess = func(context.Context, process.Command) (Process, error) {
		return nil, os.ErrNotExist
	}

	_, err := manager.Start(context.Background(), Request{Enabled: true, Password: "secret", SystemID: "vm1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "vbmcd not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartStopsProcessWhenCommandFails(t *testing.T) {
	proc := &fakeProcess{running: true}
	manager := NewManager(Options{StateDir: t.TempDir()})
	manager.Sleep = func(context.Context, time.Duration) error { return nil }
	manager.StartProcess = func(context.Context, process.Command) (Process, error) {
		return proc, nil
	}
	manager.RunCommand = func(context.Context, process.Command) (process.Result, error) {
		return process.Result{}, os.ErrPermission
	}

	_, err := manager.Start(context.Background(), Request{Enabled: true, Password: "secret", SystemID: "vm1"})
	if err == nil {
		t.Fatal("expected command error")
	}
	if proc.stopCalls != 1 {
		t.Fatalf("stopCalls = %d", proc.stopCalls)
	}
}

func TestStopStopsAndDeletesInstanceBeforeStoppingDaemon(t *testing.T) {
	var commands []process.Command
	proc := &fakeProcess{running: true}
	manager := NewManager(Options{StateDir: t.TempDir()})
	manager.RunCommand = func(_ context.Context, cmd process.Command) (process.Result, error) {
		commands = append(commands, cmd)
		return process.Result{}, nil
	}
	result := Result{Started: true, SystemID: "vm1", ConfigPath: filepath.Join(t.TempDir(), "virtualbmc.conf"), Process: proc}

	if err := manager.Stop(context.Background(), result); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %#v", commands)
	}
	assertCommand(t, commands[0], "vbmc", []string{"stop", "vm1"})
	assertCommand(t, commands[1], "vbmc", []string{"delete", "vm1"})
	if proc.stopCalls != 1 {
		t.Fatalf("stopCalls = %d", proc.stopCalls)
	}
}

type fakeProcess struct {
	running   bool
	stderr    string
	stopCalls int
}

func (p *fakeProcess) Running() bool {
	return p.running
}

func (p *fakeProcess) Stderr() string {
	return p.stderr
}

func (p *fakeProcess) Stop() error {
	p.stopCalls++
	return nil
}

func assertCommand(t *testing.T, got process.Command, wantName string, wantArgs []string) {
	t.Helper()
	if got.Name != wantName {
		t.Fatalf("Name = %q want %q", got.Name, wantName)
	}
	if strings.Join(got.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("Args = %#v want %#v", got.Args, wantArgs)
	}
}

func containsEnv(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func readText(t *testing.T, path string) string {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(bytes)
}
