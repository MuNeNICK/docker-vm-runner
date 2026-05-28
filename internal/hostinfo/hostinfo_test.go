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

func TestAvailableDiskBytesUsesExistingParent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", "vms")
	if got := AvailableDiskBytes(missing); got <= 0 {
		t.Fatalf("AvailableDiskBytes(%q) = %d", missing, got)
	}
}
