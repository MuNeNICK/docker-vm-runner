package firmware

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/munenick/docker-vm-runner/internal/config"
)

type Preparer struct {
	StateDir     string
	ExtractAAVMF func() error
}

type Request struct {
	Arch     string
	BootMode string
	VMName   string
	Profile  config.ArchitectureProfile
}

type Result struct {
	Needed     bool
	LoaderPath string
	VarsPath   string
}

func NewPreparer(stateDir string) *Preparer {
	return &Preparer{
		StateDir:     stateDir,
		ExtractAAVMF: func() error { return nil },
	}
}

func (p *Preparer) Prepare(req Request) (Result, error) {
	profile, needed, err := firmwareProfile(req)
	if err != nil {
		return Result{}, err
	}
	if !needed {
		return Result{}, nil
	}

	if req.Arch != "x86_64" && (!fileExists(profile.Loader) || !fileExists(profile.VarsTemplate)) {
		if p.ExtractAAVMF != nil {
			if err := p.ExtractAAVMF(); err != nil {
				return Result{}, fmt.Errorf("extract AAVMF firmware: %w", err)
			}
		}
	}

	if !fileExists(profile.Loader) {
		if req.Arch == "x86_64" {
			return Result{}, fmt.Errorf("OVMF firmware not found at %s", profile.Loader)
		}
		return Result{}, fmt.Errorf("firmware loader not found at %s for arch %s", profile.Loader, req.Arch)
	}
	if !fileExists(profile.VarsTemplate) {
		if req.Arch == "x86_64" {
			return Result{}, fmt.Errorf("OVMF variable template not found at %s", profile.VarsTemplate)
		}
		return Result{}, fmt.Errorf("firmware variable template not found at %s for arch %s", profile.VarsTemplate, req.Arch)
	}

	varsDestination := filepath.Join(p.StateDir, "firmware", req.VMName+"-vars.fd")
	if err := os.MkdirAll(filepath.Dir(varsDestination), 0o755); err != nil {
		return Result{}, fmt.Errorf("create firmware state directory: %w", err)
	}
	if !fileExists(varsDestination) {
		if err := copyFile(profile.VarsTemplate, varsDestination); err != nil {
			return Result{}, fmt.Errorf("copy firmware variable template: %w", err)
		}
	}
	return Result{
		Needed:     true,
		LoaderPath: profile.Loader,
		VarsPath:   varsDestination,
	}, nil
}

func firmwareProfile(req Request) (config.FirmwareProfile, bool, error) {
	if req.Arch == "x86_64" {
		if req.BootMode == "legacy" {
			return config.FirmwareProfile{}, false, nil
		}
		profile, ok := req.Profile.Firmware[req.BootMode]
		if !ok {
			return config.FirmwareProfile{}, false, fmt.Errorf("firmware configuration for boot_mode=%q not found for %s", req.BootMode, req.Arch)
		}
		return profile, true, nil
	}
	if len(req.Profile.Firmware) == 0 {
		return config.FirmwareProfile{}, false, nil
	}
	if profile, ok := req.Profile.Firmware["default"]; ok {
		return profile, true, nil
	}
	if profile, ok := req.Profile.Firmware[req.BootMode]; ok {
		return profile, true, nil
	}
	return config.FirmwareProfile{}, false, fmt.Errorf("firmware configuration not found for %s", req.Arch)
}

func AAVMFExtractCommand(debPath string, destination string) []string {
	return []string{"dpkg-deb", "-x", debPath, destination}
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(source string, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
