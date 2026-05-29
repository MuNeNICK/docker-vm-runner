package runner

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/download"
	"github.com/munenick/docker-vm-runner/internal/guestexec"
	"github.com/munenick/docker-vm-runner/internal/hostinfo"
	"github.com/munenick/docker-vm-runner/internal/libvirtmgr"
	"github.com/munenick/docker-vm-runner/internal/network"
	"github.com/munenick/docker-vm-runner/internal/paths"
	"github.com/munenick/docker-vm-runner/internal/redfish"
	"github.com/munenick/docker-vm-runner/internal/services"
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
	if !strings.Contains(stderr.String(), "arm64") {
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
	PrintConfig(&out, config.VM{
		Password:        "secret1",
		RedfishPassword: "secret2",
		VMName:          "vm1",
		CPUModel:        "host",
		IPXEEnabled:     true,
		IPXEROMPath:     "/ipxe.rom",
		RedfishSystemID: "vm1",
		NICs:            []network.Config{{Mode: "user", Model: "virtio"}},
		Filesystems:     []config.FilesystemShare{{Source: "/host/share", Target: "share", Driver: "virtiofs"}},
	})

	text := out.String()
	if !strings.Contains(text, "password: ********") || !strings.Contains(text, "redfish_password: ********") {
		t.Fatalf("output = %q", text)
	}
	if strings.Contains(text, "secret1") || strings.Contains(text, "secret2") {
		t.Fatalf("sensitive values leaked: %q", text)
	}
	for _, want := range []string{"vm_name:", "cpu_model:", "ipxe_enabled:", "ipxe_rom_path:", "redfish_system_id:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	for _, want := range []string{
		"nics:\n    [0]:",
		"      mode: user",
		"      model: virtio",
		"filesystems:\n    [0]:",
		"      source: /host/share",
		"      target: share",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing nested config %q in:\n%s", want, text)
		}
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
		"SSH      ssh -p 2222 user@localhost",
		"Console  https://localhost:6080/vnc.html?autoconnect=1&resize=scale",
		"Redfish  https://localhost:8443/",
		"Ports    8080->80",
		"Publish  ",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %q in:\n%s", needle, text)
		}
	}
}

func TestPrintAccessUsesReadableBlock(t *testing.T) {
	var out bytes.Buffer
	PrintAccess(&out, config.VM{
		LoginUser:        "user",
		Password:         "password",
		CloudInitEnabled: true,
		SSHPort:          2222,
		NICs:             []network.Config{{Mode: "user", Model: "virtio"}},
	})
	text := out.String()
	for _, needle := range []string{"== Access ==", "SSH      ssh -p 2222 user@localhost"} {
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
		ExtraDisks:     []config.Disk{{Index: 2, Size: "10G"}},
		BlockDevices:   []config.BlockDevice{{Path: "/dev/vdb", Index: 1}},
	})
	text := strings.Join(lines, "\n")
	for _, needle := range []string{"Compute  2 vCPU / 4096 MiB RAM", "Features TPM", "Extra    disk2=10G", "Devices  /dev/vdb"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("summary missing %q:\n%s", needle, text)
		}
	}
}

func TestProgressLineIncludesBarSpeedAndETA(t *testing.T) {
	line := terminalProgressLine(download.Progress{
		Written: 5 * 1024 * 1024,
		Total:   10 * 1024 * 1024,
		Elapsed: time.Second,
	}, false)
	for _, needle := range []string{"███████████████░░░░░░░░░░░░░░░", "50.0%", "5.0 MiB / 10.0 MiB", "5.0 MiB/s", "ETA 00:01"} {
		if !strings.Contains(line, needle) {
			t.Fatalf("missing %q in %q", needle, line)
		}
	}
}

func TestOutputRendersTerminalSection(t *testing.T) {
	var out bytes.Buffer
	Output{Stdout: &out, Stderr: io.Discard, Mode: OutputTerminal}.Section("Access", []string{"SSH      ssh -p 2222 ubuntu@localhost"})

	text := out.String()
	for _, needle := range []string{"┌─ Access", "│ SSH      ssh -p 2222 ubuntu@localhost", "└"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %q in:\n%s", needle, text)
		}
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %#v", lines)
	}
	width := len([]rune(lines[0]))
	for _, line := range lines[1:] {
		if len([]rune(line)) != width {
			t.Fatalf("terminal section has uneven line widths:\n%s", text)
		}
	}
	for _, line := range lines {
		runes := []rune(line)
		if runes[len(runes)-1] != '┐' && runes[len(runes)-1] != '┘' && runes[len(runes)-1] != '│' {
			t.Fatalf("line missing right border: %q", line)
		}
	}
}

func TestOutputRendersLogDownloadProgress(t *testing.T) {
	var stderr bytes.Buffer
	output := Output{Stdout: io.Discard, Stderr: &stderr, Mode: OutputLog}
	output.DownloadProgress(download.Progress{
		Label:   "Downloading base image",
		Written: 128 * 1024 * 1024,
		Total:   512 * 1024 * 1024,
		Elapsed: 4 * time.Second,
	})

	text := stderr.String()
	for _, needle := range []string{"[INFO] Downloading base image:", "128.0 MiB / 512.0 MiB", "25.0%", "32.0 MiB/s", "ETA 00:12"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %q in:\n%s", needle, text)
		}
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
	want := "start-services,connect,prepare,start-vm,wait-guest-ready,wait-stopped,cleanup,close,stop-services"
	if got := strings.Join(lifecycle.calls, ","); got != want {
		t.Fatalf("calls = %s want %s", got, want)
	}
}

func TestRunLifecycleTreatsGuestReadyFailureAsWarningInNoConsole(t *testing.T) {
	lifecycle := &fakeLifecycle{waitGuestReadyErr: errors.New("agent not ready")}
	var stderr bytes.Buffer
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Stderr = &stderr
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server", "PERSIST": "1"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{NoConsole: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := "start-services,connect,prepare,start-vm,wait-guest-ready,wait-stopped,cleanup,close,stop-services"
	if got := strings.Join(lifecycle.calls, ","); got != want {
		t.Fatalf("calls = %s want %s", got, want)
	}
	if !strings.Contains(stderr.String(), "Guest readiness check did not complete") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunLifecycleMarksISOInstalledAfterNoConsoleStop(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "fedora-42-arm64", "PERSIST": "1"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{NoConsole: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := "start-services,connect,prepare,start-vm,wait-stopped,mark-installed,cleanup,close,stop-services"
	if got := strings.Join(lifecycle.calls, ","); got != want {
		t.Fatalf("calls = %s want %s", got, want)
	}
}

func TestRunLifecycleDoesNotMarkISOInstalledOnConsoleDetach(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "fedora-42-arm64", "PERSIST": "1"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if containsCall(lifecycle.calls, "mark-installed") {
		t.Fatalf("calls include mark-installed: %s", strings.Join(lifecycle.calls, ","))
	}
}

func TestRunLifecycleMarksISOInstalledAfterConsoleStop(t *testing.T) {
	lifecycle := &fakeLifecycle{domainStopped: true}
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Resolver = &config.Resolver{DistroConfigPath: writeDistroConfig(t)}
	r.Env = config.MapEnv{"DISTRO": "fedora-42-arm64", "PERSIST": "1"}
	r.Lifecycle = lifecycle

	if err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := "start-services,connect,prepare,start-vm,attach-console,domain-stopped,mark-installed,cleanup,close,stop-services"
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
	if containsCall(lifecycle.calls, "mark-installed") {
		t.Fatalf("cloud image should not be marked installed: %s", strings.Join(lifecycle.calls, ","))
	}
}

func TestRunAutoDetectsDataMountForPersistence(t *testing.T) {
	var stdout bytes.Buffer
	r := New()
	r.Stdout = &stdout
	r.Stderr = &bytes.Buffer{}
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server"}
	r.IsMount = func(path string) bool { return path == "/data" }

	if err := r.Run(context.Background(), Options{ShowConfig: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "persist: true") {
		t.Fatalf("expected /data mount to enable persistence, output:\n%s", output)
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

func TestNewConcreteLifecyclePassesRuntimeInfoToSupervisor(t *testing.T) {
	r := New()
	r.DetectHostInfo = func(string) hostinfo.Info {
		return hostinfo.Info{RuntimeEngine: "docker", RuntimeRootless: true, RuntimePriv: false}
	}

	lifecycle := r.newConcreteLifecycle()
	supervisor, ok := lifecycle.Service.(*services.Supervisor)
	if !ok {
		t.Fatalf("Service = %T", lifecycle.Service)
	}
	if !supervisor.Options.Runtime.Rootless || supervisor.Options.Runtime.Privileged {
		t.Fatalf("Runtime = %#v", supervisor.Options.Runtime)
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

func TestRunShowXMLDoesNotTreatDiskBootFromAsCDROM(t *testing.T) {
	disk := filepath.Join(t.TempDir(), "disk.qcow2")
	if err := os.WriteFile(disk, []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	var stdout bytes.Buffer
	r := New()
	r.Stdout = &stdout
	r.Stderr = &bytes.Buffer{}
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server", "BOOT_FROM": disk}

	if err := r.Run(context.Background(), Options{ShowXML: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	output := stdout.String()
	if strings.Contains(output, `<device="cdrom"`) || strings.Contains(output, `<source file="`+disk+`"`) {
		t.Fatalf("show XML should not attach disk BOOT_FROM as CD-ROM:\n%s", output)
	}
}

func TestRunShowXMLAttachesISOAsCDROM(t *testing.T) {
	iso := filepath.Join(t.TempDir(), "installer.iso")
	if err := os.WriteFile(iso, []byte("iso"), 0o644); err != nil {
		t.Fatalf("write iso: %v", err)
	}
	var stdout bytes.Buffer
	r := New()
	r.Stdout = &stdout
	r.Stderr = &bytes.Buffer{}
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server", "BOOT_FROM": iso}

	if err := r.Run(context.Background(), Options{ShowXML: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `<source file="`+iso+`"`) || !strings.Contains(output, `device="cdrom"`) {
		t.Fatalf("show XML should attach ISO BOOT_FROM as CD-ROM:\n%s", output)
	}
}

func TestRunShowXMLIncludesUEFIFirmware(t *testing.T) {
	dataDir := t.TempDir()
	firmwareDir := t.TempDir()
	loader := filepath.Join(firmwareDir, "OVMF_CODE.fd")
	varsTemplate := filepath.Join(firmwareDir, "OVMF_VARS.fd")
	originalProfile := config.SupportedArchitectures["x86_64"]
	profile := originalProfile
	profile.Firmware = map[string]config.FirmwareProfile{
		"uefi": {Loader: loader, VarsTemplate: varsTemplate},
	}
	config.SupportedArchitectures["x86_64"] = profile
	t.Cleanup(func() { config.SupportedArchitectures["x86_64"] = originalProfile })

	var stdout bytes.Buffer
	r := New()
	r.Stdout = &stdout
	r.Stderr = &bytes.Buffer{}
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server", "DATA_DIR": dataDir}

	if err := r.Run(context.Background(), Options{ShowXML: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	output := stdout.String()
	wantVars := filepath.Join(dataDir, "state", "firmware", "ubuntu-24.04-server-vars.fd")
	for _, want := range []string{
		`<loader readonly="yes" secure="no" type="pflash">` + loader + `</loader>`,
		`<nvram>` + wantVars + `</nvram>`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("show XML output missing %q:\n%s", want, output)
		}
	}
}

func TestRunDryRunValidatesMissingBootFrom(t *testing.T) {
	var stdout bytes.Buffer
	r := New()
	r.Stdout = &stdout
	r.Stderr = &bytes.Buffer{}
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server", "BOOT_FROM": filepath.Join(t.TempDir(), "missing.iso")}

	err := r.Run(context.Background(), Options{DryRun: true})
	if err == nil {
		t.Fatal("expected missing BOOT_FROM error")
	}
	if !strings.Contains(err.Error(), "BOOT_FROM path not found") {
		t.Fatalf("unexpected error: %v", err)
	}
	output := stdout.String()
	for _, needle := range []string{"vm_name:", "== Host ==", "== Dry Run ==", "BOOT_FROM", "NOT FOUND", "Result      no VM started", "== Access =="} {
		if !strings.Contains(output, needle) {
			t.Fatalf("dry-run output missing %q:\n%s", needle, output)
		}
	}
}

func TestRunDryRunPrintsHostDiagnostics(t *testing.T) {
	var stdout bytes.Buffer
	r := New()
	r.Stdout = &stdout
	r.Stderr = &bytes.Buffer{}
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server"}

	if err := r.Run(context.Background(), Options{DryRun: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Host") || !strings.Contains(stdout.String(), "KVM") || !strings.Contains(stdout.String(), "Boot order") || !strings.Contains(stdout.String(), "Cloud-init") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunPrintsConfigWarnings(t *testing.T) {
	userData := filepath.Join(t.TempDir(), "user-data.txt")
	if err := os.WriteFile(userData, []byte("hostname: demo\n"), 0o644); err != nil {
		t.Fatalf("write user-data: %v", err)
	}
	var stderr bytes.Buffer
	r := New()
	r.Stdout = &bytes.Buffer{}
	r.Stderr = &stderr
	r.DistroConfigPath = writeDistroConfig(t)
	r.Env = config.MapEnv{"DISTRO": "ubuntu-24.04-server", "CLOUD_INIT_USER_DATA": userData}

	if err := r.Run(context.Background(), Options{ShowConfig: true}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "CLOUD_INIT_USER_DATA") {
		t.Fatalf("stderr = %q", stderr.String())
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
	want := "start-cleanup-services,connect,cleanup-stale,close,stop-services"
	if got := strings.Join(lifecycle.calls, ","); got != want {
		t.Fatalf("calls = %s want %s", got, want)
	}
}

func TestConcreteLifecycleStartsServices(t *testing.T) {
	service := &fakeServiceSupervisor{}
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	redfishProcess := &fakeRedfishProcess{}
	redfishManager := &fakeRedfishManager{process: redfishProcess}
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
		RedfishSystemID: "vm1",
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
	if redfishManager.request.SystemID != "vm1" {
		t.Fatalf("redfish system id = %q", redfishManager.request.SystemID)
	}

	if err := lifecycle.StopServices(context.Background(), config.VM{}); err != nil {
		t.Fatalf("StopServices returned error: %v", err)
	}
	if redfishProcess.stopCalls != 1 {
		t.Fatalf("redfish stop calls = %d", redfishProcess.stopCalls)
	}
}

func TestConcreteLifecycleStartsRedfishWhenStoragePoolEnsureFails(t *testing.T) {
	service := &fakeServiceSupervisor{}
	manager := &fakeLibvirtManager{
		domain:         &fakeLibvirtDomain{name: "vm1"},
		storagePoolErr: errors.New("pool unavailable"),
	}
	redfishManager := &fakeRedfishManager{}
	var status bytes.Buffer
	lifecycle := NewConcreteLifecycle(testLayout(t))
	lifecycle.Service = service
	lifecycle.Manager = manager
	lifecycle.Redfish = redfishManager
	lifecycle.NoVNC = nil
	lifecycle.Status = &status
	lifecycle.RedfishPool = libvirtmgr.StoragePoolRequest{Name: "default", TargetPath: filepath.Join(t.TempDir(), "pool")}

	err := lifecycle.StartServices(context.Background(), config.VM{
		RedfishEnabled:  true,
		RedfishUser:     "admin",
		RedfishPassword: "secret",
		RedfishPort:     8443,
	})
	if err != nil {
		t.Fatalf("StartServices returned error: %v", err)
	}
	if !redfishManager.started {
		t.Fatal("Redfish was not started")
	}
	if !strings.Contains(status.String(), "Redfish storage pool") {
		t.Fatalf("status = %q", status.String())
	}
}

func TestConcreteLifecyclePrepareDefinesDomain(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	commandLog := installFakeQEMUImgWithInfo(t, 1*1024*1024*1024)
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
	var status bytes.Buffer
	lifecycle.Status = &status

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
	if !strings.Contains(status.String(), "software emulation mode") {
		t.Fatalf("status missing TCG warning: %q", status.String())
	}
}

func TestConcreteLifecyclePrepareRejectsUnsafeVMName(t *testing.T) {
	lifecycle := NewConcreteLifecycle(testLayout(t))
	lifecycle.Manager = &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle.KVMAvailable = func() bool { return true }

	err := lifecycle.Prepare(context.Background(), config.VM{VMName: "../outside"})
	if err == nil {
		t.Fatal("expected unsafe VM name error")
	}
	if !strings.Contains(err.Error(), "invalid VM name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConcreteLifecyclePreparePassesIPXEROMPath(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2"), []byte("base"), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2"), []byte("base"), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2"), []byte("base"), 0o644); err != nil {
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

func TestConcreteLifecycleResolveBaseImageRejectsInvalidCachedImage(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2"), []byte("not-an-image"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	installFakeCommand(t, "qemu-img", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
			"if [ \"$1\" = info ]; then printf 'not-json\\n'; fi\n" +
			"exit 0\n"
	})
	lifecycle := NewConcreteLifecycle(layout)

	_, err := lifecycle.resolveBaseImage(context.Background(), config.VM{
		Distro:      "ubuntu",
		ImageFormat: "qcow2",
	})
	if err == nil {
		t.Fatal("expected cached image validation error")
	}
	if !strings.Contains(err.Error(), "parse qemu-img info") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConcreteLifecycleResolveBaseImageUsesSafeDistroCacheName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("base-image"))
	}))
	defer server.Close()
	layout := testLayout(t)
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	lifecycle := NewConcreteLifecycle(layout)

	got, err := lifecycle.resolveBaseImage(context.Background(), config.VM{
		Distro:          "../escape",
		ImageURL:        server.URL,
		ImageFormat:     "qcow2",
		DownloadRetries: 1,
	})
	if err != nil {
		t.Fatalf("resolveBaseImage returned error: %v", err)
	}
	if !pathWithin(layout.BaseImagesDir, got) {
		t.Fatalf("base image escaped base dir: %s", got)
	}
	if strings.Contains(got, "..") {
		t.Fatalf("base image path contains unsafe segment: %s", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(layout.BaseImagesDir), "escape.qcow2")); !os.IsNotExist(err) {
		t.Fatalf("unsafe escaped cache path exists or unexpected stat error: %v", err)
	}
}

func TestConcreteLifecycleResolveBaseImageUsesCompressionMetadata(t *testing.T) {
	layout := testLayout(t)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "cached-image")
	writeGzip(t, source, []byte("disk-image"))
	sibling := filepath.Join(sourceDir, "cached-image.qcow2")
	if err := os.WriteFile(sibling, []byte("do-not-overwrite"), 0o644); err != nil {
		t.Fatalf("write sibling: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	lifecycle := NewConcreteLifecycle(layout)

	got, err := lifecycle.resolveBaseImage(context.Background(), config.VM{
		Distro:                 "custom",
		VMName:                 "vm1",
		BootFrom:               source,
		ImageFormat:            "qcow2",
		SourceImageFormat:      "qcow2",
		SourceImageCompression: "gzip",
	})
	if err != nil {
		t.Fatalf("resolveBaseImage returned error: %v", err)
	}
	if got != filepath.Join(layout.VMImagesDir, "vm1", "boot.qcow2") {
		t.Fatalf("base image path = %s", got)
	}
	if content := readFileString(t, got); content != "disk-image" {
		t.Fatalf("base image content = %q", content)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("user-provided source should not be removed: %v", err)
	}
	if content := readFileString(t, sibling); content != "do-not-overwrite" {
		t.Fatalf("sibling file was overwritten: %q", content)
	}
	if entries, err := os.ReadDir(filepath.Join(layout.BaseImagesDir, "extract")); err == nil && len(entries) != 0 {
		t.Fatalf("extract cache not cleaned: %v", entries)
	}
}

func TestConcreteLifecycleResolveBaseImageDoesNotOverwriteDistroCacheForBootFromDisk(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	baseImage := filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2")
	if err := os.WriteFile(baseImage, []byte("catalog-base"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	source := filepath.Join(t.TempDir(), "custom.qcow2")
	if err := os.WriteFile(source, []byte("custom-disk"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	lifecycle := NewConcreteLifecycle(layout)

	got, err := lifecycle.resolveBaseImage(context.Background(), config.VM{
		Distro:      "ubuntu",
		VMName:      "vm1",
		BootFrom:    source,
		ImageFormat: "qcow2",
	})
	if err != nil {
		t.Fatalf("resolveBaseImage returned error: %v", err)
	}
	if got != source {
		t.Fatalf("boot source base = %s", got)
	}
	vmBootImage := filepath.Join(layout.VMImagesDir, "vm1", "boot.qcow2")
	if _, err := os.Stat(vmBootImage); !os.IsNotExist(err) {
		t.Fatalf("local boot source should not be duplicated at %s: %v", vmBootImage, err)
	}
	if content := readFileString(t, baseImage); content != "catalog-base" {
		t.Fatalf("distro cache was overwritten: %q", content)
	}
}

func TestConcreteLifecyclePrepareUsesOverlayForLocalBootDisk(t *testing.T) {
	layout := testLayout(t)
	source := filepath.Join(t.TempDir(), "custom.qcow2")
	if err := os.WriteFile(source, []byte("custom-disk"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	commandLog := installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
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
		DiskSize:       "20G",
		BootOrder:      []string{"hd"},
		BootFrom:       source,
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	workImage := filepath.Join(layout.VMImagesDir, "vm1", "disk.qcow2")
	log := readFileString(t, commandLog)
	want := "create -f qcow2 -F qcow2 -b " + source + " " + workImage
	if !strings.Contains(log, want) {
		t.Fatalf("qemu-img commands missing %q:\n%s", want, log)
	}
	if got := readOptionalFile(t, workImage); got == "custom-disk" {
		t.Fatalf("local boot disk was fully copied to %s", workImage)
	}
}

func TestConcreteLifecyclePrepareUsesOverlayForRemoteBootDiskCache(t *testing.T) {
	layout := testLayout(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("remote-disk"))
	}))
	defer server.Close()
	commandLog := installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
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
		DiskSize:       "20G",
		BootOrder:      []string{"hd"},
		BootFrom:       server.URL + "/boot.qcow2",
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	bootCache := filepath.Join(layout.BaseImagesDir, "boot", cacheName(server.URL+"/boot.qcow2"))
	workImage := filepath.Join(layout.VMImagesDir, "vm1", "disk.qcow2")
	log := readFileString(t, commandLog)
	want := "create -f qcow2 -F qcow2 -b " + bootCache + " " + workImage
	if !strings.Contains(log, want) {
		t.Fatalf("qemu-img commands missing %q:\n%s", want, log)
	}
	if content := readFileString(t, bootCache); content != "remote-disk" {
		t.Fatalf("boot cache content = %q", content)
	}
	if _, err := os.Stat(filepath.Join(layout.VMImagesDir, "vm1", "boot.qcow2")); !os.IsNotExist(err) {
		t.Fatalf("remote boot source should not be duplicated into VM boot image: %v", err)
	}
}

func TestConcreteLifecyclePrepareRejectsNonPersistentBootFromInsideVMDir(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	source := filepath.Join(vmDir, "source.qcow2")
	if err := os.WriteFile(source, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
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
		DiskSize:       "20G",
		BootOrder:      []string{"hd"},
		BootFrom:       source,
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
		Persist:        false,
	})
	if err == nil {
		t.Fatal("expected BOOT_FROM inside VM directory error")
	}
	if !strings.Contains(err.Error(), "inside VM directory") {
		t.Fatalf("unexpected error: %v", err)
	}
	if content := readFileString(t, source); content != "keep" {
		t.Fatalf("BOOT_FROM source was modified: %q", content)
	}
}

func TestConcreteLifecyclePrepareUsesOverlayForBootDiskUnderBaseDir(t *testing.T) {
	layout := testLayout(t)
	source := filepath.Join(layout.BaseImagesDir, "custom", "source.qcow2")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	if err := os.WriteFile(source, []byte("custom-base"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	commandLog := installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
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
		DiskSize:       "20G",
		BootOrder:      []string{"hd"},
		BootFrom:       source,
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	workImage := filepath.Join(layout.VMImagesDir, "vm1", "disk.qcow2")
	log := readFileString(t, commandLog)
	want := "create -f qcow2 -F qcow2 -b " + source + " " + workImage
	if !strings.Contains(log, want) {
		t.Fatalf("qemu-img commands missing %q:\n%s", want, log)
	}
	if got := readOptionalFile(t, workImage); got == "custom-base" {
		t.Fatalf("base-dir BOOT_FROM source was fully copied to %s", workImage)
	}
}

func TestConcreteLifecyclePostProcessCleansManagedIntermediates(t *testing.T) {
	layout := testLayout(t)
	source := filepath.Join(layout.BaseImagesDir, "downloads", "image.raw")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if err := os.WriteFile(source, []byte("raw"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	installFakeCommand(t, "qemu-img", func(logPath string) string {
		destination := filepath.Join(layout.BaseImagesDir, cacheName("custom")+".qcow2")
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
			"if [ \"$1\" = info ] && [ \"$3\" = " + shellQuote(destination) + " ]; then printf '{\"format\":\"qcow2\",\"virtual-size\":10737418240}\\n'; exit 0; fi\n" +
			"if [ \"$1\" = info ]; then printf '{\"format\":\"raw\",\"virtual-size\":10737418240}\\n'; fi\n" +
			"if [ \"$1\" = convert ]; then printf converted > \"$6\"; fi\n" +
			"exit 0\n"
	})
	var status bytes.Buffer
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Status = &status
	destination := filepath.Join(layout.BaseImagesDir, cacheName("custom")+".qcow2")

	got, err := lifecycle.postProcessImage(context.Background(), source, destination, imagePostProcessOptions{DesiredFormat: "qcow2", SourceFormat: "raw"})
	if err != nil {
		t.Fatalf("postProcessImage returned error: %v", err)
	}
	if got != destination {
		t.Fatalf("destination = %s", got)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("managed source should be removed, stat err = %v", err)
	}
	if content := readFileString(t, destination); content != "converted" {
		t.Fatalf("destination content = %q", content)
	}
	if !strings.Contains(status.String(), "Converting image") {
		t.Fatalf("status missing conversion log: %q", status.String())
	}
}

func TestConcreteLifecyclePostProcessRejectsInvalidConvertedImage(t *testing.T) {
	layout := testLayout(t)
	source := filepath.Join(layout.BaseImagesDir, "downloads", "image.raw")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if err := os.WriteFile(source, []byte("raw"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	installFakeCommand(t, "qemu-img", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
			"if [ \"$1\" = info ] && [ \"$3\" = " + shellQuote(source) + " ]; then printf '{\"format\":\"raw\",\"virtual-size\":10737418240}\\n'; exit 0; fi\n" +
			"if [ \"$1\" = info ]; then echo invalid >&2; exit 1; fi\n" +
			"if [ \"$1\" = convert ]; then printf broken > \"$6\"; fi\n" +
			"exit 0\n"
	})
	lifecycle := NewConcreteLifecycle(layout)
	destination := filepath.Join(layout.BaseImagesDir, cacheName("custom")+".qcow2")

	_, err := lifecycle.postProcessImage(context.Background(), source, destination, imagePostProcessOptions{DesiredFormat: "qcow2", SourceFormat: "raw"})
	if err == nil {
		t.Fatal("expected converted image validation error")
	}
	if !strings.Contains(err.Error(), "validate converted image") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("invalid converted destination remains, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(source); !os.IsNotExist(statErr) {
		t.Fatalf("managed source should be cleaned, stat err = %v", statErr)
	}
}

func TestConcreteLifecyclePostProcessRejectsInvalidManagedImage(t *testing.T) {
	layout := testLayout(t)
	source := filepath.Join(layout.BaseImagesDir, "downloads", "image.qcow2")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if err := os.WriteFile(source, []byte("html-error"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	installFakeCommand(t, "qemu-img", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
			"if [ \"$1\" = info ]; then echo invalid >&2; exit 1; fi\n" +
			"exit 0\n"
	})
	lifecycle := NewConcreteLifecycle(layout)
	destination := filepath.Join(layout.BaseImagesDir, cacheName("custom")+".qcow2")

	_, err := lifecycle.postProcessImage(context.Background(), source, destination, imagePostProcessOptions{DesiredFormat: "qcow2", SourceFormat: "qcow2"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "validate image") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("failed destination remains, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(source); !os.IsNotExist(statErr) {
		t.Fatalf("managed source should be cleaned, stat err = %v", statErr)
	}
}

func TestConcreteLifecyclePrepareRejectsBackingImageInfoFailure(t *testing.T) {
	layout := testLayout(t)
	baseImage := filepath.Join(t.TempDir(), "custom.qcow2")
	if err := os.WriteFile(baseImage, []byte("base"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	stateDir := t.TempDir()
	installFakeCommand(t, "qemu-img", func(logPath string) string {
		countPath := filepath.Join(stateDir, "info-count")
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
			"if [ \"$1\" = info ]; then\n" +
			"  count=0\n" +
			"  [ -f " + shellQuote(countPath) + " ] && count=$(cat " + shellQuote(countPath) + ")\n" +
			"  count=$((count + 1))\n" +
			"  printf '%s' \"$count\" > " + shellQuote(countPath) + "\n" +
			"  if [ \"$count\" -eq 1 ]; then printf '{\"format\":\"qcow2\",\"virtual-size\":10737418240}\\n'; exit 0; fi\n" +
			"  echo invalid >&2; exit 1\n" +
			"fi\n" +
			"exit 0\n"
	})
	lifecycle := NewConcreteLifecycle(layout)
	workImage := filepath.Join(layout.VMImagesDir, "vm1", "disk.qcow2")

	err := lifecycle.prepareDisk(context.Background(), config.VM{
		VMName:      "vm1",
		BootFrom:    baseImage,
		ImageFormat: "qcow2",
		DiskSize:    "10G",
	}, workImage)
	if err == nil {
		t.Fatal("expected backing validation error")
	}
	if !strings.Contains(err.Error(), "validate backing image") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(workImage); !os.IsNotExist(statErr) {
		t.Fatalf("work image should not be created, stat err = %v", statErr)
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

func TestConcreteLifecycleResolveBootSourceRechecksCachedChecksum(t *testing.T) {
	body := []byte("boot-iso")
	sum := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	ref := server.URL + "/installer.iso"
	layout := testLayout(t)
	destination := filepath.Join(layout.BaseImagesDir, "boot", cacheName(ref))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatalf("mkdir boot cache: %v", err)
	}
	if err := os.WriteFile(destination, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale cache: %v", err)
	}
	lifecycle := NewConcreteLifecycle(layout)

	got, err := lifecycle.resolveBootSource(context.Background(), config.VM{
		BootFrom:              ref,
		BootChecksumAlgorithm: "sha256",
		BootChecksumValue:     hex.EncodeToString(sum[:]),
		DownloadRetries:       1,
	})
	if err != nil {
		t.Fatalf("resolveBootSource returned error: %v", err)
	}
	if got != destination {
		t.Fatalf("boot source = %s", got)
	}
	if content := readFileString(t, destination); content != string(body) {
		t.Fatalf("cached content = %q", content)
	}
}

func TestConcreteLifecycleResolveBootSourceRedownloadsZeroByteCache(t *testing.T) {
	body := []byte("boot-iso")
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write(body)
	}))
	defer server.Close()
	ref := server.URL + "/installer.iso"
	layout := testLayout(t)
	destination := filepath.Join(layout.BaseImagesDir, "boot", cacheName(ref))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatalf("mkdir boot cache: %v", err)
	}
	if err := os.WriteFile(destination, nil, 0o644); err != nil {
		t.Fatalf("write zero cache: %v", err)
	}
	lifecycle := NewConcreteLifecycle(layout)

	got, err := lifecycle.resolveBootSource(context.Background(), config.VM{BootFrom: ref, DownloadRetries: 1})
	if err != nil {
		t.Fatalf("resolveBootSource returned error: %v", err)
	}
	if got != destination {
		t.Fatalf("boot source = %s", got)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
	if content := readFileString(t, destination); content != string(body) {
		t.Fatalf("cached content = %q", content)
	}
}

func TestConcreteLifecyclePrepareSeedISOPassesFilesystems(t *testing.T) {
	layout := testLayout(t)
	installFakeCommand(t, "genisoimage", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
			"cp \"$9\" \"$(dirname \"$2\")/vendor-data.captured\"\n" +
			"exit 0\n"
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
	vendorData := readFileString(t, filepath.Join(filepath.Dir(output), "vendor-data.captured"))
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

func TestConcreteLifecyclePrepareSeedISOWarnsOnEmptyUserData(t *testing.T) {
	layout := testLayout(t)
	installFakeCommand(t, "genisoimage", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\nexit 0\n"
	})
	userData := filepath.Join(t.TempDir(), "user-data")
	if err := os.WriteFile(userData, []byte(" \n"), 0o644); err != nil {
		t.Fatalf("write user-data: %v", err)
	}
	var status bytes.Buffer
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Status = &status
	output := filepath.Join(layout.VMImagesDir, "vm1", "seed.iso")

	err := lifecycle.prepareSeedISO(context.Background(), config.VM{
		VMName:                "vm1",
		LoginUser:             "user",
		LoginShell:            "/bin/bash",
		Password:              "password",
		CloudInitUserDataPath: userData,
	}, output)
	if err != nil {
		t.Fatalf("prepareSeedISO returned error: %v", err)
	}
	if !strings.Contains(status.String(), "CLOUD_INIT_USER_DATA is empty") {
		t.Fatalf("status = %q", status.String())
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

func TestConcreteLifecyclePrepareRejectsInvalidPersistentWorkImage(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	workImage := filepath.Join(vmDir, "disk.qcow2")
	if err := os.WriteFile(workImage, []byte("partial"), 0o644); err != nil {
		t.Fatalf("write work image: %v", err)
	}
	installFakeCommand(t, "qemu-img", func(logPath string) string {
		return "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
			"if [ \"$1\" = info ] && [ \"$3\" = " + shellQuote(workImage) + " ]; then printf 'not-json\\n'; exit 0; fi\n" +
			"if [ \"$1\" = info ]; then printf '{\"format\":\"qcow2\",\"virtual-size\":21474836480}\\n'; fi\n" +
			"exit 0\n"
	})
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
	if err == nil {
		t.Fatal("expected invalid persistent disk error")
	}
	if !strings.Contains(err.Error(), "validate persistent disk") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFileString(t, workImage); got != "partial" {
		t.Fatalf("work image = %q", got)
	}
}

func TestConcreteLifecyclePrepareDoesNotShrinkCopiedBaseImage(t *testing.T) {
	layout := testLayout(t)
	if err := os.MkdirAll(layout.BaseImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2"), []byte("base"), 0o644); err != nil {
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
	commandLog := installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	var status bytes.Buffer
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.TPM = nil
	lifecycle.EnsureEmulator = func(context.Context, string) error { return nil }
	lifecycle.Status = &status

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
	if !strings.Contains(status.String(), "Creating blank disk") {
		t.Fatalf("status missing disk creation log: %q", status.String())
	}
}

func TestConcreteLifecyclePrepareAttachesBootISOWithBlankWorkDisk(t *testing.T) {
	layout := testLayout(t)
	bootISO := filepath.Join(t.TempDir(), "installer.iso")
	if err := os.WriteFile(bootISO, []byte("iso"), 0o644); err != nil {
		t.Fatalf("write boot iso: %v", err)
	}
	commandLog := installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
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
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("custom")+".qcow2"), []byte("base"), 0o644); err != nil {
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

	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
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
	commandLog := installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
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
	installFakeQEMUImgWithInfo(t, 8*1024*1024*1024)
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
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	commandLog := installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
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

func TestConcreteLifecycleCleanupKeepsNonPersistentVMDirWhenLibvirtCleanupFails(t *testing.T) {
	layout := testLayout(t)
	vmDir := filepath.Join(layout.VMImagesDir, "vm1")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "disk.qcow2"), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}, cleanupErr: errors.New("destroy failed")}
	lifecycle.Domain = &fakeLibvirtDomain{name: "vm1"}
	lifecycle.TPM = nil
	lifecycle.NoVNC = nil

	err := lifecycle.Cleanup(context.Background(), config.VM{VMName: "vm1", Persist: false})
	if err == nil {
		t.Fatal("expected cleanup error")
	}
	if _, statErr := os.Stat(vmDir); statErr != nil {
		t.Fatalf("vm dir should remain after cleanup failure: %v", statErr)
	}
}

func TestConcreteLifecycleCleanupRejectsUnsafeVMName(t *testing.T) {
	layout := testLayout(t)
	outside := filepath.Join(filepath.Dir(layout.VMImagesDir), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	lifecycle := NewConcreteLifecycle(layout)

	err := lifecycle.Cleanup(context.Background(), config.VM{VMName: "../outside", Persist: false})
	if err == nil {
		t.Fatal("expected unsafe VM name error")
	}
	if !strings.Contains(err.Error(), "invalid VM name") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFileString(t, outside); got != "keep" {
		t.Fatalf("outside file was modified: %q", got)
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

func TestConcreteLifecycleCleanupPassesTPMFlag(t *testing.T) {
	layout := testLayout(t)
	manager := &fakeLibvirtManager{domain: &fakeLibvirtDomain{name: "vm1"}}
	lifecycle := NewConcreteLifecycle(layout)
	lifecycle.Manager = manager
	lifecycle.Domain = &fakeLibvirtDomain{name: "vm1"}
	lifecycle.TPM = nil
	lifecycle.NoVNC = nil

	if err := lifecycle.Cleanup(context.Background(), config.VM{VMName: "vm1", Persist: true, TPMEnabled: true}); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if !manager.lastCleanupOpts.HasTPM {
		t.Fatalf("HasTPM = false")
	}
}

func TestConcreteLifecycleWaitUntilStoppedTreatsMissingDomainAsStopped(t *testing.T) {
	lifecycle := NewConcreteLifecycle(testLayout(t))
	lifecycle.Domain = &fakeLibvirtDomain{name: "vm1", isActiveErr: libvirtmgr.ErrNotFound}

	if err := lifecycle.WaitUntilStopped(context.Background(), config.VM{}); err != nil {
		t.Fatalf("WaitUntilStopped returned error: %v", err)
	}
}

func TestConcreteLifecycleCleanupTreatsMissingDomainAsCleaned(t *testing.T) {
	lifecycle := NewConcreteLifecycle(testLayout(t))
	lifecycle.Manager = &fakeLibvirtManager{cleanupErr: libvirtmgr.ErrNotFound}
	lifecycle.Domain = &fakeLibvirtDomain{name: "vm1"}
	lifecycle.TPM = nil
	lifecycle.NoVNC = nil

	if err := lifecycle.Cleanup(context.Background(), config.VM{VMName: "vm1", Persist: true}); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
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
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2"), []byte("base"), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(layout.BaseImagesDir, cacheName("ubuntu")+".qcow2"), []byte("base"), 0o644); err != nil {
		t.Fatalf("write base image: %v", err)
	}
	installFakeQEMUImgWithInfo(t, 10*1024*1024*1024)
	manager := &fakeLibvirtManager{
		domain:    &fakeLibvirtDomain{name: "vm1"},
		startErrs: []error{errors.New("failed to create network backend"), nil},
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
	if manager.lastCleanupOpts.HasTPM {
		t.Fatalf("HasTPM = true")
	}
	if strings.Contains(manager.definedXML, `<backend type="passt"/>`) {
		t.Fatalf("fallback XML still contains passt backend:\n%s", manager.definedXML)
	}
}

func TestConcreteLifecycleStartVMFallbackPassesTPMFlag(t *testing.T) {
	lifecycle := NewConcreteLifecycle(testLayout(t))
	manager := &fakeLibvirtManager{
		domain:    &fakeLibvirtDomain{name: "vm1"},
		startErrs: []error{errors.New("failed to create network backend"), nil},
	}
	lifecycle.Manager = manager
	lifecycle.Domain = &fakeLibvirtDomain{name: "vm1"}
	lifecycle.vmDir = filepath.Join(lifecycle.Layout.VMImagesDir, "vm1")
	lifecycle.currentConfig = config.VM{
		VMName:         "vm1",
		Arch:           "x86_64",
		BootMode:       "legacy",
		ImageFormat:    "qcow2",
		CPUModel:       "qemu64",
		MemoryMB:       1024,
		CPUs:           1,
		BootOrder:      []string{"hd"},
		MachineType:    "q35",
		DiskController: "virtio",
		DiskCache:      "none",
		DiskIO:         "native",
		NICs:           []network.Config{{Mode: "user", Model: "virtio"}},
		TPMEnabled:     true,
	}

	if err := lifecycle.StartVM(context.Background(), lifecycle.currentConfig); err != nil {
		t.Fatalf("StartVM returned error: %v", err)
	}
	if !manager.lastCleanupOpts.HasTPM {
		t.Fatalf("HasTPM = false")
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

func TestConcreteLifecycleAttachConsoleSyncsTerminalSizeOnSigwinch(t *testing.T) {
	client := &fakeGuestExecClient{
		responses: []guestExecResponse{
			{raw: json.RawMessage(`{}`)},
			{raw: json.RawMessage(`{"pid":17}`)},
			{raw: json.RawMessage(`{"exited":true,"exitcode":0}`)},
			{raw: json.RawMessage(`{}`)},
			{raw: json.RawMessage(`{"pid":18}`)},
			{raw: json.RawMessage(`{"exited":true,"exitcode":0}`)},
		},
	}
	secondResize := make(chan struct{})
	signals := make(chan os.Signal, 1)
	var resizeCount int
	var resizeMu sync.Mutex
	client.commandHook = func(command guestexec.Command) {
		if command.Execute != "guest-exec" {
			return
		}
		args := command.Arguments.(map[string]any)
		arg := strings.Join(args["arg"].([]string), " ")
		if !strings.Contains(arg, "stty") {
			return
		}
		resizeMu.Lock()
		resizeCount++
		count := resizeCount
		resizeMu.Unlock()
		if count == 1 {
			signals <- syscall.SIGWINCH
		}
		if count == 2 {
			close(secondResize)
		}
	}
	lifecycle := NewConcreteLifecycle(testLayout(t))
	lifecycle.GuestClient = client
	lifecycle.Console = fakeConsoleRunnerFunc(func(context.Context, string) (int, error) {
		select {
		case <-secondResize:
			return 0, nil
		case <-time.After(time.Second):
			return 1, errors.New("timed out waiting for resize sync")
		}
	})
	lifecycle.TerminalSize = func() (int, int, bool) { return 40, 120, true }
	lifecycle.Notify = func(ch chan<- os.Signal, _ ...os.Signal) {
		go func() {
			for sig := range signals {
				ch <- sig
			}
		}()
	}
	lifecycle.StopNotify = func(chan<- os.Signal) { close(signals) }
	lifecycle.Sleep = func(context.Context, time.Duration) error { return nil }

	if _, err := lifecycle.AttachConsole(context.Background(), config.VM{VMName: "vm1"}); err != nil {
		t.Fatalf("AttachConsole returned error: %v", err)
	}
	if len(client.commands) != 6 {
		t.Fatalf("commands = %#v", client.commands)
	}
	args := client.commands[1].Arguments.(map[string]any)
	command := strings.Join(args["arg"].([]string), " ")
	for _, want := range []string{"/dev/hvc0", "/dev/ttyS0", "rows 40", "cols 120"} {
		if !strings.Contains(command, want) {
			t.Fatalf("resize command missing %q: %s", want, command)
		}
	}
}

func TestConcreteLifecycleAttachConsoleKeepsInitialResizeFailureSilent(t *testing.T) {
	client := &fakeGuestExecClient{
		responses: []guestExecResponse{
			{err: guestexec.ErrAgentWaitTimeout},
			{err: guestexec.ErrAgentWaitTimeout},
		},
	}
	secondAttempt := make(chan struct{})
	var attempts int
	var attemptsMu sync.Mutex
	client.commandHook = func(guestexec.Command) {
		attemptsMu.Lock()
		defer attemptsMu.Unlock()
		attempts++
		if attempts == 2 {
			close(secondAttempt)
		}
	}
	signals := make(chan os.Signal, 1)
	started := make(chan struct{})
	var status bytes.Buffer
	lifecycle := NewConcreteLifecycle(testLayout(t))
	lifecycle.GuestClient = client
	lifecycle.Status = &status
	lifecycle.Console = fakeConsoleRunnerFunc(func(context.Context, string) (int, error) {
		close(started)
		signals <- syscall.SIGWINCH
		select {
		case <-secondAttempt:
		case <-time.After(time.Second):
			return 1, errors.New("timed out waiting for second resize attempt")
		}
		return 0, nil
	})
	lifecycle.TerminalSize = func() (int, int, bool) { return 40, 120, true }
	lifecycle.Notify = func(ch chan<- os.Signal, _ ...os.Signal) {
		go func() {
			<-started
			for sig := range signals {
				ch <- sig
			}
		}()
	}
	lifecycle.StopNotify = func(chan<- os.Signal) { close(signals) }
	lifecycle.Sleep = func(context.Context, time.Duration) error { return nil }

	if _, err := lifecycle.AttachConsole(context.Background(), config.VM{VMName: "vm1"}); err != nil {
		t.Fatalf("AttachConsole returned error: %v", err)
	}
	warnings := strings.Count(status.String(), "Could not sync console terminal size")
	if warnings != 1 {
		t.Fatalf("warnings = %d status = %q", warnings, status.String())
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
	waitGuestReadyErr  error
	domainStopped      bool
	domainStoppedErr   error
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
	process redfish.Process
	request redfish.Request
}

func (m *fakeRedfishManager) Start(_ context.Context, req redfish.Request) (redfish.Result, error) {
	m.started = true
	m.request = req
	return redfish.Result{Started: true, Process: m.process}, nil
}

type fakeRedfishProcess struct {
	stopCalls int
}

func (p *fakeRedfishProcess) Running() bool {
	return true
}

func (p *fakeRedfishProcess) Stderr() string {
	return ""
}

func (p *fakeRedfishProcess) Stop() error {
	p.stopCalls++
	return nil
}

type guestExecResponse struct {
	raw json.RawMessage
	err error
}

type fakeGuestExecClient struct {
	mu          sync.Mutex
	responses   []guestExecResponse
	commands    []guestexec.Command
	commandHook func(guestexec.Command)
}

func (c *fakeGuestExecClient) ListRunningDomains(context.Context) ([]string, error) {
	return []string{"vm1"}, nil
}

func (c *fakeGuestExecClient) Execute(_ context.Context, _ string, command guestexec.Command) (json.RawMessage, error) {
	c.mu.Lock()
	c.commands = append(c.commands, command)
	if len(c.responses) == 0 {
		c.mu.Unlock()
		return nil, errors.New("unexpected guest command")
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	hook := c.commandHook
	c.mu.Unlock()
	if hook != nil {
		hook(command)
	}
	return resp.raw, resp.err
}

type fakeConsoleRunnerFunc func(context.Context, string) (int, error)

func (f fakeConsoleRunnerFunc) Run(ctx context.Context, vmName string) (int, error) {
	return f(ctx, vmName)
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
	storagePoolErr   error
	cleanupErr       error
	reconcileCalls   int
	startCalls       int
	startErrs        []error
	lastCleanupOpts  libvirtmgr.CleanupOptions
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
func (m *fakeLibvirtManager) Cleanup(_ libvirtmgr.Domain, opts libvirtmgr.CleanupOptions) error {
	m.lastCleanupOpts = opts
	return m.cleanupErr
}
func (m *fakeLibvirtManager) EnsureStoragePool(libvirtmgr.StoragePoolRequest) (libvirtmgr.StoragePool, error) {
	m.storagePoolCalls++
	return nil, m.storagePoolErr
}
func (m *fakeLibvirtManager) Close() error { return nil }

type fakeLibvirtDomain struct {
	name        string
	active      bool
	isActiveErr error
}

func (d *fakeLibvirtDomain) Name() string         { return d.name }
func (d *fakeLibvirtDomain) XML() (string, error) { return managedDomainXML(d.name), nil }
func (d *fakeLibvirtDomain) IsActive() (bool, error) {
	if d.isActiveErr != nil {
		return false, d.isActiveErr
	}
	return d.active, nil
}
func (d *fakeLibvirtDomain) Create() error                 { d.active = true; return nil }
func (d *fakeLibvirtDomain) Shutdown() error               { d.active = false; return nil }
func (d *fakeLibvirtDomain) Destroy() error                { d.active = false; return nil }
func (d *fakeLibvirtDomain) Undefine() error               { return nil }
func (d *fakeLibvirtDomain) UndefineNVRAM() error          { return nil }
func (d *fakeLibvirtDomain) UndefineNVRAMTPM() error       { return nil }
func (d *fakeLibvirtDomain) UndefineTPM() error            { return nil }
func (d *fakeLibvirtDomain) unusedTpmReference(tpm.Result) {}

func (l *fakeLifecycle) StartServices(context.Context, config.VM) error {
	l.calls = append(l.calls, "start-services")
	return l.startServicesErr
}
func (l *fakeLifecycle) StartCleanupServices(context.Context, config.VM) error {
	l.calls = append(l.calls, "start-cleanup-services")
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
	return l.waitGuestReadyErr
}
func (l *fakeLifecycle) WaitUntilStopped(context.Context, config.VM) error {
	l.calls = append(l.calls, "wait-stopped")
	return nil
}
func (l *fakeLifecycle) DomainStopped(context.Context, config.VM) (bool, error) {
	l.calls = append(l.calls, "domain-stopped")
	return l.domainStopped, l.domainStoppedErr
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

func writeGzip(t *testing.T, path string, payload []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir gzip dir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create gzip: %v", err)
	}
	writer := gzip.NewWriter(file)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("write gzip payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close gzip file: %v", err)
	}
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
