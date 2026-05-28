package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDistroConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "distros.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write distro config: %v", err)
	}
	return path
}

func TestLoadDistroConfig(t *testing.T) {
	path := writeDistroConfig(t, `
distributions:
  ubuntu-2404:
    name: Ubuntu 24.04
    url: https://example.com/ubuntu.qcow2
    user: user
  alma-aarch64:
    name: AlmaLinux 9 (aarch64)
    url: https://example.com/alma-aarch64.qcow2
    user: user
    arch: aarch64
    shell: /bin/bash
`)

	cfg, err := LoadDistroConfig(path, "ubuntu-2404")
	if err != nil {
		t.Fatalf("LoadDistroConfig returned error: %v", err)
	}
	if cfg.Name != "Ubuntu 24.04" {
		t.Fatalf("Name = %q", cfg.Name)
	}
	if cfg.URL != "https://example.com/ubuntu.qcow2" {
		t.Fatalf("URL = %q", cfg.URL)
	}
	if cfg.User != "user" {
		t.Fatalf("User = %q", cfg.User)
	}
	if cfg.Arch != "" {
		t.Fatalf("Arch = %q", cfg.Arch)
	}
}

func TestLoadDistroConfigUnknownDistro(t *testing.T) {
	path := writeDistroConfig(t, `
distributions:
  ubuntu-2404:
    name: Ubuntu 24.04
    url: https://example.com/ubuntu.qcow2
`)
	_, err := LoadDistroConfig(path, "nonexistent")
	if err == nil {
		t.Fatal("expected unknown distro error")
	}
	if !strings.Contains(err.Error(), "Unknown distro 'nonexistent'") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "ubuntu-2404") {
		t.Fatalf("error does not list available distros: %v", err)
	}
}

func TestLoadDistroConfigMissingFile(t *testing.T) {
	_, err := LoadDistroConfig(filepath.Join(t.TempDir(), "missing.yaml"), "ubuntu-2404")
	if err == nil {
		t.Fatal("expected missing file error")
	}
	if !strings.Contains(err.Error(), "distribution config missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadDistroConfigMalformedYAML(t *testing.T) {
	path := writeDistroConfig(t, "distributions: [")
	_, err := LoadDistroConfig(path, "ubuntu-2404")
	if err == nil {
		t.Fatal("expected malformed YAML error")
	}
}

func TestLoadDistroConfigMissingRequiredFields(t *testing.T) {
	path := writeDistroConfig(t, `
distributions:
  broken:
    name: Broken
`)
	_, err := LoadDistroConfig(path, "broken")
	if err == nil {
		t.Fatal("expected required field error")
	}
	if !strings.Contains(err.Error(), "url") {
		t.Fatalf("unexpected error: %v", err)
	}
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

