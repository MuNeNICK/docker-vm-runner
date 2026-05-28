package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/munenick/docker-vm-runner/internal/archive"
	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/console"
	"github.com/munenick/docker-vm-runner/internal/domain"
	"github.com/munenick/docker-vm-runner/internal/download"
	"github.com/munenick/docker-vm-runner/internal/firmware"
	"github.com/munenick/docker-vm-runner/internal/guestexec"
	"github.com/munenick/docker-vm-runner/internal/images"
	"github.com/munenick/docker-vm-runner/internal/libvirtmgr"
	"github.com/munenick/docker-vm-runner/internal/oci"
	"github.com/munenick/docker-vm-runner/internal/password"
	"github.com/munenick/docker-vm-runner/internal/paths"
	"github.com/munenick/docker-vm-runner/internal/process"
	"github.com/munenick/docker-vm-runner/internal/redfish"
	"github.com/munenick/docker-vm-runner/internal/seediso"
	"github.com/munenick/docker-vm-runner/internal/services"
	"github.com/munenick/docker-vm-runner/internal/tpm"
	"github.com/munenick/docker-vm-runner/internal/units"
	"github.com/munenick/docker-vm-runner/internal/vmstate"
	"github.com/munenick/docker-vm-runner/internal/vncproxy"
)

type ConcreteLifecycle struct {
	Layout         paths.Layout
	LibvirtURI     string
	RedfishPool    libvirtmgr.StoragePoolRequest
	CommandRunner  process.CommandRunner
	Manager        libvirtManager
	Domain         libvirtmgr.Domain
	Service        serviceSupervisor
	Redfish        redfishManager
	NoVNC          novncProxy
	TPM            tpmSupervisor
	Console        consoleRunner
	Sleep          func(context.Context, time.Duration) error
	EnsureEmulator func(context.Context, string) error

	workImagePath string
	seedISOPath   string
	bootISOPath   string
	firmware      firmware.Result
	tpmProcess    tpm.Process
}

type libvirtManager interface {
	EnsureDefined(string, string) (libvirtmgr.Domain, error)
	Start(libvirtmgr.Domain) error
	Cleanup(libvirtmgr.Domain, libvirtmgr.CleanupOptions) error
	EnsureStoragePool(libvirtmgr.StoragePoolRequest) (libvirtmgr.StoragePool, error)
	Close() error
}

type serviceSupervisor interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type redfishManager interface {
	Start(context.Context, redfish.Request) (redfish.Result, error)
}

type novncProxy interface {
	Start(context.Context, vncproxy.Request) (vncproxy.Result, error)
	Stop(context.Context) error
}

type tpmSupervisor interface {
	Start(context.Context, tpm.Request) (tpm.Result, error)
}

type consoleRunner interface {
	Run(context.Context, string) (int, error)
}

func NewConcreteLifecycle(layout paths.Layout) *ConcreteLifecycle {
	if layout.ImagesDir == "" {
		layout = paths.ResolveLayout("", nil)
	}
	runner := process.NewCommandRunner()
	lifecycle := &ConcreteLifecycle{
		Layout:        layout,
		LibvirtURI:    libvirtmgr.DefaultURI,
		RedfishPool:   libvirtmgr.StoragePoolRequest{Name: "default", TargetPath: "/var/lib/libvirt/images"},
		CommandRunner: *runner,
		Service:       services.NewSupervisor(services.Options{}),
		Redfish:       redfish.NewManager(redfish.Options{StateDir: layout.StateDir}),
		NoVNC:         vncproxy.New(vncproxy.Options{StateDir: layout.StateDir}),
		TPM:           tpm.NewSupervisor(layout.StateDir),
		Console:       console.NewRunner(),
		Sleep:         sleepContext,
	}
	lifecycle.EnsureEmulator = lifecycle.ensureEmulator
	return lifecycle
}

func (l *ConcreteLifecycle) StartServices(ctx context.Context, cfg config.VM) error {
	if l.Service != nil {
		if err := l.Service.Start(ctx); err != nil {
			return err
		}
	}
	if cfg.RedfishEnabled {
		poolReq := l.redfishPool()
		if err := os.MkdirAll(poolReq.TargetPath, 0o755); err != nil {
			return fmt.Errorf("create Redfish storage pool directory: %w", err)
		}
		manager := l.Manager
		if manager == nil {
			conn := libvirtmgr.NewVirshConnection(ctx, l.libvirtURI(), &l.CommandRunner)
			manager = libvirtmgr.New(conn)
			l.Manager = manager
		}
		if _, err := manager.EnsureStoragePool(poolReq); err != nil {
			return err
		}
		if l.Redfish != nil {
			if _, err := l.Redfish.Start(ctx, redfish.Request{
				Enabled:    true,
				User:       cfg.RedfishUser,
				Password:   cfg.RedfishPassword,
				Port:       cfg.RedfishPort,
				LibvirtURI: l.libvirtURI(),
			}); err != nil {
				return err
			}
		}
	}
	if cfg.NoVNCEnabled && l.NoVNC != nil {
		if _, err := l.NoVNC.Start(ctx, vncproxy.Request{Enabled: true, NoVNCPort: cfg.NoVNCPort, VNCPort: cfg.VNCPort}); err != nil {
			return err
		}
	}
	return nil
}

func (l *ConcreteLifecycle) Connect(ctx context.Context, _ config.VM) error {
	if l.Manager != nil {
		return nil
	}
	conn := libvirtmgr.NewVirshConnection(ctx, l.libvirtURI(), &l.CommandRunner)
	l.Manager = libvirtmgr.New(conn)
	return nil
}

func (l *ConcreteLifecycle) Prepare(ctx context.Context, cfg config.VM) error {
	if l.Manager == nil {
		return fmt.Errorf("libvirt manager is not connected")
	}
	vmDir := filepath.Join(l.Layout.VMImagesDir, cfg.VMName)
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	preparedCfg, err := l.applyPersistentState(vmDir, cfg)
	if err != nil {
		return err
	}
	cfg = preparedCfg
	l.workImagePath = filepath.Join(vmDir, "disk."+defaultString(cfg.ImageFormat, "qcow2"))
	if err := l.prepareDisk(ctx, cfg, l.workImagePath); err != nil {
		return err
	}
	if err := l.prepareExtraDisks(ctx, cfg, vmDir); err != nil {
		return err
	}
	if l.EnsureEmulator != nil {
		if err := l.EnsureEmulator(ctx, cfg.Arch); err != nil {
			return err
		}
	}
	firmwarePreparer := firmware.NewPreparer(l.Layout.StateDir)
	firmwarePreparer.ExtractAAVMF = func() error {
		command := firmware.AAVMFExtractCommand("/opt/aavmf.deb", "/")
		_, err := l.CommandRunner.Run(ctx, process.Command{Name: command[0], Args: command[1:]})
		return err
	}
	fw, err := firmwarePreparer.Prepare(firmware.Request{
		Arch:     cfg.Arch,
		BootMode: cfg.BootMode,
		VMName:   cfg.VMName,
		Profile:  config.SupportedArchitectures[cfg.Arch],
	})
	if err != nil {
		return err
	}
	l.firmware = fw
	if cfg.CloudInitEnabled {
		if err := l.prepareSeedISO(ctx, cfg, filepath.Join(vmDir, "seed.iso")); err != nil {
			return err
		}
	}
	if l.TPM != nil {
		result, err := l.TPM.Start(ctx, tpm.Request{Enabled: cfg.TPMEnabled, VMName: cfg.VMName})
		if err != nil {
			return err
		}
		l.tpmProcess = result.Process
	}
	xmlText, err := domain.NewRenderer().Render(domain.Request{
		VM:                cfg,
		VMDir:             vmDir,
		WorkImagePath:     l.workImagePath,
		SeedISOPath:       l.seedISOPath,
		BootISOPath:       l.bootISOPath,
		FirmwareLoader:    l.firmware.LoaderPath,
		FirmwareVars:      l.firmware.VarsPath,
		KVMAvailable:      fileExists("/dev/kvm"),
		EffectiveCPUModel: cfg.CPUModel,
	})
	if err != nil {
		return err
	}
	defined, err := l.Manager.EnsureDefined(cfg.VMName, xmlText)
	if err != nil {
		return err
	}
	l.Domain = defined
	return nil
}

func (l *ConcreteLifecycle) StartVM(_ context.Context, _ config.VM) error {
	return l.Manager.Start(l.Domain)
}

func (l *ConcreteLifecycle) WaitForGuestReady(ctx context.Context, _ config.VM) error {
	client := guestexec.NewVirshClient(&l.CommandRunner)
	return waitFor(ctx, l.Sleep, 120*time.Second, 2*time.Second, func() (bool, error) {
		_, err := client.Execute(ctx, l.Domain.Name(), guestexec.Command{Execute: "guest-ping"})
		if err == nil {
			return true, nil
		}
		if errors.Is(err, guestexec.ErrAgentNotConnected) {
			return false, nil
		}
		return false, err
	})
}

func (l *ConcreteLifecycle) WaitUntilStopped(ctx context.Context, _ config.VM) error {
	return waitFor(ctx, l.Sleep, 0, 2*time.Second, func() (bool, error) {
		if l.Domain == nil {
			return true, nil
		}
		active, err := l.Domain.IsActive()
		if err != nil {
			return false, err
		}
		return !active, nil
	})
}

func (l *ConcreteLifecycle) AttachConsole(ctx context.Context, cfg config.VM) (int, error) {
	if l.Console == nil {
		return 0, nil
	}
	return l.Console.Run(ctx, cfg.VMName)
}

func (l *ConcreteLifecycle) MarkInstalled(_ context.Context, cfg config.VM) error {
	vmDir := filepath.Join(l.Layout.VMImagesDir, cfg.VMName)
	return vmstate.MarkInstalled(vmDir, time.Now().UTC())
}

func (l *ConcreteLifecycle) Cleanup(ctx context.Context, cfg config.VM) error {
	if l.tpmProcess != nil {
		_ = l.tpmProcess.Stop()
	}
	if l.NoVNC != nil {
		_ = l.NoVNC.Stop(ctx)
	}
	var cleanupErr error
	if l.Manager != nil && l.Domain != nil {
		cleanupErr = l.Manager.Cleanup(l.Domain, libvirtmgr.CleanupOptions{HasNVRAM: l.firmware.VarsPath != ""})
	}
	if !cfg.Persist && cfg.VMName != "" {
		vmDir := filepath.Join(l.Layout.VMImagesDir, cfg.VMName)
		if err := os.RemoveAll(vmDir); err != nil && cleanupErr == nil {
			cleanupErr = fmt.Errorf("remove VM directory: %w", err)
		}
	}
	return cleanupErr
}

func (l *ConcreteLifecycle) Close(_ context.Context, _ config.VM) error {
	if l.Manager == nil {
		return nil
	}
	return l.Manager.Close()
}

func (l *ConcreteLifecycle) StopServices(ctx context.Context, _ config.VM) error {
	if l.Service == nil {
		return nil
	}
	return l.Service.Stop(ctx)
}

func (l *ConcreteLifecycle) applyPersistentState(vmDir string, cfg config.VM) (config.VM, error) {
	if !cfg.Persist || cfg.ForceISO || cfg.BootFrom == "" {
		return cfg, nil
	}
	state, err := vmstate.Read(vmDir)
	if err != nil {
		return config.VM{}, err
	}
	if !vmstate.IsInstalled(state) {
		return cfg, nil
	}
	cfg.BootFrom = ""
	cfg.BlankWorkDisk = false
	cfg.BootOrder = withoutBootDevice(cfg.BootOrder, "cdrom")
	if len(cfg.BootOrder) == 0 {
		cfg.BootOrder = []string{"hd"}
	}
	return cfg, nil
}

func (l *ConcreteLifecycle) prepareDisk(ctx context.Context, cfg config.VM, workImage string) error {
	diskManager := images.NewDiskManager(&l.CommandRunner)
	if fileExists(workImage) && cfg.Persist {
		if cfg.BootFrom != "" {
			source, err := l.resolveBootSource(ctx, cfg.BootFrom, cfg.DownloadRetries)
			if err != nil {
				return err
			}
			if isISO(source) {
				l.bootISOPath = source
			}
		}
		return l.resizeDiskIfNeeded(ctx, diskManager, workImage, cfg.DiskSize)
	}
	if cfg.BlankWorkDisk {
		if cfg.BootFrom != "" {
			source, err := l.resolveBootSource(ctx, cfg.BootFrom, cfg.DownloadRetries)
			if err != nil {
				return err
			}
			if isISO(source) {
				l.bootISOPath = source
			}
		}
		return diskManager.CreateDisk(ctx, images.CreateDiskRequest{
			Path:        workImage,
			Format:      defaultString(cfg.ImageFormat, "qcow2"),
			Size:        cfg.DiskSize,
			Preallocate: cfg.DiskPreallocate,
		})
	}
	baseImage, err := l.resolveBaseImage(ctx, cfg)
	if err != nil {
		return err
	}
	if l.bootISOPath != "" {
		return images.NewDiskManager(&l.CommandRunner).CreateDisk(ctx, images.CreateDiskRequest{
			Path:        workImage,
			Format:      defaultString(cfg.ImageFormat, "qcow2"),
			Size:        cfg.DiskSize,
			Preallocate: cfg.DiskPreallocate,
		})
	}
	if !fileExists(baseImage) {
		return fmt.Errorf("base image not found at %s", baseImage)
	}
	if err := os.MkdirAll(filepath.Dir(workImage), 0o755); err != nil {
		return fmt.Errorf("create work image directory: %w", err)
	}
	if err := copyFile(baseImage, workImage); err != nil {
		return fmt.Errorf("copy base image to work image: %w", err)
	}
	return l.resizeDiskIfNeeded(ctx, diskManager, workImage, cfg.DiskSize)
}

func (l *ConcreteLifecycle) resizeDiskIfNeeded(ctx context.Context, diskManager *images.DiskManager, path string, size string) error {
	if size == "" {
		return nil
	}
	desired, err := units.ParseSizeBytes(size)
	if err != nil {
		return err
	}
	info, err := diskManager.ImageInfo(ctx, path)
	if err == nil && info.VirtualSize >= desired {
		return nil
	}
	if err := diskManager.ResizeDisk(ctx, path, size); err != nil {
		return err
	}
	return nil
}

func (l *ConcreteLifecycle) prepareExtraDisks(ctx context.Context, cfg config.VM, vmDir string) error {
	if len(cfg.ExtraDisks) == 0 {
		return nil
	}
	diskManager := images.NewDiskManager(&l.CommandRunner)
	format := defaultString(cfg.ImageFormat, "qcow2")
	for _, disk := range cfg.ExtraDisks {
		path := filepath.Join(vmDir, fmt.Sprintf("disk%d.%s", disk.Index, format))
		if fileExists(path) {
			if cfg.Persist {
				continue
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("replace extra disk %s: %w", path, err)
			}
		}
		if err := diskManager.CreateDisk(ctx, images.CreateDiskRequest{
			Path:        path,
			Format:      format,
			Size:        disk.Size,
			Preallocate: cfg.DiskPreallocate,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (l *ConcreteLifecycle) ensureEmulator(ctx context.Context, arch string) error {
	binary, deb, ok := emulatorPackage(arch)
	if !ok {
		return nil
	}
	if fileExists(binary) {
		return nil
	}
	if !fileExists(deb) {
		return fmt.Errorf("QEMU emulator %s not found and package cache missing: %s", binary, deb)
	}
	if _, err := l.CommandRunner.Run(ctx, process.Command{Name: "dpkg-deb", Args: []string{"-x", deb, "/"}}); err != nil {
		return fmt.Errorf("extract QEMU emulator package %s: %w", deb, err)
	}
	if !fileExists(binary) {
		return fmt.Errorf("QEMU emulator %s still missing after extracting %s", binary, deb)
	}
	return nil
}

func emulatorPackage(arch string) (string, string, bool) {
	switch arch {
	case "x86_64":
		return "/usr/bin/qemu-system-x86_64", "/opt/qemu-x86.deb", true
	case "aarch64":
		return "/usr/bin/qemu-system-aarch64", "/opt/qemu-arm.deb", true
	case "ppc64":
		return "/usr/bin/qemu-system-ppc64", "/opt/qemu-ppc.deb", true
	case "s390x":
		return "/usr/bin/qemu-system-s390x", "/opt/qemu-s390x.deb", true
	case "riscv64":
		return "/usr/bin/qemu-system-riscv64", "/opt/qemu-riscv.deb", true
	default:
		return "", "", false
	}
}

func (l *ConcreteLifecycle) resolveBaseImage(ctx context.Context, cfg config.VM) (string, error) {
	if cfg.BootFrom != "" {
		source, err := l.resolveBootSource(ctx, cfg.BootFrom, cfg.DownloadRetries)
		if err != nil {
			return "", err
		}
		if isISO(source) {
			l.bootISOPath = source
			return "", nil
		}
		return l.postProcessImage(ctx, source, filepath.Join(l.Layout.BaseImagesDir, cfg.Distro+"."+defaultString(cfg.ImageFormat, "qcow2")), defaultString(cfg.ImageFormat, "qcow2"))
	}
	baseImage := filepath.Join(l.Layout.BaseImagesDir, cfg.Distro+"."+defaultString(cfg.ImageFormat, "qcow2"))
	if fileExists(baseImage) {
		return baseImage, nil
	}
	if cfg.ImageURL == "" {
		return "", fmt.Errorf("image URL is empty and base image is missing: %s", baseImage)
	}
	downloadPath := filepath.Join(l.Layout.BaseImagesDir, "downloads", cacheName(cfg.ImageURL))
	if err := download.NewDownloader(nil).DownloadWithRetry(ctx, cfg.ImageURL, downloadPath, cfg.DownloadRetries); err != nil {
		return "", err
	}
	return l.postProcessImage(ctx, downloadPath, baseImage, defaultString(cfg.ImageFormat, "qcow2"))
}

func (l *ConcreteLifecycle) resolveBootSource(ctx context.Context, ref string, retries int) (string, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		destination := filepath.Join(l.Layout.BaseImagesDir, "boot", cacheName(ref))
		if fileExists(destination) {
			return destination, nil
		}
		if err := download.NewDownloader(nil).DownloadWithRetry(ctx, ref, destination, retries); err != nil {
			return "", err
		}
		return destination, nil
	}
	if oci.IsReference(ref) {
		result, err := oci.NewPuller().Pull(ctx, ref, filepath.Join(l.Layout.BaseImagesDir, "oci"))
		if err != nil {
			return "", err
		}
		return result.Path, nil
	}
	if !fileExists(ref) {
		return "", fmt.Errorf("BOOT_FROM path not found: %s", ref)
	}
	return ref, nil
}

func (l *ConcreteLifecycle) postProcessImage(ctx context.Context, source string, destination string, desiredFormat string) (string, error) {
	current := source
	extractor := archive.NewExtractor()
	for isArchive(current) {
		result, err := extractor.ExtractWithResult(ctx, current, filepath.Dir(current))
		if err != nil {
			return "", err
		}
		current = result.Path
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", fmt.Errorf("create base image directory: %w", err)
	}
	diskManager := images.NewDiskManager(&l.CommandRunner)
	info, err := diskManager.ImageInfo(ctx, current)
	if err == nil && info.Format != "" && info.Format != desiredFormat {
		if err := diskManager.ConvertDisk(ctx, current, destination, desiredFormat); err != nil {
			return "", err
		}
		return destination, nil
	}
	if current != destination {
		if err := copyFile(current, destination); err != nil {
			return "", fmt.Errorf("place base image: %w", err)
		}
	}
	return destination, nil
}

func (l *ConcreteLifecycle) prepareSeedISO(ctx context.Context, cfg config.VM, outputPath string) error {
	userData := ""
	if cfg.CloudInitUserDataPath != "" {
		content, err := os.ReadFile(cfg.CloudInitUserDataPath)
		if err != nil {
			return fmt.Errorf("read cloud-init user-data: %w", err)
		}
		userData = string(content)
	}
	content, err := seediso.BuildCloudInit(seediso.CloudInitRequest{
		VMName:       cfg.VMName,
		LoginUser:    cfg.LoginUser,
		LoginShell:   cfg.LoginShell,
		Password:     cfg.Password,
		PasswordHash: password.SHA512Crypt,
		SSHPubKey:    cfg.SSHPubkey,
		UserData:     userData,
	})
	if err != nil {
		return err
	}
	if err := seediso.NewBuilder(&l.CommandRunner).Build(ctx, outputPath, content); err != nil {
		return err
	}
	l.seedISOPath = outputPath
	return nil
}

func (l *ConcreteLifecycle) libvirtURI() string {
	if l.LibvirtURI == "" {
		return libvirtmgr.DefaultURI
	}
	return l.LibvirtURI
}

func (l *ConcreteLifecycle) redfishPool() libvirtmgr.StoragePoolRequest {
	if l.RedfishPool.Name == "" {
		l.RedfishPool.Name = "default"
	}
	if l.RedfishPool.TargetPath == "" {
		l.RedfishPool.TargetPath = "/var/lib/libvirt/images"
	}
	return l.RedfishPool
}

func waitFor(ctx context.Context, sleep func(context.Context, time.Duration) error, timeout time.Duration, interval time.Duration, check func() (bool, error)) error {
	if sleep == nil {
		sleep = sleepContext
	}
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for VM state")
		}
		if err := sleep(ctx, interval); err != nil {
			return err
		}
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isISO(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".iso")
}

func isArchive(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gz", ".xz", ".bz2", ".zip", ".tar", ".ova", ".7z", ".rar":
		return true
	default:
		return false
	}
}

func cacheName(value string) string {
	sum := sha256.Sum256([]byte(value))
	prefix := hex.EncodeToString(sum[:])[:12]
	name := filepath.Base(value)
	if parsed, err := url.Parse(value); err == nil && parsed.Path != "" {
		name = filepath.Base(parsed.Path)
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		name = "image"
	}
	return prefix + "-" + safeFilename(name)
}

func safeFilename(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func copyFile(source string, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Close()
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func withoutBootDevice(values []string, device string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value != device {
			filtered = append(filtered, value)
		}
	}
	return filtered
}
