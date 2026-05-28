package firmware

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/config"
)

func TestPrepareX86LegacyNoop(t *testing.T) {
	result, err := NewPreparer(t.TempDir()).Prepare(Request{
		Arch:     "x86_64",
		BootMode: "legacy",
		VMName:   "test-vm",
		Profile:  config.SupportedArchitectures["x86_64"],
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if result.Needed {
		t.Fatalf("Needed = true")
	}
	if result.LoaderPath != "" || result.VarsPath != "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestPrepareX86MissingFirmwareRaises(t *testing.T) {
	dir := t.TempDir()
	profile := config.ArchitectureProfile{Firmware: map[string]config.FirmwareProfile{
		"uefi": {
			Loader:       filepath.Join(dir, "missing-loader.fd"),
			VarsTemplate: filepath.Join(dir, "missing-vars.fd"),
		},
	}}

	_, err := NewPreparer(filepath.Join(dir, "state")).Prepare(Request{
		Arch:     "x86_64",
		BootMode: "uefi",
		VMName:   "test-vm",
		Profile:  profile,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "OVMF firmware not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareX86SuccessCopiesNVRAM(t *testing.T) {
	dir := t.TempDir()
	loader := filepath.Join(dir, "OVMF_CODE.fd")
	varsTemplate := filepath.Join(dir, "OVMF_VARS.fd")
	writeFile(t, loader, []byte("loader"))
	writeFile(t, varsTemplate, []byte("vars"))

	result, err := NewPreparer(filepath.Join(dir, "state")).Prepare(Request{
		Arch:     "x86_64",
		BootMode: "uefi",
		VMName:   "test-vm",
		Profile: config.ArchitectureProfile{Firmware: map[string]config.FirmwareProfile{
			"uefi": {Loader: loader, VarsTemplate: varsTemplate},
		}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if !result.Needed {
		t.Fatalf("Needed = false")
	}
	if result.LoaderPath != loader {
		t.Fatalf("LoaderPath = %q", result.LoaderPath)
	}
	if filepath.Base(result.VarsPath) != "test-vm-vars.fd" {
		t.Fatalf("VarsPath = %q", result.VarsPath)
	}
	if string(readFile(t, result.VarsPath)) != "vars" {
		t.Fatalf("vars content mismatch")
	}
}

func TestPrepareDoesNotOverwriteExistingNVRAM(t *testing.T) {
	dir := t.TempDir()
	loader := filepath.Join(dir, "OVMF_CODE.fd")
	varsTemplate := filepath.Join(dir, "OVMF_VARS.fd")
	stateDir := filepath.Join(dir, "state")
	existingVars := filepath.Join(stateDir, "firmware", "test-vm-vars.fd")
	writeFile(t, loader, []byte("loader"))
	writeFile(t, varsTemplate, []byte("template"))
	writeFile(t, existingVars, []byte("existing"))

	result, err := NewPreparer(stateDir).Prepare(Request{
		Arch:     "x86_64",
		BootMode: "secure",
		VMName:   "test-vm",
		Profile: config.ArchitectureProfile{Firmware: map[string]config.FirmwareProfile{
			"secure": {Loader: loader, VarsTemplate: varsTemplate},
		}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if result.VarsPath != existingVars {
		t.Fatalf("VarsPath = %q", result.VarsPath)
	}
	if string(readFile(t, existingVars)) != "existing" {
		t.Fatalf("existing nvram was overwritten")
	}
}

func TestPrepareAarch64ExtractsAAVMFWhenMissing(t *testing.T) {
	dir := t.TempDir()
	loader := filepath.Join(dir, "AAVMF_CODE.fd")
	varsTemplate := filepath.Join(dir, "AAVMF_VARS.fd")
	called := false
	preparer := NewPreparer(filepath.Join(dir, "state"))
	preparer.ExtractAAVMF = func() error {
		called = true
		writeFile(t, loader, []byte("loader"))
		writeFile(t, varsTemplate, []byte("vars"))
		return nil
	}

	result, err := preparer.Prepare(Request{
		Arch:     "aarch64",
		BootMode: "uefi",
		VMName:   "arm-vm",
		Profile: config.ArchitectureProfile{Firmware: map[string]config.FirmwareProfile{
			"default": {Loader: loader, VarsTemplate: varsTemplate},
		}},
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if !called {
		t.Fatalf("ExtractAAVMF was not called")
	}
	if result.LoaderPath != loader {
		t.Fatalf("LoaderPath = %q", result.LoaderPath)
	}
}

func TestAAVMFExtractCommand(t *testing.T) {
	cmd := AAVMFExtractCommand("/opt/aavmf.deb", "/tmp/extract")
	want := []string{"dpkg-deb", "-x", "/opt/aavmf.deb", "/tmp/extract"}
	if len(cmd) != len(want) {
		t.Fatalf("len = %d want %d: %#v", len(cmd), len(want), cmd)
	}
	for i := range want {
		if cmd[i] != want[i] {
			t.Fatalf("cmd[%d] = %q want %q", i, cmd[i], want[i])
		}
	}
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

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}
