package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/paths"
)

func testResolver(t *testing.T) (*Resolver, string) {
	t.Helper()
	configPath := writeDistroConfig(t, `
distributions:
  ubuntu-2404:
    name: Ubuntu 24.04
    url: https://example.com/ubuntu.qcow2
    user: user
    arch: x86_64
    shell: /bin/bash
  alma-aarch64:
    name: AlmaLinux 9
    url: https://example.com/alma-aarch64.qcow2
    user: user
    arch: aarch64
`)
	resolver := &Resolver{
		DistroConfigPath:     configPath,
		Layout:               paths.ResolveLayout("", func(string) bool { return false }),
		AvailableMemoryBytes: func() int64 { return 8 * 1024 * 1024 * 1024 },
		CPUCount:             func() int { return 8 },
		AvailableDiskBytes:   func(string) int64 { return 100 * 1024 * 1024 * 1024 },
		DetectHostMTU:        func() int { return 1500 },
		ROMExists:            func(string) bool { return false },
		FileExists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		IsFile: func(path string) bool {
			stat, err := os.Stat(path)
			return err == nil && !stat.IsDir()
		},
		ReadFile: os.ReadFile,
	}
	return resolver, configPath
}

func TestResolveDefaultConfig(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.Distro != "ubuntu-2404" {
		t.Fatalf("Distro = %q", cfg.Distro)
	}
	if cfg.DistroName != "Ubuntu 24.04" {
		t.Fatalf("DistroName = %q", cfg.DistroName)
	}
	if cfg.ImageURL != "https://example.com/ubuntu.qcow2" {
		t.Fatalf("ImageURL = %q", cfg.ImageURL)
	}
	if cfg.LoginUser != "user" {
		t.Fatalf("LoginUser = %q", cfg.LoginUser)
	}
	if cfg.MemoryMB != 4096 {
		t.Fatalf("MemoryMB = %d", cfg.MemoryMB)
	}
	if cfg.CPUs != 2 {
		t.Fatalf("CPUs = %d", cfg.CPUs)
	}
	if cfg.DiskSize != "20G" {
		t.Fatalf("DiskSize = %q", cfg.DiskSize)
	}
	if cfg.Arch != "x86_64" {
		t.Fatalf("Arch = %q", cfg.Arch)
	}
	if cfg.CPUModel != "host" {
		t.Fatalf("CPUModel = %q", cfg.CPUModel)
	}
	if cfg.SSHPort != 2222 {
		t.Fatalf("SSHPort = %d", cfg.SSHPort)
	}
	if !cfg.CloudInitEnabled {
		t.Fatal("CloudInitEnabled = false")
	}
	if cfg.Persist {
		t.Fatal("Persist = true")
	}
	if cfg.ForceISO {
		t.Fatal("ForceISO = true")
	}
	if len(cfg.NICs) != 1 || cfg.NICs[0].Mode != "user" {
		t.Fatalf("NICs = %#v", cfg.NICs)
	}
}

func TestResolveCustomMemoryAndCPUs(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"MEMORY": "8192", "CPUS": "4"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.MemoryMB != 8192 || cfg.CPUs != 4 {
		t.Fatalf("resources = memory %d cpus %d", cfg.MemoryMB, cfg.CPUs)
	}
}

func TestResolveMaxHalfResources(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"MEMORY": "max", "CPUS": "half", "DISK_SIZE": "half"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.MemoryMB != 7680 {
		t.Fatalf("MemoryMB = %d", cfg.MemoryMB)
	}
	if cfg.CPUs != 4 {
		t.Fatalf("CPUs = %d", cfg.CPUs)
	}
	if cfg.DiskSize != "50G" {
		t.Fatalf("DiskSize = %q", cfg.DiskSize)
	}
}

func TestResolveArchAlias(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"ARCH": "amd64"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.Arch != "x86_64" {
		t.Fatalf("Arch = %q", cfg.Arch)
	}
}

func TestResolveArchMismatch(t *testing.T) {
	resolver, _ := testResolver(t)
	_, err := resolver.Resolve(MapEnv{"DISTRO": "alma-aarch64", "ARCH": "x86_64"})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "does not match distribution") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveGraphicsNoVNC(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"GRAPHICS": "novnc"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !cfg.NoVNCEnabled {
		t.Fatal("NoVNCEnabled = false")
	}
	if cfg.GraphicsType != "vnc" {
		t.Fatalf("GraphicsType = %q", cfg.GraphicsType)
	}
}

func TestResolveISOAutoDisablesCloudInit(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"BOOT_FROM": "https://example.com/installer.iso"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.CloudInitEnabled {
		t.Fatal("CloudInitEnabled = true")
	}
	if !cfg.BlankWorkDisk {
		t.Fatal("BlankWorkDisk = false")
	}
	if cfg.BootOrder[0] != "cdrom" {
		t.Fatalf("BootOrder = %#v", cfg.BootOrder)
	}
	if cfg.DistroName != "Custom ISO" {
		t.Fatalf("DistroName = %q", cfg.DistroName)
	}
}

func TestResolveISOCloudInitOverride(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"BOOT_FROM": "https://example.com/installer.iso", "CLOUD_INIT": "1"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !cfg.CloudInitEnabled {
		t.Fatal("CloudInitEnabled = false")
	}
}

func TestResolveCloudInitUserDataMissingFile(t *testing.T) {
	resolver, _ := testResolver(t)
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	_, err := resolver.Resolve(MapEnv{"CLOUD_INIT_USER_DATA": missing})
	if err == nil {
		t.Fatal("expected missing user-data error")
	}
	if !strings.Contains(err.Error(), "CLOUD_INIT_USER_DATA file not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveCloudInitUserDataInvalidYAML(t *testing.T) {
	resolver, _ := testResolver(t)
	userData := filepath.Join(t.TempDir(), "user-data.yaml")
	if err := os.WriteFile(userData, []byte("#cloud-config\nusers: [\n"), 0o644); err != nil {
		t.Fatalf("write user-data: %v", err)
	}
	_, err := resolver.Resolve(MapEnv{"CLOUD_INIT_USER_DATA": userData})
	if err == nil {
		t.Fatal("expected invalid YAML error")
	}
	if !strings.Contains(err.Error(), "contains invalid YAML") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveRedfish(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{
		"REDFISH_ENABLE":   "1",
		"REDFISH_USERNAME": "operator",
		"REDFISH_PASSWORD": "secret",
		"REDFISH_PORT":     "9443",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !cfg.RedfishEnabled {
		t.Fatal("RedfishEnabled = false")
	}
	if cfg.RedfishUser != "operator" || cfg.RedfishPassword != "secret" || cfg.RedfishPort != 9443 {
		t.Fatalf("redfish = %#v", cfg)
	}
}

func TestResolvePortForwardAndConflict(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"PORT_FWD": "8080:80,8443:443"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(cfg.PortForwards) != 2 {
		t.Fatalf("PortForwards count = %d", len(cfg.PortForwards))
	}

	_, err = resolver.Resolve(MapEnv{"SSH_PORT": "8080", "PORT_FWD": "8080:80"})
	if err == nil {
		t.Fatal("expected port conflict")
	}
	if !strings.Contains(err.Error(), "Port conflict") {
		t.Fatalf("unexpected error: %v", err)
	}
}
