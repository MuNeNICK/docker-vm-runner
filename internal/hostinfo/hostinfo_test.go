package hostinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMemInfo(t *testing.T) {
	total, available := parseMemInfo([]byte("MemTotal: 2097152 kB\nMemAvailable: 1048576 kB\n"))
	if total != 2*1024*1024*1024 || available != 1*1024*1024*1024 {
		t.Fatalf("total=%d available=%d", total, available)
	}
}

func TestFirstCPUModel(t *testing.T) {
	model := firstCPUModel([]byte("processor: 0\nmodel name: Example CPU\n"))
	if model != "Example CPU" {
		t.Fatalf("model = %q", model)
	}
}

func TestCPUVendorAndFlags(t *testing.T) {
	content := []byte("vendor_id\t: GenuineIntel\nflags\t\t: sse apicv avic\n")
	if got := cpuVendor(content); got != "intel" {
		t.Fatalf("cpuVendor = %q", got)
	}
	flags := cpuFlags(content)
	if !flags["apicv"] || !flags["avic"] {
		t.Fatalf("cpuFlags = %#v", flags)
	}

	if got := cpuVendor([]byte("vendor_id\t: AuthenticAMD\n")); got != "amd" {
		t.Fatalf("cpuVendor AMD = %q", got)
	}
}

func TestLines(t *testing.T) {
	lines := Lines(Info{
		CPUModel:        "Example CPU",
		CPUCount:        8,
		MemTotalBytes:   16 * 1024 * 1024 * 1024,
		MemAvailBytes:   4 * 1024 * 1024 * 1024,
		DiskAvailBytes:  20 * 1024 * 1024 * 1024,
		DiskPath:        "/images",
		KVMAvailable:    true,
		Kernel:          "6.0.0",
		RuntimeEngine:   "docker",
		RuntimeRootless: true,
		RuntimePriv:     false,
	})
	text := strings.Join(lines, "\n")
	for _, needle := range []string{
		"CPU:     Example CPU (8 cores)",
		"Memory:  4.0 GiB free / 16.0 GiB total",
		"Storage: 20.0 GiB free at /images",
		"KVM:     available",
		"Runtime: docker (unprivileged, rootless)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing %q in:\n%s", needle, text)
		}
	}
}

func TestRuntimeEngineFromContainerMarkers(t *testing.T) {
	if got := runtimeEngineFrom([]byte("0::/docker/abcdef\n"), false, false); got != "docker" {
		t.Fatalf("docker cgroup engine = %q", got)
	}
	if got := runtimeEngineFrom([]byte("0::/libpod-abcdef.scope\n"), false, false); got != "podman" {
		t.Fatalf("podman cgroup engine = %q", got)
	}
	if got := runtimeEngineFrom(nil, true, false); got != "docker" {
		t.Fatalf("docker marker engine = %q", got)
	}
	if got := runtimeEngineFrom(nil, false, true); got != "podman" {
		t.Fatalf("podman marker engine = %q", got)
	}
}

func TestRuntimeRootlessFromUIDMap(t *testing.T) {
	if !runtimeRootlessFromUIDMap([]byte("0 100000 65536\n"), 0) {
		t.Fatal("rootless uid map was not detected")
	}
	if runtimeRootlessFromUIDMap([]byte("0 0 4294967295\n"), 0) {
		t.Fatal("host uid map was detected as rootless")
	}
	if !runtimeRootlessFromUIDMap(nil, 1000) {
		t.Fatal("non-root euid was not detected as rootless")
	}
}

func TestRuntimePrivilegedFromStatus(t *testing.T) {
	if !runtimePrivilegedFromStatus([]byte("CapEff:\t0000000000200000\n")) {
		t.Fatal("CAP_SYS_ADMIN should be treated as privileged")
	}
	if runtimePrivilegedFromStatus([]byte("CapEff:\t0000000000000000\n")) {
		t.Fatal("empty capabilities should not be privileged")
	}
}

func TestFilesystemProbes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "user-data.yaml")
	if err := os.WriteFile(file, []byte("#cloud-config\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if !FileExists(file) {
		t.Fatal("FileExists = false")
	}
	if !IsFile(file) {
		t.Fatal("IsFile = false")
	}
	if IsFile(dir) {
		t.Fatal("IsFile directory = true")
	}
	if IsBlockDevice(file) {
		t.Fatal("IsBlockDevice regular file = true")
	}
}

func TestIsMountReadsMountInfo(t *testing.T) {
	mountInfo := "42 24 0:39 / / rw,relatime - overlay overlay rw\n" +
		"43 42 0:40 / /data rw,relatime - ext4 /dev/sda1 rw\n" +
		"44 42 0:41 / /path\\040with\\040space rw,relatime - ext4 /dev/sda2 rw\n"
	readFile := func(string) ([]byte, error) { return []byte(mountInfo), nil }

	if !isMount("/data", "/proc/self/mountinfo", readFile) {
		t.Fatal("/data mount was not detected")
	}
	if !isMount("/path with space", "/proc/self/mountinfo", readFile) {
		t.Fatal("escaped mount path was not detected")
	}
	if isMount("/images", "/proc/self/mountinfo", readFile) {
		t.Fatal("/images incorrectly detected as mount")
	}
}

func TestKVMAvailableRequiresOpenableDevice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kvm")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("write kvm file: %v", err)
	}
	if !kvmAvailable(path, os.Open) {
		t.Fatal("openable path should be available")
	}
	if kvmAvailable(filepath.Join(dir, "missing"), os.Open) {
		t.Fatal("missing path should not be available")
	}
	if kvmAvailable(path, func(string) (*os.File, error) {
		return nil, os.ErrPermission
	}) {
		t.Fatal("permission error should not be available")
	}
}

func TestBlockSectorSizeReadsSysfs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "vdb", "queue")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir sysfs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "logical_block_size"), []byte("4096\n"), 0o644); err != nil {
		t.Fatalf("write sector size: %v", err)
	}

	size, ok := blockSectorSize("/dev/vdb", root, os.ReadFile)
	if !ok || size != 4096 {
		t.Fatalf("blockSectorSize = %d, %v", size, ok)
	}
}

func TestBlockSectorSizeIgnoresInvalidSysfs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "vdb", "queue")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir sysfs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "logical_block_size"), []byte("bad\n"), 0o644); err != nil {
		t.Fatalf("write sector size: %v", err)
	}

	if size, ok := blockSectorSize("/dev/vdb", root, os.ReadFile); ok || size != 0 {
		t.Fatalf("blockSectorSize = %d, %v", size, ok)
	}
}

func TestIPv6AvailableReadsDisableFlag(t *testing.T) {
	readFile := func(string) ([]byte, error) { return []byte("0\n"), nil }
	if !ipv6Available("/proc/test", readFile) {
		t.Fatal("ipv6Available = false")
	}
	readFile = func(string) ([]byte, error) { return []byte("1\n"), nil }
	if ipv6Available("/proc/test", readFile) {
		t.Fatal("ipv6Available = true")
	}
}

func TestAvailableDiskBytesUsesExistingParent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", "vms")
	if got := AvailableDiskBytes(missing); got <= 0 {
		t.Fatalf("AvailableDiskBytes(%q) = %d", missing, got)
	}
}
