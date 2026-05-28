package redfish

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/munenick/docker-vm-runner/internal/process"
	"golang.org/x/crypto/bcrypt"
)

func TestStartDisabledNoop(t *testing.T) {
	manager := NewManager(Options{StateDir: t.TempDir()})
	called := false
	manager.StartProcess = func(context.Context, process.Command) (Process, error) {
		called = true
		return fakeProcess{}, nil
	}

	result, err := manager.Start(context.Background(), Request{Enabled: false})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if result.Started {
		t.Fatal("Started = true")
	}
	if called {
		t.Fatal("StartProcess was called")
	}
}

func TestStartWritesAuthConfigAndCommand(t *testing.T) {
	stateDir := t.TempDir()
	var got process.Command
	manager := NewManager(Options{StateDir: stateDir})
	manager.Sleep = func(context.Context, time.Duration) error { return nil }
	manager.StartProcess = func(ctx context.Context, cmd process.Command) (Process, error) {
		got = cmd
		return fakeProcess{running: true}, nil
	}

	result, err := manager.Start(context.Background(), Request{
		Enabled:    true,
		User:       "operator",
		Password:   "secret",
		Port:       9443,
		LibvirtURI: "qemu:///system",
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !result.Started {
		t.Fatal("Started = false")
	}
	if got.Name != "sushy-emulator" {
		t.Fatalf("Name = %q", got.Name)
	}
	wantArgs := []string{"--config", result.ConfigPath, "--libvirt-uri", "qemu:///system"}
	if strings.Join(got.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("Args = %#v", got.Args)
	}

	authText := readText(t, result.AuthPath)
	parts := strings.Split(strings.TrimSpace(authText), ":")
	if len(parts) != 2 || parts[0] != "operator" {
		t.Fatalf("auth file = %q", authText)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(parts[1]), []byte("secret")); err != nil {
		t.Fatalf("password hash does not match: %v", err)
	}

	configText := readText(t, result.ConfigPath)
	for _, needle := range []string{
		`SUSHY_EMULATOR_LIBVIRT_URI = "qemu:///system"`,
		`SUSHY_EMULATOR_LISTEN_IP = "0.0.0.0"`,
		`SUSHY_EMULATOR_LISTEN_PORT = 9443`,
		`SUSHY_EMULATOR_SSL_CERT = "` + filepath.Join(stateDir, "certs", "sushy.crt") + `"`,
		`SUSHY_EMULATOR_SSL_KEY = "` + filepath.Join(stateDir, "certs", "sushy.key") + `"`,
		`SUSHY_EMULATOR_AUTH_FILE = "` + filepath.Join(stateDir, "sushy", "htpasswd") + `"`,
	} {
		if !strings.Contains(configText, needle) {
			t.Fatalf("config missing %q:\n%s", needle, configText)
		}
	}
	if _, err := os.Stat(result.CertPath); err != nil {
		t.Fatalf("stat cert: %v", err)
	}
	if _, err := os.Stat(result.KeyPath); err != nil {
		t.Fatalf("stat key: %v", err)
	}
}

func TestStartReusesExistingCertificatePair(t *testing.T) {
	stateDir := t.TempDir()
	certPath := filepath.Join(stateDir, "certs", "sushy.crt")
	keyPath := filepath.Join(stateDir, "certs", "sushy.key")
	writeFile(t, certPath, []byte("existing cert"), 0o644)
	writeFile(t, keyPath, []byte("existing key"), 0o600)
	manager := NewManager(Options{StateDir: stateDir})
	manager.Sleep = func(context.Context, time.Duration) error { return nil }
	manager.StartProcess = func(context.Context, process.Command) (Process, error) {
		return fakeProcess{running: true}, nil
	}

	if _, err := manager.Start(context.Background(), Request{Enabled: true}); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if got := readText(t, certPath); got != "existing cert" {
		t.Fatalf("cert was overwritten: %q", got)
	}
	if got := readText(t, keyPath); got != "existing key" {
		t.Fatalf("key was overwritten: %q", got)
	}
}

func TestStartMissingSushyEmulator(t *testing.T) {
	manager := NewManager(Options{StateDir: t.TempDir()})
	manager.StartProcess = func(context.Context, process.Command) (Process, error) {
		return nil, os.ErrNotExist
	}

	_, err := manager.Start(context.Background(), Request{Enabled: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "sushy-emulator not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartFailsWhenProcessExits(t *testing.T) {
	manager := NewManager(Options{StateDir: t.TempDir()})
	manager.Sleep = func(context.Context, time.Duration) error { return nil }
	manager.StartProcess = func(context.Context, process.Command) (Process, error) {
		return fakeProcess{running: false, stderr: "bind failed"}, nil
	}

	_, err := manager.Start(context.Background(), Request{Enabled: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "sushy-emulator failed to start: bind failed") {
		t.Fatalf("unexpected error: %v", err)
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

func readText(t *testing.T, path string) string {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(bytes)
}

func writeFile(t *testing.T, path string, content []byte, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
