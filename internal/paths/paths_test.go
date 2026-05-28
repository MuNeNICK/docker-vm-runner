package paths

import (
	"path/filepath"
	"testing"
)

func TestDefaultConfigPath(t *testing.T) {
	if DefaultConfigPath != "/config/os-iso-catalog/v1/all.json" {
		t.Fatalf("DefaultConfigPath = %q", DefaultConfigPath)
	}
}

func TestResolveLayoutWithoutDataDir(t *testing.T) {
	layout := ResolveLayout("", func(string) bool { return false })
	if layout.ImagesDir != "/images" {
		t.Fatalf("ImagesDir = %q", layout.ImagesDir)
	}
	if layout.BaseImagesDir != filepath.Join("/images", "base") {
		t.Fatalf("BaseImagesDir = %q", layout.BaseImagesDir)
	}
	if layout.StateDir != "/var/lib/docker-vm-runner" {
		t.Fatalf("StateDir = %q", layout.StateDir)
	}
	if layout.DataDir != "" {
		t.Fatalf("DataDir = %q", layout.DataDir)
	}
}

func TestResolveLayoutWithExplicitDataDir(t *testing.T) {
	layout := ResolveLayout("/data", func(string) bool { return false })
	if layout.DataDir != "/data" {
		t.Fatalf("DataDir = %q", layout.DataDir)
	}
	if layout.ImagesDir != "/data" {
		t.Fatalf("ImagesDir = %q", layout.ImagesDir)
	}
	if layout.StateDir != filepath.Join("/data", "state") {
		t.Fatalf("StateDir = %q", layout.StateDir)
	}
}

func TestResolveLayoutAutoDetectsDataMount(t *testing.T) {
	layout := ResolveLayout("", func(path string) bool { return path == "/data" })
	if layout.DataDir != "/data" {
		t.Fatalf("DataDir = %q", layout.DataDir)
	}
}
