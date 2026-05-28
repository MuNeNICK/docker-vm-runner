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
	configPath := writeDistroConfig(t, testCatalogJSON())
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
	if cfg.Distro != "ubuntu-24.04-cloud-amd64" {
		t.Fatalf("Distro = %q", cfg.Distro)
	}
	if cfg.DistroName != "Ubuntu 24.04 LTS Cloud" {
		t.Fatalf("DistroName = %q", cfg.DistroName)
	}
	if cfg.ImageURL != "https://example.com/ubuntu.qcow2" {
		t.Fatalf("ImageURL = %q", cfg.ImageURL)
	}
	if cfg.SourceImageType != "cloud-image" || cfg.SourceImageFormat != "qcow2" || cfg.SourceImageCompression != "none" {
		t.Fatalf("source metadata = %q %q %q", cfg.SourceImageType, cfg.SourceImageFormat, cfg.SourceImageCompression)
	}
	if cfg.DownloadMaxBytes != 64*1024*1024*1024 {
		t.Fatalf("DownloadMaxBytes = %d", cfg.DownloadMaxBytes)
	}
	if cfg.ExtractMaxBytes != 512*1024*1024*1024 {
		t.Fatalf("ExtractMaxBytes = %d", cfg.ExtractMaxBytes)
	}
	if cfg.ImageChecksumAlgorithm != "sha256" || cfg.ImageChecksumValue != "abc123" {
		t.Fatalf("Image checksum = %q %q", cfg.ImageChecksumAlgorithm, cfg.ImageChecksumValue)
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
	_, err := resolver.Resolve(MapEnv{"DISTRO": "alma-9-aarch64", "ARCH": "x86_64"})
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
	if cfg.BootChecksumAlgorithm != "" || cfg.BootChecksumValue != "" {
		t.Fatalf("custom ISO boot checksum = %q %q", cfg.BootChecksumAlgorithm, cfg.BootChecksumValue)
	}
}

func TestResolveCatalogISOUsesBootMediaAndBlankDisk(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"DISTRO": "ubuntu-24.04-server"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.ImageURL != "" {
		t.Fatalf("ImageURL = %q", cfg.ImageURL)
	}
	if cfg.BootFrom != "https://example.com/ubuntu.iso" {
		t.Fatalf("BootFrom = %q", cfg.BootFrom)
	}
	if cfg.BootChecksumAlgorithm != "sha256" || cfg.BootChecksumValue != "def456" {
		t.Fatalf("Boot checksum = %q %q", cfg.BootChecksumAlgorithm, cfg.BootChecksumValue)
	}
	if !cfg.BlankWorkDisk {
		t.Fatal("BlankWorkDisk = false")
	}
	if cfg.BootOrder[0] != "cdrom" {
		t.Fatalf("BootOrder = %#v", cfg.BootOrder)
	}
	if cfg.DistroName != "Ubuntu 24.04 LTS Server" {
		t.Fatalf("DistroName = %q", cfg.DistroName)
	}
	if cfg.CloudInitEnabled {
		t.Fatal("CloudInitEnabled = true")
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

func TestResolveIPXEStoresROMAndBootsNetwork(t *testing.T) {
	resolver, _ := testResolver(t)
	rom := "/tmp/ipxe.rom"
	resolver.ROMExists = func(path string) bool { return path == rom }

	cfg, err := resolver.Resolve(MapEnv{
		"IPXE_ENABLE":   "1",
		"IPXE_ROM_PATH": rom,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !cfg.IPXEEnabled {
		t.Fatal("IPXEEnabled = false")
	}
	if cfg.IPXEROMPath != rom {
		t.Fatalf("IPXEROMPath = %q", cfg.IPXEROMPath)
	}
	if len(cfg.BootOrder) == 0 || cfg.BootOrder[0] != "network" {
		t.Fatalf("BootOrder = %#v", cfg.BootOrder)
	}
}

func TestResolveIPXEMovesExistingNetworkBootFirst(t *testing.T) {
	resolver, _ := testResolver(t)
	rom := "/tmp/ipxe.rom"
	resolver.ROMExists = func(path string) bool { return path == rom }

	cfg, err := resolver.Resolve(MapEnv{
		"IPXE_ENABLE":   "1",
		"IPXE_ROM_PATH": rom,
		"BOOT_ORDER":    "hd,network",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got := strings.Join(cfg.BootOrder, ","); got != "network,hd" {
		t.Fatalf("BootOrder = %#v", cfg.BootOrder)
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

func TestResolveCloudInitUserDataWarnsOnUnrecognizedHeader(t *testing.T) {
	resolver, _ := testResolver(t)
	userData := filepath.Join(t.TempDir(), "user-data.txt")
	if err := os.WriteFile(userData, []byte("hostname: demo\n"), 0o644); err != nil {
		t.Fatalf("write user-data: %v", err)
	}

	cfg, err := resolver.Resolve(MapEnv{"CLOUD_INIT_USER_DATA": userData})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "CLOUD_INIT_USER_DATA") {
		t.Fatalf("Warnings = %#v", cfg.Warnings)
	}
}

func TestResolveWarnsWhenISOAutoDisablesCloudInit(t *testing.T) {
	resolver, _ := testResolver(t)
	iso := filepath.Join(t.TempDir(), "installer.iso")
	if err := os.WriteFile(iso, []byte("iso"), 0o644); err != nil {
		t.Fatalf("write iso: %v", err)
	}

	cfg, err := resolver.Resolve(MapEnv{"BOOT_FROM": iso})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.CloudInitEnabled {
		t.Fatal("CloudInitEnabled = true")
	}
	if !containsWarning(cfg.Warnings, "auto-disabling cloud-init") {
		t.Fatalf("Warnings = %#v", cfg.Warnings)
	}
}

func TestResolveCloudConfigWarnsWhenNotMapping(t *testing.T) {
	resolver, _ := testResolver(t)
	userData := filepath.Join(t.TempDir(), "user-data.yaml")
	if err := os.WriteFile(userData, []byte("#cloud-config\n- bad\n"), 0o644); err != nil {
		t.Fatalf("write user-data: %v", err)
	}

	cfg, err := resolver.Resolve(MapEnv{"CLOUD_INIT_USER_DATA": userData})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !containsWarning(cfg.Warnings, "not a YAML mapping") {
		t.Fatalf("Warnings = %#v", cfg.Warnings)
	}
}

func containsWarning(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
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

func TestResolveRedfishRejectsDefaultPassword(t *testing.T) {
	resolver, _ := testResolver(t)
	_, err := resolver.Resolve(MapEnv{"REDFISH_ENABLE": "1"})
	if err == nil {
		t.Fatal("expected default password error")
	}
	if !strings.Contains(err.Error(), "REDFISH_PASSWORD must be changed") {
		t.Fatalf("unexpected error: %v", err)
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

func TestResolveDataDirDefaultsPersist(t *testing.T) {
	resolver, _ := testResolver(t)
	resolver.Layout = paths.ResolveLayout("/data", func(string) bool { return false })
	cfg, err := resolver.Resolve(MapEnv{})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !cfg.Persist {
		t.Fatal("Persist = false")
	}

	cfg, err = resolver.Resolve(MapEnv{"PERSIST": "0"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.Persist {
		t.Fatal("Persist override = true")
	}
}

func TestResolveRejectsInvalidBooleanEnv(t *testing.T) {
	resolver, _ := testResolver(t)
	_, err := resolver.Resolve(MapEnv{"PERSIST": "ture"})
	if err == nil {
		t.Fatal("expected boolean error")
	}
	if !strings.Contains(err.Error(), "PERSIST must be a boolean") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSizeLimits(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{
		"DOWNLOAD_MAX_SIZE": "1G",
		"EXTRACT_MAX_SIZE":  "2G",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.DownloadMaxBytes != 1024*1024*1024 {
		t.Fatalf("DownloadMaxBytes = %d", cfg.DownloadMaxBytes)
	}
	if cfg.ExtractMaxBytes != 2*1024*1024*1024 {
		t.Fatalf("ExtractMaxBytes = %d", cfg.ExtractMaxBytes)
	}

	_, err = resolver.Resolve(MapEnv{"DOWNLOAD_MAX_SIZE": "bad"})
	if err == nil {
		t.Fatal("expected invalid size limit")
	}
	if !strings.Contains(err.Error(), "DOWNLOAD_MAX_SIZE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveBootModeAndTPM(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"BOOT_MODE": "secure"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.BootMode != "secure" {
		t.Fatalf("BootMode = %q", cfg.BootMode)
	}
	if !cfg.TPMEnabled {
		t.Fatal("TPMEnabled = false")
	}

	cfg, err = resolver.Resolve(MapEnv{"BOOT_MODE": "secure", "TPM": "0"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.TPMEnabled {
		t.Fatal("TPM override = true")
	}
}

func TestResolveInvalidBootMode(t *testing.T) {
	resolver, _ := testResolver(t)
	_, err := resolver.Resolve(MapEnv{"BOOT_MODE": "bad"})
	if err == nil {
		t.Fatal("expected boot mode error")
	}
	if !strings.Contains(err.Error(), "Invalid BOOT_MODE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveMachineType(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"MACHINE": "pc"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.MachineType != "pc" {
		t.Fatalf("MachineType = %q", cfg.MachineType)
	}

	_, err = resolver.Resolve(MapEnv{"MACHINE": "bad"})
	if err == nil {
		t.Fatal("expected machine error")
	}
}

func TestResolveDiskOptions(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{
		"DISK_TYPE":  "scsi",
		"DISK_IO":    "threads",
		"DISK_CACHE": "writeback",
		"ALLOCATE":   "1",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.DiskController != "scsi" {
		t.Fatalf("DiskController = %q", cfg.DiskController)
	}
	if cfg.DiskIO != "threads" {
		t.Fatalf("DiskIO = %q", cfg.DiskIO)
	}
	if cfg.DiskCache != "writeback" {
		t.Fatalf("DiskCache = %q", cfg.DiskCache)
	}
	if !cfg.DiskPreallocate {
		t.Fatal("DiskPreallocate = false")
	}
}

func TestResolveInvalidDiskOptions(t *testing.T) {
	resolver, _ := testResolver(t)
	tests := []MapEnv{
		{"DISK_TYPE": "bogus"},
		{"DISK_IO": "bogus"},
		{"DISK_CACHE": "bogus"},
	}
	for _, env := range tests {
		if _, err := resolver.Resolve(env); err == nil {
			t.Fatalf("expected error for env %#v", env)
		}
	}
}

func TestResolveExtraDisks(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"DISK2_SIZE": "10G", "DISK6_SIZE": "1T"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(cfg.ExtraDisks) != 2 {
		t.Fatalf("ExtraDisks count = %d", len(cfg.ExtraDisks))
	}
	if cfg.ExtraDisks[0].Index != 2 || cfg.ExtraDisks[0].Size != "10G" {
		t.Fatalf("first extra disk = %#v", cfg.ExtraDisks[0])
	}
	if cfg.ExtraDisks[1].Index != 6 || cfg.ExtraDisks[1].Size != "1T" {
		t.Fatalf("second extra disk = %#v", cfg.ExtraDisks[1])
	}
}

func TestResolveInvalidExtraDiskSize(t *testing.T) {
	resolver, _ := testResolver(t)
	_, err := resolver.Resolve(MapEnv{"DISK2_SIZE": "not-a-size"})
	if err == nil {
		t.Fatal("expected extra disk size error")
	}
	if !strings.Contains(err.Error(), "invalid DISK2_SIZE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveFilesystems(t *testing.T) {
	resolver, _ := testResolver(t)
	source := t.TempDir()

	cfg, err := resolver.Resolve(MapEnv{
		"FILESYSTEM_SOURCE":   source,
		"FILESYSTEM_TARGET":   "data",
		"FILESYSTEM_DRIVER":   "9p",
		"FILESYSTEM_READONLY": "1",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(cfg.Filesystems) != 1 {
		t.Fatalf("Filesystems count = %d", len(cfg.Filesystems))
	}
	share := cfg.Filesystems[0]
	if share.Source != source || share.Target != "data" || share.Driver != "9p" || !share.Readonly {
		t.Fatalf("Filesystem = %#v", share)
	}
}

func TestResolveBlockDevice(t *testing.T) {
	resolver, _ := testResolver(t)
	device := "/dev/testblk"
	resolver.FileExists = func(path string) bool { return path == device }
	resolver.IsBlockDevice = func(path string) bool { return path == device }

	cfg, err := resolver.Resolve(MapEnv{"DEVICE": device})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(cfg.BlockDevices) != 1 {
		t.Fatalf("BlockDevices count = %d", len(cfg.BlockDevices))
	}
	if cfg.BlockDevices[0].Path != device || cfg.BlockDevices[0].Index != 1 {
		t.Fatalf("BlockDevice = %#v", cfg.BlockDevices[0])
	}
}

func TestResolveBlockDeviceMustExistAndBeBlockDevice(t *testing.T) {
	resolver, _ := testResolver(t)
	_, err := resolver.Resolve(MapEnv{"DEVICE": "/dev/missing"})
	if err == nil {
		t.Fatal("expected missing device error")
	}

	file := filepath.Join(t.TempDir(), "not-block")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err = resolver.Resolve(MapEnv{"DEVICE": file})
	if err == nil {
		t.Fatal("expected block device error")
	}
	if !strings.Contains(err.Error(), "is not a block device") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveFeatureFlags(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{
		"GPU":         "intel",
		"USB":         "0",
		"HYPERV":      "1",
		"BALLOON":     "0",
		"RNG":         "0",
		"IO_THREAD":   "0",
		"REQUIRE_KVM": "1",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.GPUPassthrough != "intel" {
		t.Fatalf("GPUPassthrough = %q", cfg.GPUPassthrough)
	}
	if cfg.USBController {
		t.Fatal("USBController = true")
	}
	if !cfg.HyperVEnabled {
		t.Fatal("HyperVEnabled = false")
	}
	if cfg.BalloonEnabled || cfg.RNGEnabled || cfg.IOThread {
		t.Fatalf("performance flags = balloon %v rng %v iothread %v", cfg.BalloonEnabled, cfg.RNGEnabled, cfg.IOThread)
	}
	if !cfg.RequireKVM {
		t.Fatal("RequireKVM = false")
	}
}

func TestResolveInvalidGPU(t *testing.T) {
	resolver, _ := testResolver(t)
	_, err := resolver.Resolve(MapEnv{"GPU": "nvidia"})
	if err == nil {
		t.Fatal("expected GPU error")
	}
}

func TestResolveShellAndRetries(t *testing.T) {
	resolver, _ := testResolver(t)
	cfg, err := resolver.Resolve(MapEnv{"SHELL": "/bin/zsh", "DOWNLOAD_RETRIES": "5"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cfg.LoginShell != "/bin/zsh" {
		t.Fatalf("LoginShell = %q", cfg.LoginShell)
	}
	if cfg.DownloadRetries != 5 {
		t.Fatalf("DownloadRetries = %d", cfg.DownloadRetries)
	}
}

func TestResolveInvalidRetries(t *testing.T) {
	resolver, _ := testResolver(t)
	_, err := resolver.Resolve(MapEnv{"DOWNLOAD_RETRIES": "99"})
	if err == nil {
		t.Fatal("expected retries error")
	}
}
