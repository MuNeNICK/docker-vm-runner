package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/guestexec"
	"github.com/munenick/docker-vm-runner/internal/libvirtmgr"
	"github.com/munenick/docker-vm-runner/internal/network"
	"github.com/munenick/docker-vm-runner/internal/paths"
	"github.com/munenick/docker-vm-runner/internal/redfish"
	"github.com/munenick/docker-vm-runner/internal/tpm"
	"github.com/munenick/docker-vm-runner/internal/vmstate"
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
	if strings.Contains(stdout.String(), "ubuntu") || !strings.Contains(stdout.String(), "fedora-42-arm64") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "aarch64") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestListDistrosFiltersByTypeAndSearch(t *testing.T) {
	path := writeDistroConfig(t)
	var stdout, stderr bytes.Buffer
	r := New()
	r.Stdout = &stdout
	r.Stderr = &stderr
	r.DistroConfigPath = path

	if err := r.Run(context.Background(), Options{ListDistros: true, ListType: "cloud image", ListSearch: "ubuntu"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "ubuntu-24.04-server") || strings.Contains(stdout.String(), "fedora-42-arm64") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "type=cloud-image") {
		t.Fatalf("stdout missing type: %q", stdout.String())
	}
	if stderr.String() != "" {
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
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server", "PERSIST": "1"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{NoConsole: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := "start-services,connect,prepare,start-vm,wait-guest-ready,wait-stopped,mark-installed,cleanup,close,stop-services"
	if got := strings.Join(lifecycle.calls, ","); got != want {
		t.Fatalf("calls = %s want %s", got, want)
	}
}

func TestRunWiresDataDirIntoDefaultResolver(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server", "DATA_DIR": t.TempDir()}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{NoConsole: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !containsCall(lifecycle.calls, "mark-installed") {
		t.Fatalf("calls = %s", strings.Join(lifecycle.calls, ","))
	}
}

func TestApplyRuntimeEnvConfiguresConcreteLifecycle(t *testing.T) {
	lifecycle := NewConcreteLifecycle(testLayout(t))
	applyRuntimeEnv(lifecycle, config.MapEnv{
		"LIBVIRT_URI":          "qemu+unix:///system?socket=/custom/libvirt.sock",
		"REDFISH_STORAGE_POOL": "redfish",
		"REDFISH_STORAGE_PATH": "/var/lib/redfish",
	})

	if lifecycle.LibvirtURI != "qemu+unix:///system?socket=/custom/libvirt.sock" {
		t.Fatalf("LibvirtURI = %q", lifecycle.LibvirtURI)
	}
	if lifecycle.RedfishPool.Name != "redfish" {
		t.Fatalf("RedfishPool.Name = %q", lifecycle.RedfishPool.Name)
	}
	if lifecycle.RedfishPool.TargetPath != "/var/lib/redfish" {
		t.Fatalf("RedfishPool.TargetPath = %q", lifecycle.RedfishPool.TargetPath)
	}
}

func TestRunWiresHostFileProbeIntoDefaultResolver(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{
		"DISTRO":               "ubuntu-24.04-server",
		"CLOUD_INIT_USER_DATA": filepath.Join(t.TempDir(), "missing.yaml"),
	}
	r.Lifecycle = lifecycle

	err := r.Run(context.Background(), Options{NoConsole: true})
	if err == nil {
		t.Fatal("expected missing user-data error")
	}
	if !strings.Contains(err.Error(), "CLOUD_INIT_USER_DATA file not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShowXML(t *testing.T) {
	var stdout bytes.Buffer
	r := New()
	r.Stdout = &stdout
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server"}
	r.Lifecycle = &fakeLifecycle{}

	if err := r.Run(context.Background(), Options{ShowXML: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		`<domain type=`,
		`<name>ubuntu-24.04-server</name>`,
		`<disk type="file" device="disk">`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("show XML output missing %q:\n%s", want, output)
		}
	}
}

func TestRunLifecycleCleansUpOnPrepareError(t *testing.T) {
	lifecycle := &fakeLifecycle{prepareErr: os.ErrPermission}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{NoConsole: true}); err == nil {
		t.Fatal("expected error")
	}
	want := "start-services,connect,prepare,cleanup,close,stop-services"
	if got := strings.Join(lifecycle.calls, ","); got != want {
		t.Fatalf("calls = %s want %s", got, want)
	}
}

func TestRunLifecycleUsesFreshContextForCleanup(t *testing.T) {
	lifecycle := &fakeLifecycle{prepareErr: os.ErrPermission}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server"}
	r.Lifecycle = lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := r.Run(ctx, Options{NoConsole: true}); err == nil {
		t.Fatal("expected error")
	}
	if len(lifecycle.cleanupContextErrs) == 0 {
		t.Fatal("cleanup was not called")
	}
	for _, err := range lifecycle.cleanupContextErrs {
		if err != nil {
			t.Fatalf("cleanup context was canceled: %v", err)
		}
	}
}

func TestRunLifecycleStopsServicesWhenStartFails(t *testing.T) {
	lifecycle := &fakeLifecycle{startServicesErr: errors.New("service failed")}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{NoConsole: true}); err == nil {
		t.Fatal("expected error")
	}
	want := "start-services,stop-services"
	if got := strings.Join(lifecycle.calls, ","); got != want {
		t.Fatalf("calls = %s want %s", got, want)
	}
}

func TestRunCleanupModeOnlyCleansStaleResources(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{Cleanup: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := "start-services,connect,cleanup-stale,close,stop-services"
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
	commandLog := installFakeQEMUImg(t)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

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
	if got := readFileString(t, commandLog); !strings.Contains(got, "resize") || !strings.Contains(got, "10G") {
		t.Fatalf("qemu-img commands = %q", got)
	}
}

func TestConcreteLifecyclePrepareRequiresKVM(t *testing.T) {
	lifecycle := NewConcreteLifecycle(testLayout(t))
	lifecycle.Manager = &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle.KVMAvailable = func() bool { return false }

	err := lifecycle.Prepare(context.Background(), config.VM{
		VMName:     "vm1",
		RequireKVM: true,
	})
	if err == nil {
		t.Fatal("expected REQUIRE_KVM error")
	}
	if !strings.Contains(err.Error(), "REQUIRE_KVM=1 requires /dev/kvm") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConcreteLifecyclePreparePassesIPXEROMPath(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, "ubuntu.qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

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
		BootOrder:      []string{"network", "hd"},
		IPXEEnabled:    true,
		IPXEROMPath:    "/tmp/ipxe.rom",
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio", Boot: true}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if !strings.Contains(manager.definedXML, `<rom file="/tmp/ipxe.rom"/>`) {
		t.Fatalf("domain XML missing iPXE ROM:\n%s", manager.definedXML)
	}
}

func TestConcreteLifecyclePreparePassesBlockSectorSize(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, "ubuntu.qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }
	lifecycle.BlockSectorSize = func(path string) (int, bool) {
		return 4096, path == "/dev/testblk"
	}

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
		BlockDevices:   []config.BlockDevice{{Path: "/dev/testblk", Index: 1}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if !strings.Contains(manager.definedXML, `<blockio logical_block_size="4096" physical_block_size="4096"/>`) {
		t.Fatalf("domain XML missing blockio:\n%s", manager.definedXML)
	}
}

func TestConcreteLifecyclePreparePassesIPv6Availability(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, "ubuntu.qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }
	lifecycle.IPv6Available = func() bool { return true }

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
	if !strings.Contains(manager.definedXML, `<ip family="ipv6" address="fec0::2" prefix="64"/>`) {
		t.Fatalf("domain XML missing default IPv6:\n%s", manager.definedXML)
	}
}

func TestConcreteLifecycleResolveBaseImageVerifiesChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("base-image"))
	}))
	defer server.Close()
	sum := sha256.Sum256([]byte("different"))
	lifecycle := NewConcreteLifecycle(testLayout(t))

	_, err := lifecycle.resolveBaseImage(context.Background(), config.VM{
		Distro:                 "ubuntu-24.04-server",
		ImageURL:               server.URL,
		ImageChecksumAlgorithm: "sha256",
		ImageChecksumValue:     hex.EncodeToString(sum[:]),
		ImageFormat:            "qcow2",
		DownloadRetries:        1,
	})
	if err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConcreteLifecycleResolveBootSourceVerifiesCatalogChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("boot-iso"))
	}))
	defer server.Close()
	sum := sha256.Sum256([]byte("different"))
	lifecycle := NewConcreteLifecycle(testLayout(t))

	_, err := lifecycle.resolveBootSource(context.Background(), config.VM{
		BootFrom:              server.URL + "/installer.iso",
		BootChecksumAlgorithm: "sha256",
		BootChecksumValue:     hex.EncodeToString(sum[:]),
		DownloadRetries:       1,
	})
	if err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConcreteLifecyclePrepareSeedISOPassesFilesystems(t *testing.T) {
	layout := testLayout(t)
	installFakeCommand(t, "genisoimage", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\nexit 0\n"
	})
	lifecycle := NewConcreteLifecycle(layout)
	output := filepath.Join(layout.VMImagesDir, "vm1", "seed.iso")

	err := lifecycle.prepareSeedISO(context.Background(), config.VM{
		VMName:     "vm1",
		LoginUser:  "user",
		LoginShell: "/bin/bash",
		Password:   "password",
		Filesystems: []config.FilesystemShare{
			{Target: "data", Driver: "virtiofs"},
			{Target: "ro-share", Driver: "9p", Readonly: true},
		},
	}, output)
	if err != nil {
		t.Fatalf("prepareSeedISO returned error: %v", err)
	}
	vendorData := readFileString(t, filepath.Join(filepath.Dir(output), "vendor-data"))
	for _, want := range []string{
		"- data",
		"- /mnt/data",
		"- virtiofs",
		"- ro-share",
		"- /mnt/ro-share",
		"- 9p",
		"trans=virtio,version=9p2000.L,_netdev,ro",
	} {
		if !strings.Contains(vendorData, want) {
			t.Fatalf("vendor-data missing %q:\n%s", want, vendorData)
		}
	}
}

func TestConcreteLifecyclePrepareKeepsPersistentWorkImage(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(filepath.Join(layout.VMImagesDir, "vm1"), 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	workImage := filepath.Join(layout.VMImagesDir, "vm1", "disk.qcow2")
	if err := os.WriteFile(workImage, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write work image: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

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
		Persist:        true,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got := readFileString(t, workImage); got != "existing" {
		t.Fatalf("work image = %q", got)
	}
}

func TestConcreteLifecyclePrepareExpandsPersistentWorkImage(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	workImage := filepath.Join(vmDir, "disk.qcow2")
	if err := os.WriteFile(workImage, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write work image: %v", err)
	}
	commandLog := installFakeQEMUImgWithInfo(t, 8*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

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
		Persist:        true,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got := readFileString(t, commandLog); !strings.Contains(got, "resize "+workImage+" 10G") {
		t.Fatalf("qemu-img commands = %q", got)
	}
}

func TestConcreteLifecyclePrepareDoesNotShrinkPersistentWorkImage(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	workImage := filepath.Join(vmDir, "disk.qcow2")
	if err := os.WriteFile(workImage, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write work image: %v", err)
	}
	commandLog := installFakeQEMUImgWithInfo(t, 20*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

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
		Persist:        true,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got := readFileString(t, commandLog); strings.Contains(got, "resize") {
		t.Fatalf("qemu-img should not shrink disk:\n%s", got)
	}
}

func TestConcreteLifecyclePrepareDoesNotShrinkCopiedBaseImage(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, "ubuntu.qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	commandLog := installFakeQEMUImgWithInfo(t, 20*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

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
	if got := readFileString(t, commandLog); strings.Contains(got, "resize") {
		t.Fatalf("qemu-img should not shrink copied base image:\n%s", got)
	}
}

func TestConcreteLifecyclePrepareCreatesBlankWorkDisk(t *testing.T) {
	layout := testLayout(t)
	commandLog := installFakeQEMUImg(t)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

	err := lifecycle.Prepare(context.Background(), config.VM{
		Distro:         "blank",
		VMName:         "vm1",
		Arch:           "x86_64",
		BootMode:       "legacy",
		ImageFormat:    "qcow2",
		CPUModel:       "qemu64",
		MemoryMB:       1024,
		CPUs:           1,
		DiskSize:       "8G",
		BootOrder:      []string{"hd"},
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
		BlankWorkDisk:  true,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	want := "create -f qcow2 " + filepath.Join(layout.VMImagesDir, "vm1", "disk.qcow2") + " 8G"
	if got := readFileString(t, commandLog); !strings.Contains(got, want) {
		t.Fatalf("qemu-img commands missing %q:\n%s", want, got)
	}
}

func TestConcreteLifecyclePrepareAttachesBootISOWithBlankWorkDisk(t *testing.T) {
	layout := testLayout(t)
	bootISO := filepath.Join(t.TempDir(), "installer.iso")
	if err := os.WriteFile(bootISO, []byte("iso"), 0o644); err != nil {
		t.Fatalf("write boot iso: %v", err)
	}
	commandLog := installFakeQEMUImg(t)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

	err := lifecycle.Prepare(context.Background(), config.VM{
		Distro:         "custom",
		VMName:         "vm1",
		Arch:           "x86_64",
		BootMode:       "legacy",
		ImageFormat:    "qcow2",
		CPUModel:       "qemu64",
		MemoryMB:       1024,
		CPUs:           1,
		DiskSize:       "8G",
		BootFrom:       bootISO,
		BlankWorkDisk:  true,
		BootOrder:      []string{"cdrom", "hd"},
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if !strings.Contains(manager.definedXML, `<source file="`+bootISO+`"/>`) {
		t.Fatalf("domain XML missing boot ISO %q:\n%s", bootISO, manager.definedXML)
	}
	want := "create -f qcow2 " + filepath.Join(layout.VMImagesDir, "vm1", "disk.qcow2") + " 8G"
	if got := readFileString(t, commandLog); !strings.Contains(got, want) {
		t.Fatalf("qemu-img commands missing %q:\n%s", want, got)
	}
}

func TestConcreteLifecyclePrepareExtractsAAVMFForAarch64(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, "custom.qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	firmwareDir := t.TempDir()
	loader := filepath.Join(firmwareDir, "AAVMF_CODE.fd")
	vars := filepath.Join(firmwareDir, "AAVMF_VARS.fd")
	originalProfile := config.SupportedArchitectures["aarch64"]
	profile := originalProfile
	profile.Firmware = map[string]config.FirmwareProfile{
		"default": {Loader: loader, VarsTemplate: vars},
	}
	config.SupportedArchitectures["aarch64"] = profile
	t.Cleanup(func() { config.SupportedArchitectures["aarch64"] = originalProfile })

	installFakeQEMUImg(t)
	dpkgLog := installFakeCommand(t, "dpkg-deb", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
			"mkdir -p " + shellQuote(firmwareDir) + "\n" +
			"printf loader > " + shellQuote(loader) + "\n" +
			"printf vars > " + shellQuote(vars) + "\n" +
			"exit 0\n"
	})
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "arm-vm"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

	err := lifecycle.Prepare(context.Background(), config.VM{
		Distro:         "custom",
		VMName:         "arm-vm",
		Arch:           "aarch64",
		BootMode:       "uefi",
		ImageFormat:    "qcow2",
		CPUModel:       "cortex-a72",
		MemoryMB:       1024,
		CPUs:           1,
		DiskSize:       "10G",
		BootOrder:      []string{"hd"},
		MachineType:    "virt",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if !strings.Contains(readFileString(t, dpkgLog), "-x /opt/aavmf.deb /") {
		t.Fatalf("dpkg-deb log = %q", readFileString(t, dpkgLog))
	}
	if !strings.Contains(manager.definedXML, `<loader readonly="yes" secure="no" type="pflash">`+loader+`</loader>`) {
		t.Fatalf("domain XML missing AAVMF loader:\n%s", manager.definedXML)
	}
}

func TestConcreteLifecyclePrepareSkipsInstalledBootISO(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	workImage := filepath.Join(vmDir, "disk.qcow2")
	if err := os.WriteFile(workImage, []byte("installed-disk"), 0o644); err != nil {
		t.Fatalf("write work image: %v", err)
	}
	if err := vmstate.MarkInstalled(vmDir, fixedTime()); err != nil {
		t.Fatalf("mark installed: %v", err)
	}
	bootISO := filepath.Join(t.TempDir(), "installer.iso")
	if err := os.WriteFile(bootISO, []byte("iso"), 0o644); err != nil {
		t.Fatalf("write boot iso: %v", err)
	}
	commandLog := installFakeQEMUImg(t)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

	err := lifecycle.Prepare(context.Background(), config.VM{
		Distro:         "custom",
		VMName:         "vm1",
		Arch:           "x86_64",
		BootMode:       "legacy",
		ImageFormat:    "qcow2",
		CPUModel:       "qemu64",
		MemoryMB:       1024,
		CPUs:           1,
		DiskSize:       "8G",
		BootFrom:       bootISO,
		BlankWorkDisk:  true,
		BootOrder:      []string{"cdrom", "hd"},
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
		Persist:        true,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if strings.Contains(manager.definedXML, bootISO) {
		t.Fatalf("domain XML should not contain boot ISO %q:\n%s", bootISO, manager.definedXML)
	}
	if strings.Contains(readOptionalFile(t, commandLog), "create") {
		t.Fatalf("qemu-img should not recreate installed disk:\n%s", readOptionalFile(t, commandLog))
	}
	if got := readFileString(t, workImage); got != "installed-disk" {
		t.Fatalf("work image = %q", got)
	}
}

func TestConcreteLifecyclePrepareForceISOBootsInstalledDiskFromISO(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	workImage := filepath.Join(vmDir, "disk.qcow2")
	if err := os.WriteFile(workImage, []byte("installed-disk"), 0o644); err != nil {
		t.Fatalf("write work image: %v", err)
	}
	if err := vmstate.MarkInstalled(vmDir, fixedTime()); err != nil {
		t.Fatalf("mark installed: %v", err)
	}
	bootISO := filepath.Join(t.TempDir(), "installer.iso")
	if err := os.WriteFile(bootISO, []byte("iso"), 0o644); err != nil {
		t.Fatalf("write boot iso: %v", err)
	}
	installFakeQEMUImg(t)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

	err := lifecycle.Prepare(context.Background(), config.VM{
		Distro:         "custom",
		VMName:         "vm1",
		Arch:           "x86_64",
		BootMode:       "legacy",
		ImageFormat:    "qcow2",
		CPUModel:       "qemu64",
		MemoryMB:       1024,
		CPUs:           1,
		DiskSize:       "8G",
		BootFrom:       bootISO,
		BlankWorkDisk:  true,
		BootOrder:      []string{"cdrom", "hd"},
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
		Persist:        true,
		ForceISO:       true,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if !strings.Contains(manager.definedXML, `<source file="`+bootISO+`"/>`) {
		t.Fatalf("domain XML missing boot ISO %q:\n%s", bootISO, manager.definedXML)
	}
	if got := readFileString(t, workImage); got != "installed-disk" {
		t.Fatalf("work image = %q", got)
	}
}

func TestConcreteLifecyclePrepareCreatesExtraDisks(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, "ubuntu.qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	commandLog := installFakeQEMUImg(t)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

	err := lifecycle.Prepare(context.Background(), config.VM{
		Distro:          "ubuntu",
		VMName:          "vm1",
		Arch:            "x86_64",
		BootMode:        "legacy",
		ImageFormat:     "qcow2",
		CPUModel:        "qemu64",
		MemoryMB:        1024,
		CPUs:            1,
		DiskSize:        "10G",
		BootOrder:       []string{"hd"},
		MachineType:     "q35",
		DiskController:  "virtio",
		DiskCache:       "none",
		DiskIO:          "native",
		NICs:            []network.Config{{Mode: "user", Model: "virtio"}},
		ExtraDisks:      []config.Disk{{Index: 2, Size: "5G"}, {Index: 4, Size: "20G"}},
		DiskPreallocate: true,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	log := readFileString(t, commandLog)
	for _, want := range []string{
		"create -f qcow2 -o preallocation=falloc " + filepath.Join(layout.VMImagesDir, "vm1", "disk2.qcow2") + " 5G",
		"create -f qcow2 -o preallocation=falloc " + filepath.Join(layout.VMImagesDir, "vm1", "disk4.qcow2") + " 20G",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("qemu-img commands missing %q:\n%s", want, log)
		}
	}
}

func TestConcreteLifecycleCleanupRemovesNonPersistentVMDir(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "disk.qcow2"), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle.Domain = &fakeLibvirtDomain{name: "vm1"}
	lifecycle.TPM = nil
	lifecycle.NoVNC = nil

	if err := lifecycle.Cleanup(context.Background(), config.VM{VMName: "vm1", Persist: false}); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Fatalf("vm dir still exists, stat err=%v", err)
	}
}

func TestConcreteLifecycleCleanupKeepsPersistentVMDir(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle.Domain = &fakeLibvirtDomain{name: "vm1"}
	lifecycle.TPM = nil
	lifecycle.NoVNC = nil

	if err := lifecycle.Cleanup(context.Background(), config.VM{VMName: "vm1", Persist: true}); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if _, err := os.Stat(vmDir); err != nil {
		t.Fatalf("vm dir stat err=%v", err)
	}
}

func TestConcreteLifecycleCleanupStaleRemovesNonPersistentVMDir(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	manager := &fakeLibvirtManager{}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager

	if err := lifecycle.CleanupStale(context.Background(), config.VM{VMName: "vm1", Persist: false}); err != nil {
		t.Fatalf("CleanupStale returned error: %v", err)
	}
	if manager.reconcileCalls != 1 {
		t.Fatalf("reconcileCalls = %d", manager.reconcileCalls)
	}
	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Fatalf("vm dir still exists, stat err=%v", err)
	}
}

func TestConcreteLifecyclePrepareRemovesLeftoverNonPersistentVMDir(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	leftover := filepath.Join(vmDir, "leftover")
	if err := os.WriteFile(leftover, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write leftover: %v", err)
	}
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, "ubuntu.qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

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
		Persist:        false,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if manager.reconcileCalls != 1 {
		t.Fatalf("reconcileCalls = %d", manager.reconcileCalls)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("leftover stat err=%v", err)
	}
}

func TestConcreteLifecycleStartVMFallsBackFromPasstBackend(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, "ubuntu.qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	manager := &fakeLibvirtManager{
		domain:    &fakeLibvirtDomain{name: "vm1"},
		startErrs: []error{errors.New("failed to create passt backend"), nil},
	}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }
	cfg := config.VM{
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
	}

	if err := lifecycle.Prepare(context.Background(), cfg); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if !strings.Contains(manager.definedXML, `<backend type="passt"/>`) {
		t.Fatalf("initial XML missing passt backend:\n%s", manager.definedXML)
	}
	if err := lifecycle.StartVM(context.Background(), cfg); err != nil {
		t.Fatalf("StartVM returned error: %v", err)
	}
	if manager.startCalls != 2 {
		t.Fatalf("startCalls = %d", manager.startCalls)
	}
	if strings.Contains(manager.definedXML, `<backend type="passt"/>`) {
		t.Fatalf("fallback XML still contains passt backend:\n%s", manager.definedXML)
	}
}

func TestConcreteLifecycleWaitForGuestReadyWaitsForCloudInit(t *testing.T) {
	client := &fakeGuestExecClient{
		responses: []guestExecResponse{
			{raw: json.RawMessage(`{}`)},
			{raw: json.RawMessage(`{"pid":17}`)},
			{raw: json.RawMessage(`{"exited":true,"exitcode":0}`)},
		},
	}
	lifecycle := NewConcreteLifecycle(testLayout(t))
	lifecycle.Domain = &fakeLibvirtDomain{name: "vm1"}
	lifecycle.GuestClient = client
	lifecycle.Sleep = func(context.Context, time.Duration) error { return nil }

	if err := lifecycle.WaitForGuestReady(context.Background(), config.VM{CloudInitEnabled: true}); err != nil {
		t.Fatalf("WaitForGuestReady returned error: %v", err)
	}
	if len(client.commands) != 3 {
		t.Fatalf("commands = %#v", client.commands)
	}
	if client.commands[0].Execute != "guest-ping" {
		t.Fatalf("first command = %#v", client.commands[0])
	}
	args := client.commands[1].Arguments.(map[string]any)
	if client.commands[1].Execute != "guest-exec" || args["path"] != "/bin/sh" {
		t.Fatalf("cloud-init exec command = %#v", client.commands[1])
	}
	if got := strings.Join(args["arg"].([]string), " "); got != "-c cloud-init status --wait" {
		t.Fatalf("cloud-init args = %q", got)
	}
	if client.commands[2].Execute != "guest-exec-status" {
		t.Fatalf("status command = %#v", client.commands[2])
	}
}

func TestConcreteLifecyclePrepareKeepsPersistentExtraDisk(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "disk.qcow2"), []byte("existing-main"), 0o644); err != nil {
		t.Fatalf("write work image: %v", err)
	}
	extraDisk := filepath.Join(vmDir, "disk2.qcow2")
	if err := os.WriteFile(extraDisk, []byte("existing-extra"), 0o644); err != nil {
		t.Fatalf("write extra disk: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }

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
		ExtraDisks:     []config.Disk{{Index: 2, Size: "5G"}},
		Persist:        true,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got := readFileString(t, extraDisk); got != "existing-extra" {
		t.Fatalf("extra disk = %q", got)
	}
}

func TestEmulatorPackageMapsSupportedArchitectures(t *testing.T) {
	tests := []struct {
		arch       string
		wantBinary string
		wantDeb    string
	}{
		{"x86_64", "/usr/bin/qemu-system-x86_64", "/opt/qemu-x86.deb"},
		{"aarch64", "/usr/bin/qemu-system-aarch64", "/opt/qemu-arm.deb"},
		{"ppc64", "/usr/bin/qemu-system-ppc64", "/opt/qemu-ppc.deb"},
		{"s390x", "/usr/bin/qemu-system-s390x", "/opt/qemu-s390x.deb"},
		{"riscv64", "/usr/bin/qemu-system-riscv64", "/opt/qemu-riscv.deb"},
	}
	for _, tt := range tests {
		binary, deb, ok := emulatorPackage(tt.arch)
		if !ok {
			t.Fatalf("emulatorPackage(%q) ok = false", tt.arch)
		}
		if binary != tt.wantBinary || deb != tt.wantDeb {
			t.Fatalf("emulatorPackage(%q) = %q, %q", tt.arch, binary, deb)
		}
	}
}

func TestEmulatorPackageIgnoresUnknownArchitecture(t *testing.T) {
	binary, deb, ok := emulatorPackage("mips64")
	if ok || binary != "" || deb != "" {
		t.Fatalf("emulatorPackage returned %q, %q, %v", binary, deb, ok)
	}
}

type fakeLifecycle struct {
	calls              []string
	startServicesErr   error
	prepareErr         error
	cleanupContextErrs []error
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

type guestExecResponse struct {
	raw json.RawMessage
	err error
}

type fakeGuestExecClient struct {
	responses []guestExecResponse
	commands  []guestexec.Command
}

func (c *fakeGuestExecClient) ListRunningDomains(context.Context) ([]string, error) {
	return []string{"vm1"}, nil
}

func (c *fakeGuestExecClient) Execute(_ context.Context, _ string, command guestexec.Command) (json.RawMessage, error) {
	c.commands = append(c.commands, command)
	if len(c.responses) == 0 {
		return nil, errors.New("unexpected guest command")
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return resp.raw, resp.err
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
	reconcileCalls   int
	startCalls       int
	startErrs        []error
}

func (m *fakeLibvirtManager) ReconcileStaleDomain(string) error {
	m.reconcileCalls++
	return nil
}

func (m *fakeLibvirtManager) EnsureDefined(name string, xml string) (libvirtmgr.Domain, error) {
	m.definedName = name
	m.definedXML = xml
	return m.domain, nil
}

func (m *fakeLibvirtManager) Start(libvirtmgr.Domain) error {
	m.startCalls++
	if len(m.startErrs) == 0 {
		return nil
	}
	err := m.startErrs[0]
	m.startErrs = m.startErrs[1:]
	return err
}
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
func (d *fakeLibvirtDomain) XML() (string, error)          { return managedDomainXML(d.name), nil }
func (d *fakeLibvirtDomain) IsActive() (bool, error)       { return d.active, nil }
func (d *fakeLibvirtDomain) Create() error                 { d.active = true; return nil }
func (d *fakeLibvirtDomain) Shutdown() error               { d.active = false; return nil }
func (d *fakeLibvirtDomain) Destroy() error                { d.active = false; return nil }
func (d *fakeLibvirtDomain) Undefine() error               { return nil }
func (d *fakeLibvirtDomain) UndefineNVRAM() error          { return nil }
func (d *fakeLibvirtDomain) unusedTpmReference(tpm.Result) {}

func (l *fakeLifecycle) StartServices(context.Context, config.VM) error {
	l.calls = append(l.calls, "start-services")
	return l.startServicesErr
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
func (l *fakeLifecycle) Cleanup(ctx context.Context, _ config.VM) error {
	l.calls = append(l.calls, "cleanup")
	l.cleanupContextErrs = append(l.cleanupContextErrs, ctx.Err())
	return nil
}
func (l *fakeLifecycle) CleanupStale(ctx context.Context, _ config.VM) error {
	l.calls = append(l.calls, "cleanup-stale")
	l.cleanupContextErrs = append(l.cleanupContextErrs, ctx.Err())
	return nil
}
func (l *fakeLifecycle) Close(ctx context.Context, _ config.VM) error {
	l.calls = append(l.calls, "close")
	l.cleanupContextErrs = append(l.cleanupContextErrs, ctx.Err())
	return nil
}
func (l *fakeLifecycle) StopServices(ctx context.Context, _ config.VM) error {
	l.calls = append(l.calls, "stop-services")
	l.cleanupContextErrs = append(l.cleanupContextErrs, ctx.Err())
	return nil
}

func writeDistroConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "supported.json")
	content := `{
  "meta": {"api_version": "v1", "count": 2},
  "images": [
    {
      "id": "ubuntu-24.04-server",
      "name": "Ubuntu 24.04 LTS",
      "image_type": "cloud-image",
      "category": "linux",
      "distro": "ubuntu",
      "version": "24.04",
      "edition": "Server",
      "arch": "amd64",
      "release_type": "stable",
      "url": "https://example.com/ubuntu.qcow2",
      "eol": {"standard": "2029-05-31", "is_rolling": false},
      "status": "supported"
    },
    {
      "id": "fedora-42-arm64",
      "name": "Fedora 42",
      "image_type": "iso",
      "category": "linux",
      "distro": "fedora",
      "version": "42",
      "edition": "Server",
      "arch": "arm64",
      "release_type": "stable",
      "url": "https://example.com/fedora.qcow2",
      "eol": {"standard": "2026-05-13", "is_rolling": false},
      "status": "supported"
    }
  ]
}`
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

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func fixedTime() time.Time {
	return time.Date(2026, 5, 28, 13, 0, 0, 0, time.UTC)
}

func installFakeQEMUImg(t *testing.T) string {
	t.Helper()
	return installFakeCommand(t, "qemu-img", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\nexit 0\n"
	})
}

func installFakeQEMUImgWithInfo(t *testing.T, virtualSize int64) string {
	t.Helper()
	return installFakeCommand(t, "qemu-img", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
			"if [ \"$1\" = info ]; then\n" +
			"  printf '{\"format\":\"qcow2\",\"virtual-size\":" + fmt.Sprint(virtualSize) + "}\\n'\n" +
			"fi\n" +
			"exit 0\n"
	})
}

func installFakeCommand(t *testing.T, name string, content func(logPath string) string) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, name+".log")
	script := filepath.Join(dir, name)
	if err := os.WriteFile(script, []byte(content(logPath)), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func managedDomainXML(name string) string {
	return `<domain><name>` + name + `</name><metadata><dvr:managed xmlns:dvr="https://github.com/munenick/docker-qemu/v2">true</dvr:managed><dvr:vm-name xmlns:dvr="https://github.com/munenick/docker-qemu/v2">` + name + `</dvr:vm-name></metadata></domain>`
}
