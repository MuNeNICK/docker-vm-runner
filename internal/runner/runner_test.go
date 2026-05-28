package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/libvirtmgr"
	"github.com/munenick/docker-vm-runner/internal/network"
	"github.com/munenick/docker-vm-runner/internal/paths"
	"github.com/munenick/docker-vm-runner/internal/redfish"
	"github.com/munenick/docker-vm-runner/internal/tpm"
	"github.com/munenick/docker-vm-runner/internal/vncproxy"
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

func TestConcreteLifecycleStartsServices(t *testing.T) {
	service := &fakeServiceSupervisor{}
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	redfishManager := &fakeRedfishManager{}
	novnc := &fakeNoVNCProxy{}
	lifecycle := NewConcreteLifecycle(testLayout(t))
	lifecycle.Service = service
	lifecycle.Manager = manager
	lifecycle.Redfish = redfishManager
	lifecycle.NoVNC = novnc
	lifecycle.RedfishPool = libvirtmgr.StoragePoolRequest{Name: "default", TargetPath: filepath.Join(t.TempDir(), "pool")}

	err := lifecycle.StartServices(context.Background(), config.VM{
		RedfishEnabled:  true,
		RedfishUser:     "admin",
		RedfishPassword: "secret",
		RedfishPort:     8443,
		NoVNCEnabled:    true,
		NoVNCPort:       6080,
		VNCPort:         5900,
	})
	if err != nil {
		t.Fatalf("StartServices returned error: %v", err)
	}
	if service.startCalls != 1 || manager.storagePoolCalls != 1 || !redfishManager.started || !novnc.started {
		t.Fatalf("service=%d storage=%d redfish=%v novnc=%v", service.startCalls, manager.storagePoolCalls, redfishManager.started, novnc.started)
	}
}

func TestConcreteLifecyclePrepareDefinesDomain(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, "ubuntu.qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil

	err := lifecycle.Prepare(context.Background(), config.VM{
		Distro:         "ubuntu",
		VMName:         "vm1",
		Arch:           "x86_64",
		BootMode:       "legacy",
		ImageFormat:    "qcow2",
		CPUModel:       "qemu64",
		MemoryMB:       1024,
		CPUs:           1,
		DiskSize:       "10G",
		BootOrder:      []string{"hd"},
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if manager.definedName != "vm1" || !strings.Contains(manager.definedXML, `<domain type=`) {
		t.Fatalf("definedName=%q xml=%s", manager.definedName, manager.definedXML)
	}
	if got := readFileString(t, filepath.Join(layout.VMImagesDir, "vm1", "disk.qcow2")); got != "base" {
		t.Fatalf("work image = %q", got)
	}
}

type fakeLifecycle struct {
	calls      []string
	prepareErr error
}

type fakeServiceSupervisor struct {
	startCalls int
	stopCalls  int
}

func (s *fakeServiceSupervisor) Start(context.Context) error {
	s.startCalls++
	return nil
}

func (s *fakeServiceSupervisor) Stop(context.Context) error {
	s.stopCalls++
	return nil
}

type fakeRedfishManager struct {
	started bool
}

func (m *fakeRedfishManager) Start(context.Context, redfish.Request) (redfish.Result, error) {
	m.started = true
	return redfish.Result{Started: true}, nil
}

type fakeNoVNCProxy struct {
	started bool
	stopped bool
}

func (p *fakeNoVNCProxy) Start(context.Context, vncproxy.Request) (vncproxy.Result, error) {
	p.started = true
	return vncproxy.Result{Started: true}, nil
}

func (p *fakeNoVNCProxy) Stop(context.Context) error {
	p.stopped = true
	return nil
}

type fakeLibvirtManager struct {
	domain           libvirtmgr.Domain
	definedName      string
	definedXML       string
	storagePoolCalls int
}

func (m *fakeLibvirtManager) EnsureDefined(name string, xml string) (libvirtmgr.Domain, error) {
	m.definedName = name
	m.definedXML = xml
	return m.domain, nil
}

func (m *fakeLibvirtManager) Start(libvirtmgr.Domain) error { return nil }
func (m *fakeLibvirtManager) Cleanup(libvirtmgr.Domain, libvirtmgr.CleanupOptions) error {
	return nil
}
func (m *fakeLibvirtManager) EnsureStoragePool(libvirtmgr.StoragePoolRequest) (libvirtmgr.StoragePool, error) {
	m.storagePoolCalls++
	return nil, nil
}
func (m *fakeLibvirtManager) Close() error { return nil }

type fakeLibvirtDomain struct {
	name   string
	active bool
}

func (d *fakeLibvirtDomain) Name() string                  { return d.name }
func (d *fakeLibvirtDomain) IsActive() (bool, error)       { return d.active, nil }
func (d *fakeLibvirtDomain) Create() error                 { d.active = true; return nil }
func (d *fakeLibvirtDomain) Shutdown() error               { d.active = false; return nil }
func (d *fakeLibvirtDomain) Destroy() error                { d.active = false; return nil }
func (d *fakeLibvirtDomain) Undefine() error               { return nil }
func (d *fakeLibvirtDomain) UndefineNVRAM() error          { return nil }
func (d *fakeLibvirtDomain) unusedTpmReference(tpm.Result) {}

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

func testLayout(t *testing.T) paths.Layout {
	t.Helper()
	root := t.TempDir()
	return paths.Layout{
		DataDir:       root,
		ImagesDir:     root,
		BaseImagesDir: filepath.Join(root, "base"),
		VMImagesDir:   filepath.Join(root, "vms"),
		StateDir:      filepath.Join(root, "state"),
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
