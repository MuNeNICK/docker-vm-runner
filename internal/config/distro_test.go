package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDistroConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "supported.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write catalog config: %v", err)
	}
	return path
}

func TestLoadDistroConfig(t *testing.T) {
	path := writeDistroConfig(t, testCatalogJSON())

	cfg, err := LoadDistroConfig(path, "ubuntu-24.04-server")
	if err != nil {
		t.Fatalf("LoadDistroConfig returned error: %v", err)
	}
	if cfg.Name != "Ubuntu 24.04 LTS Server" {
		t.Fatalf("Name = %q", cfg.Name)
	}
	if cfg.URL != "https://example.com/ubuntu.iso" {
		t.Fatalf("URL = %q", cfg.URL)
	}
	if cfg.User != "user" {
		t.Fatalf("User = %q", cfg.User)
	}
	if cfg.Arch != "amd64" {
		t.Fatalf("Arch = %q", cfg.Arch)
	}
	if cfg.ChecksumAlgorithm != "sha256" || cfg.ChecksumValue != "abc123" {
		t.Fatalf("Checksum = %q %q", cfg.ChecksumAlgorithm, cfg.ChecksumValue)
	}
}

func TestLoadDistroConfigUnknownDistro(t *testing.T) {
	path := writeDistroConfig(t, testCatalogJSON())
	_, err := LoadDistroConfig(path, "nonexistent")
	if err == nil {
		t.Fatal("expected unknown distro error")
	}
	if !strings.Contains(err.Error(), "Unknown catalog image 'nonexistent'") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "ubuntu-24.04-server") {
		t.Fatalf("error does not list available image IDs: %v", err)
	}
}

func TestLoadDistroConfigMissingFile(t *testing.T) {
	_, err := LoadDistroConfig(filepath.Join(t.TempDir(), "missing.json"), "ubuntu-24.04-server")
	if err == nil {
		t.Fatal("expected missing file error")
	}
	if !strings.Contains(err.Error(), "catalog config missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadDistroConfigMalformedJSON(t *testing.T) {
	path := writeDistroConfig(t, `{"meta":`)
	_, err := LoadDistroConfig(path, "ubuntu-24.04-server")
	if err == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestLoadDistroConfigMissingRequiredFields(t *testing.T) {
	path := writeDistroConfig(t, `{"meta":{"api_version":"v1"},"images":[{"id":"broken","name":"Broken","category":"linux","version":"1","arch":"amd64","status":"supported"}]}`)
	_, err := LoadDistroConfig(path, "broken")
	if err == nil {
		t.Fatal("expected required field error")
	}
	if !strings.Contains(err.Error(), "url") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testCatalogJSON() string {
	return `{
	  "meta": {"api_version": "v1", "count": 2},
	  "images": [
	    {
	      "id": "ubuntu-24.04-server",
	      "name": "Ubuntu 24.04 LTS",
	      "category": "linux",
	      "distro": "ubuntu",
	      "version": "24.04",
	      "edition": "Server",
	      "arch": "amd64",
	      "release_type": "stable",
	      "url": "https://example.com/ubuntu.iso",
	      "checksum": {"algorithm": "sha256", "value": "abc123"},
	      "eol": {"standard": "2029-05-31", "is_rolling": false},
	      "status": "supported"
	    },
	    {
	      "id": "alma-9-aarch64",
	      "name": "AlmaLinux 9",
	      "category": "linux",
	      "distro": "almalinux",
	      "version": "9",
	      "edition": "DVD",
	      "arch": "aarch64",
	      "release_type": "stable",
	      "url": "https://example.com/alma.iso",
	      "eol": {"standard": "2032-05-31", "is_rolling": false},
	      "status": "supported"
	    }
	  ]
	}`
}

func TestNormalizeArchitecture(t *testing.T) {
	tests := map[string]string{
		"amd64":   "x86_64",
		"arm64":   "aarch64",
		"ppc64le": "ppc64",
		"riscv":   "riscv64",
		"x86_64":  "x86_64",
	}
	for input, want := range tests {
		got, err := NormalizeArchitecture(input)
		if err != nil {
			t.Fatalf("NormalizeArchitecture(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeArchitecture(%q) = %q want %q", input, got, want)
		}
	}
}

func TestNormalizeArchitectureUnsupported(t *testing.T) {
	_, err := NormalizeArchitecture("mips64")
	if err == nil {
		t.Fatal("expected unsupported architecture error")
	}
	if !strings.Contains(err.Error(), "Unsupported ARCH 'mips64'") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveArchitecture(t *testing.T) {
	got, err := ResolveArchitecture("x86_64", "")
	if err != nil {
		t.Fatalf("ResolveArchitecture: %v", err)
	}
	if got != "x86_64" {
		t.Fatalf("ResolveArchitecture = %q", got)
	}

	got, err = ResolveArchitecture("", "amd64")
	if err != nil {
		t.Fatalf("ResolveArchitecture override: %v", err)
	}
	if got != "x86_64" {
		t.Fatalf("ResolveArchitecture override = %q", got)
	}
}

func TestResolveArchitectureMismatch(t *testing.T) {
	_, err := ResolveArchitecture("aarch64", "x86_64")
	if err == nil {
		t.Fatal("expected architecture mismatch")
	}
	if !strings.Contains(err.Error(), "does not match distribution") {
		t.Fatalf("unexpected error: %v", err)
	}
}
