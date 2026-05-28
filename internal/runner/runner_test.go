package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/network"
)

func TestListDistrosFiltersByArch(t *testing.T) {
	path := writeDistroConfig(t)
	var stdout, stderr bytes.Buffer
	r := New()
	r.Stdout = &stdout
	r.Stderr = &stderr
	r.DistroConfigPath = path

	if err := r.Run(context.Background(), Options{ListDistros: true, ListArch: "arm64"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if strings.Contains(stdout.String(), "ubuntu") || !strings.Contains(stdout.String(), "fedora-arm") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "aarch64") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestPrintConfigMasksSensitiveFields(t *testing.T) {
	var out bytes.Buffer
	PrintConfig(&out, config.VM{Password: "secret1", RedfishPassword: "secret2", VMName: "vm1"})

	text := out.String()
	if !strings.Contains(text, "password: ********") || !strings.Contains(text, "redfish_password: ********") {
		t.Fatalf("output = %q", text)
	}
	if strings.Contains(text, "secret1") || strings.Contains(text, "secret2") {
		t.Fatalf("sensitive values leaked: %q", text)
	}
}

func TestAccessLinesIncludeConsoleRedfishAndPublish(t *testing.T) {
	lines := AccessLines(config.VM{
		LoginUser:        "user",
		Password:         "password",
		CloudInitEnabled: true,
		SSHPort:          2222,
		NoVNCEnabled:     true,
		NoVNCPort:        6080,
		RedfishEnabled:   true,
		RedfishPort:      8443,
		NICs:             []network.Config{{Mode: "user", Model: "virtio"}},
		PortForwards:     []network.PortForward{{HostPort: 8080, GuestPort: 80}},
	})
	text := strings.Join(lines, "\n")
	for _, needle := range []string{
		"SSH:     ssh -p 2222 user@localhost",
		"Console: https://localhost:6080/vnc.html",
		"Redfish: https://localhost:8443/",
		"Ports:   8080->80",
		"Publish: ",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %q in:\n%s", needle, text)
		}
	}
}

func TestVMSummaryLines(t *testing.T) {
	lines := VMSummaryLines(config.VM{
		CPUs:           2,
		MemoryMB:       4096,
		DiskSize:       "20G",
		BootMode:       "uefi",
		MachineType:    "q35",
		DiskController: "virtio",
		TPMEnabled:     true,
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
		BootOrder:      []string{"hd"},
	})
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "2 vCPU | 4096 MiB RAM | 20G disk") || !strings.Contains(text, "TPM") {
		t.Fatalf("summary = %q", text)
	}
}

func TestRunLifecycleNoConsole(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "ubuntu", "PERSIST": "1"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{NoConsole: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := "start-services,connect,prepare,start-vm,wait-guest-ready,wait-stopped,mark-installed,cleanup,close,stop-services"
	if got := strings.Join(lifecycle.calls, ","); got != want {
		t.Fatalf("calls = %s want %s", got, want)
	}
}

func TestRunLifecycleCleansUpOnPrepareError(t *testing.T) {
	lifecycle := &fakeLifecycle{prepareErr: os.ErrPermission}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "ubuntu"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{NoConsole: true}); err == nil {
		t.Fatal("expected error")
	}
	want := "start-services,connect,prepare,cleanup,close,stop-services"
	if got := strings.Join(lifecycle.calls, ","); got != want {
		t.Fatalf("calls = %s want %s", got, want)
	}
}

type fakeLifecycle struct {
	calls      []string
	prepareErr error
}

func (l *fakeLifecycle) StartServices(context.Context, config.VM) error {
	l.calls = append(l.calls, "start-services")
	return nil
}
func (l *fakeLifecycle) Connect(context.Context, config.VM) error {
	l.calls = append(l.calls, "connect")
	return nil
}
func (l *fakeLifecycle) Prepare(context.Context, config.VM) error {
	l.calls = append(l.calls, "prepare")
	return l.prepareErr
}
func (l *fakeLifecycle) StartVM(context.Context, config.VM) error {
	l.calls = append(l.calls, "start-vm")
	return nil
}
func (l *fakeLifecycle) WaitForGuestReady(context.Context, config.VM) error {
	l.calls = append(l.calls, "wait-guest-ready")
	return nil
}
func (l *fakeLifecycle) WaitUntilStopped(context.Context, config.VM) error {
	l.calls = append(l.calls, "wait-stopped")
	return nil
}
func (l *fakeLifecycle) AttachConsole(context.Context, config.VM) (int, error) {
	l.calls = append(l.calls, "attach-console")
	return 0, nil
}
func (l *fakeLifecycle) MarkInstalled(context.Context, config.VM) error {
	l.calls = append(l.calls, "mark-installed")
	return nil
}
func (l *fakeLifecycle) Cleanup(context.Context, config.VM) error {
	l.calls = append(l.calls, "cleanup")
	return nil
}
func (l *fakeLifecycle) Close(context.Context, config.VM) error {
	l.calls = append(l.calls, "close")
	return nil
}
func (l *fakeLifecycle) StopServices(context.Context, config.VM) error {
	l.calls = append(l.calls, "stop-services")
	return nil
}

func writeDistroConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "distros.yaml")
	content := `
distributions:
  ubuntu:
    name: Ubuntu
    url: https://example.com/ubuntu.qcow2
    user: user
    arch: x86_64
  fedora-arm:
    name: Fedora ARM
    url: https://example.com/fedora.qcow2
    user: fedora
    arch: aarch64
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write distro config: %v", err)
	}
	return path
}
