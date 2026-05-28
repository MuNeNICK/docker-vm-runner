package paths

import "path/filepath"

const DefaultConfigPath = "/config/os-iso-catalog/v1/supported.json"

type Layout struct {
	DataDir       string
	ImagesDir     string
	BaseImagesDir string
	VMImagesDir   string
	StateDir      string
}

func ResolveLayout(dataDir string, isMount func(string) bool) Layout {
	if dataDir == "" && isMount != nil && isMount("/data") {
		dataDir = "/data"
	}
	if dataDir != "" {
		return Layout{
			DataDir:       dataDir,
			ImagesDir:     dataDir,
			BaseImagesDir: filepath.Join(dataDir, "base"),
			VMImagesDir:   filepath.Join(dataDir, "vms"),
			StateDir:      filepath.Join(dataDir, "state"),
		}
	}
	return Layout{
		ImagesDir:     "/images",
		BaseImagesDir: filepath.Join("/images", "base"),
		VMImagesDir:   filepath.Join("/images", "vms"),
		StateDir:      "/var/lib/docker-vm-runner",
	}
}
