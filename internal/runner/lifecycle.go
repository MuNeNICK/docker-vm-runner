package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/console"
	"github.com/munenick/docker-vm-runner/internal/domain"
	"github.com/munenick/docker-vm-runner/internal/firmware"
	"github.com/munenick/docker-vm-runner/internal/guestexec"
	"github.com/munenick/docker-vm-runner/internal/images"
	"github.com/munenick/docker-vm-runner/internal/libvirtmgr"
	"github.com/munenick/docker-vm-runner/internal/password"
	"github.com/munenick/docker-vm-runner/internal/paths"
	"github.com/munenick/docker-vm-runner/internal/process"
	"github.com/munenick/docker-vm-runner/internal/redfish"
	"github.com/munenick/docker-vm-runner/internal/seediso"
	"github.com/munenick/docker-vm-runner/internal/services"
	"github.com/munenick/docker-vm-runner/internal/tpm"
	"github.com/munenick/docker-vm-runner/internal/vncproxy"
)

type ConcreteLifecycle struct {
	Layout        paths.Layout
	LibvirtURI    string
	RedfishPool   libvirtmgr.StoragePoolRequest
	CommandRunner process.CommandRunner
	Manager       libvirtManager
	Domain        libvirtmgr.Domain
	Service       serviceSupervisor
	Redfish       redfishManager
	NoVNC         novncProxy
	TPM           tpmSupervisor
	Console       consoleRunner
	Sleep         func(context.Context, time.Duration) error

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
	return &ConcreteLifecycle{
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
	l.workImagePath = filepath.Join(vmDir, "disk."+defaultString(cfg.ImageFormat, "qcow2"))
	if err := l.prepareDisk(ctx, cfg, l.workImagePath); err != nil {
		return err
	}
	fw, err := firmware.NewPreparer(l.Layout.StateDir).Prepare(firmware.Request{
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
	return os.WriteFile(filepath.Join(vmDir, ".installed"), []byte("ok\n"), 0o644)
}

func (l *ConcreteLifecycle) Cleanup(ctx context.Context, _ config.VM) error {
	if l.tpmProcess != nil {
		_ = l.tpmProcess.Stop()
	}
	if l.NoVNC != nil {
		_ = l.NoVNC.Stop(ctx)
	}
	if l.Manager == nil || l.Domain == nil {
		return nil
	}
	return l.Manager.Cleanup(l.Domain, libvirtmgr.CleanupOptions{HasNVRAM: l.firmware.VarsPath != ""})
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

func (l *ConcreteLifecycle) prepareDisk(ctx context.Context, cfg config.VM, workImage string) error {
	if cfg.BlankWorkDisk {
		return images.NewDiskManager(&l.CommandRunner).CreateDisk(ctx, images.CreateDiskRequest{
			Path:        workImage,
			Format:      defaultString(cfg.ImageFormat, "qcow2"),
			Size:        cfg.DiskSize,
			Preallocate: cfg.DiskPreallocate,
		})
	}
	if fileExists(workImage) && cfg.Persist {
		return nil
	}
	baseImage := filepath.Join(l.Layout.BaseImagesDir, cfg.Distro+"."+defaultString(cfg.ImageFormat, "qcow2"))
	if !fileExists(baseImage) {
		return fmt.Errorf("base image not found at %s; image download pipeline is not wired into lifecycle yet", baseImage)
	}
	if err := os.MkdirAll(filepath.Dir(workImage), 0o755); err != nil {
		return fmt.Errorf("create work image directory: %w", err)
	}
	input, err := os.ReadFile(baseImage)
	if err != nil {
		return fmt.Errorf("read base image: %w", err)
	}
	if err := os.WriteFile(workImage, input, 0o644); err != nil {
		return fmt.Errorf("write work image: %w", err)
	}
	return nil
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

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
