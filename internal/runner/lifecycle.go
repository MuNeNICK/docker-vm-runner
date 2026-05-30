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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/munenick/docker-vm-runner/internal/archive"
	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/console"
	"github.com/munenick/docker-vm-runner/internal/domain"
	"github.com/munenick/docker-vm-runner/internal/download"
	"github.com/munenick/docker-vm-runner/internal/firmware"
	"github.com/munenick/docker-vm-runner/internal/guestexec"
	"github.com/munenick/docker-vm-runner/internal/hostinfo"
	"github.com/munenick/docker-vm-runner/internal/images"
	"github.com/munenick/docker-vm-runner/internal/ipmi"
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
	"github.com/munenick/docker-vm-runner/internal/vmname"
	"github.com/munenick/docker-vm-runner/internal/vmstate"
	"github.com/munenick/docker-vm-runner/internal/vncproxy"
)

type ConcreteLifecycle struct {
	Layout          paths.Layout
	LibvirtURI      string
	RedfishPool     libvirtmgr.StoragePoolRequest
	CommandRunner   process.CommandRunner
	Manager         libvirtManager
	Domain          libvirtmgr.Domain
	Service         serviceSupervisor
	Redfish         redfishManager
	IPMI            ipmiManager
	NoVNC           novncProxy
	TPM             tpmSupervisor
	Console         consoleRunner
	GuestClient     guestexec.Client
	Sleep           func(context.Context, time.Duration) error
	EnsureEmulator  func(context.Context, string) error
	KVMAvailable    func() bool
	BlockSectorSize func(string) (int, bool)
	IPv6Available   func() bool
	CPUVendor       func() string
	CPUFlags        func() map[string]bool
	TerminalSize    func() (int, int, bool)
	Notify          func(chan<- os.Signal, ...os.Signal)
	StopNotify      func(chan<- os.Signal)
	Status          io.Writer
	Output          *Output

	workImagePath  string
	seedISOPath    string
	bootISOPath    string
	vmDir          string
	currentConfig  config.VM
	disablePasst   bool
	firmware       firmware.Result
	redfishProcess redfish.Process
	ipmiResult     ipmi.Result
}

type libvirtManager interface {
	ReconcileStaleDomain(string) error
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

type ipmiManager interface {
	Start(context.Context, ipmi.Request) (ipmi.Result, error)
	Stop(context.Context, ipmi.Result) error
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
		IPMI:          ipmi.NewManager(ipmi.Options{StateDir: layout.StateDir}),
		NoVNC:         vncproxy.New(vncproxy.Options{StateDir: layout.StateDir}),
		TPM:           tpm.NewSupervisor(layout.StateDir),
		Console:       console.NewRunner(),
		Sleep:         sleepContext,
		TerminalSize:  currentTerminalSize,
		Notify:        signal.Notify,
		StopNotify:    signal.Stop,
	}
	lifecycle.EnsureEmulator = lifecycle.ensureEmulator
	return lifecycle
}

func (l *ConcreteLifecycle) StartServices(ctx context.Context, cfg config.VM) (err error) {
	if err := l.StartCleanupServices(ctx, cfg); err != nil {
		return err
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
			l.warnf("Redfish storage pool %q could not be ensured: %v", poolReq.Name, err)
		}
		if l.Redfish != nil {
			result, err := l.Redfish.Start(ctx, redfish.Request{
				Enabled:    true,
				User:       cfg.RedfishUser,
				Password:   cfg.RedfishPassword,
				Port:       cfg.RedfishPort,
				SystemID:   cfg.RedfishSystemID,
				LibvirtURI: l.libvirtURI(),
			})
			if err != nil {
				return err
			}
			l.redfishProcess = result.Process
		}
	}
	if cfg.IPMIEnabled && l.IPMI != nil {
		result, err := l.IPMI.Start(ctx, ipmi.Request{
			Enabled:    true,
			User:       cfg.IPMIUser,
			Password:   cfg.IPMIPassword,
			Port:       cfg.IPMIPort,
			SystemID:   cfg.IPMISystemID,
			LibvirtURI: l.libvirtURI(),
		})
		if err != nil {
			return err
		}
		l.ipmiResult = result
	}
	if cfg.NoVNCEnabled && l.NoVNC != nil {
		if _, err := l.NoVNC.Start(ctx, vncproxy.Request{Enabled: true, NoVNCPort: cfg.NoVNCPort, VNCPort: cfg.VNCPort}); err != nil {
			return err
		}
	}
	return nil
}

func (l *ConcreteLifecycle) StartCleanupServices(ctx context.Context, cfg config.VM) (err error) {
	started := false
	defer func() {
		if err != nil && started {
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = l.StopServices(stopCtx, cfg)
		}
	}()
	if l.Service != nil {
		if err := l.Service.Start(ctx); err != nil {
			return err
		}
		started = true
	}
	return nil
}

func (l *ConcreteLifecycle) warnf(format string, args ...interface{}) {
	if l.Output != nil {
		l.Output.Warn(fmt.Sprintf(format, args...))
		return
	}
	if l.Status != nil {
		fmt.Fprintf(l.Status, "[WARN] "+format+"\n", args...)
	}
}

func (l *ConcreteLifecycle) infof(format string, args ...interface{}) {
	if l.Output != nil {
		l.Output.Info(fmt.Sprintf(format, args...))
		return
	}
	if l.Status != nil {
		fmt.Fprintf(l.Status, "[INFO] "+format+"\n", args...)
	}
}

func (l *ConcreteLifecycle) diskManager() *images.DiskManager {
	manager := images.NewDiskManager(&l.CommandRunner)
	if l.Output != nil {
		manager.Progress = l.Output.Stderr
	} else {
		manager.Progress = l.Status
	}
	return manager
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
	kvmAvailable := l.kvmAvailable()
	if !kvmAvailable {
		l.warnf("/dev/kvm is not available; running in software emulation mode (TCG). Performance will be 10-50x slower. Add --device /dev/kvm:/dev/kvm to enable KVM.")
		if model := strings.ToLower(strings.TrimSpace(cfg.CPUModel)); model == "host" || model == "host-passthrough" {
			if profile, ok := config.SupportedArchitectures[cfg.Arch]; ok && profile.TCGFallback != "" {
				l.warnf("CPU_MODEL=%s is not compatible with TCG on %s; using %s instead", cfg.CPUModel, cfg.Arch, profile.TCGFallback)
			}
		}
	}
	if cfg.RequireKVM && !kvmAvailable {
		return fmt.Errorf("REQUIRE_KVM=1 requires /dev/kvm")
	}
	vmDir, err := l.vmDirFor(cfg.VMName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	if err := l.Manager.ReconcileStaleDomain(cfg.VMName); err != nil {
		return err
	}
	if !cfg.Persist {
		if err := l.rejectLocalBootSourceInVMDir(cfg, vmDir); err != nil {
			return err
		}
		if err := os.RemoveAll(vmDir); err != nil {
			return fmt.Errorf("remove stale non-persistent VM directory: %w", err)
		}
		if err := os.MkdirAll(vmDir, 0o755); err != nil {
			return fmt.Errorf("create VM directory: %w", err)
		}
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
		if _, err := l.TPM.Start(ctx, tpm.Request{Enabled: cfg.TPMEnabled, VMName: cfg.VMName}); err != nil {
			return err
		}
	}
	l.vmDir = vmDir
	l.currentConfig = cfg
	l.disablePasst = false
	return l.defineDomain(ctx, cfg, vmDir)
}

func (l *ConcreteLifecycle) defineDomain(ctx context.Context, cfg config.VM, vmDir string) error {
	kvmAvailable := l.kvmAvailable()
	xmlText, err := domain.NewRenderer().Render(domain.Request{
		VM:                cfg,
		VMDir:             vmDir,
		WorkImagePath:     l.workImagePath,
		SeedISOPath:       l.seedISOPath,
		BootISOPath:       l.bootISOPath,
		FirmwareLoader:    l.firmware.LoaderPath,
		FirmwareVars:      l.firmware.VarsPath,
		IPXEROMPath:       cfg.IPXEROMPath,
		KVMAvailable:      kvmAvailable,
		EffectiveCPUModel: cfg.CPUModel,
		IPv6Enabled:       l.ipv6Available(),
		IntelRenderNode:   fileExists("/dev/dri/renderD128"),
		DisablePasst:      l.disablePasst,
		HostCPUVendor:     l.cpuVendor(),
		HostCPUFlags:      l.cpuFlags(),
		BlockSectorSize:   l.blockSectorSize,
		NativeIOUnsafe:    hostinfo.NativeDiskIOUnsafe(l.workImagePath),
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

func (l *ConcreteLifecycle) StartVM(ctx context.Context, cfg config.VM) error {
	err := l.Manager.Start(l.Domain)
	if err == nil || l.disablePasst || !isPasstStartError(err) {
		return err
	}
	l.disablePasst = true
	if l.Domain != nil {
		if cleanupErr := l.Manager.Cleanup(l.Domain, libvirtmgr.CleanupOptions{HasNVRAM: l.firmware.VarsPath != "", HasTPM: cfg.TPMEnabled}); cleanupErr != nil {
			return cleanupErr
		}
	}
	if l.vmDir == "" {
		return err
	}
	if l.currentConfig.VMName == "" {
		l.currentConfig = cfg
	}
	if defineErr := l.defineDomain(ctx, l.currentConfig, l.vmDir); defineErr != nil {
		return defineErr
	}
	return l.Manager.Start(l.Domain)
}

func (l *ConcreteLifecycle) WaitForGuestReady(ctx context.Context, cfg config.VM) error {
	client := l.guestClient()
	domainName := l.Domain.Name()
	return waitFor(ctx, l.Sleep, 120*time.Second, 2*time.Second, func() (bool, error) {
		if _, err := client.Execute(ctx, domainName, guestexec.Command{Execute: "guest-ping"}); err != nil {
			if errors.Is(err, guestexec.ErrAgentNotConnected) {
				return false, nil
			}
			return false, err
		}
		if !cfg.CloudInitEnabled {
			return true, nil
		}
		if err := l.waitForCloudInit(ctx, client, domainName); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (l *ConcreteLifecycle) waitForCloudInit(ctx context.Context, client guestexec.Client, domainName string) error {
	executor := guestexec.NewExecutor(client)
	executor.Sleep = l.Sleep
	result, err := executor.RunOnDomain(ctx, domainName, guestexec.Invocation{
		Path: "/bin/sh",
		Args: []string{"-c", "cloud-init status --wait"},
	})
	if err != nil {
		return fmt.Errorf("wait for cloud-init: %w", err)
	}
	if result.ExitCode != 0 {
		output := strings.TrimSpace(string(result.Stderr))
		if output == "" {
			output = strings.TrimSpace(string(result.Stdout))
		}
		if output != "" {
			return fmt.Errorf("cloud-init status --wait exited with status %d: %s", result.ExitCode, output)
		}
		return fmt.Errorf("cloud-init status --wait exited with status %d", result.ExitCode)
	}
	return nil
}

func (l *ConcreteLifecycle) WaitUntilStopped(ctx context.Context, _ config.VM) error {
	return waitFor(ctx, l.Sleep, 0, 2*time.Second, func() (bool, error) {
		return l.domainStopped()
	})
}

func (l *ConcreteLifecycle) DomainStopped(context.Context, config.VM) (bool, error) {
	return l.domainStopped()
}

func (l *ConcreteLifecycle) domainStopped() (bool, error) {
	if l.Domain == nil {
		return true, nil
	}
	active, err := l.Domain.IsActive()
	if err != nil {
		if errors.Is(err, libvirtmgr.ErrNotFound) {
			return true, nil
		}
		return false, err
	}
	return !active, nil
}

func (l *ConcreteLifecycle) AttachConsole(ctx context.Context, cfg config.VM) (int, error) {
	if l.Console == nil {
		return 0, nil
	}
	if runner, ok := l.Console.(*console.Runner); ok {
		runner.LibvirtURI = l.libvirtURI()
	}
	stopResize := l.startConsoleResizeSync(ctx, cfg)
	defer stopResize()
	return l.Console.Run(ctx, cfg.VMName)
}

func (l *ConcreteLifecycle) startConsoleResizeSync(ctx context.Context, cfg config.VM) func() {
	signals := make(chan os.Signal, 1)
	notify := l.Notify
	if notify == nil {
		notify = signal.Notify
	}
	stopNotify := l.StopNotify
	if stopNotify == nil {
		stopNotify = signal.Stop
	}
	notify(signals, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		l.syncConsoleSize(ctx, cfg, false)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-signals:
				l.syncConsoleSize(ctx, cfg, true)
			}
		}
	}()
	return func() {
		stopNotify(signals)
		close(done)
	}
}

func (l *ConcreteLifecycle) syncConsoleSize(ctx context.Context, cfg config.VM, warn bool) {
	rows, cols, ok := l.consoleTerminalSize()
	if !ok {
		return
	}
	client := l.guestClient()
	executor := guestexec.NewExecutor(client)
	executor.Sleep = l.Sleep
	executor.AgentWaitTimeout = 5 * time.Second
	executor.AgentWaitInterval = 500 * time.Millisecond
	executor.PollTimeout = 5 * time.Second
	command := fmt.Sprintf(
		`for tty in /dev/hvc0 /dev/ttyS0; do [ -e "$tty" ] && stty -F "$tty" rows %s cols %s 2>/dev/null; done`,
		strconv.Itoa(rows),
		strconv.Itoa(cols),
	)
	result, err := executor.RunOnDomain(ctx, cfg.VMName, guestexec.Invocation{
		Wait: true,
		Path: "sh",
		Args: []string{"-c", command},
	})
	if err != nil {
		if warn {
			l.warnf("Could not sync console terminal size: %v", err)
		}
		return
	}
	if warn && result.ExitCode != 0 {
		l.warnf("Could not sync console terminal size: stty exited with status %d", result.ExitCode)
	}
}

func (l *ConcreteLifecycle) consoleTerminalSize() (int, int, bool) {
	if l.TerminalSize != nil {
		return l.TerminalSize()
	}
	return currentTerminalSize()
}

func currentTerminalSize() (int, int, bool) {
	type winsize struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.Row == 0 || ws.Col == 0 {
		return 0, 0, false
	}
	return int(ws.Row), int(ws.Col), true
}

func (l *ConcreteLifecycle) MarkInstalled(_ context.Context, cfg config.VM) error {
	vmDir, err := l.vmDirFor(cfg.VMName)
	if err != nil {
		return err
	}
	return vmstate.MarkInstalled(vmDir, time.Now().UTC())
}

func (l *ConcreteLifecycle) Cleanup(ctx context.Context, cfg config.VM) error {
	if l.NoVNC != nil {
		_ = l.NoVNC.Stop(ctx)
	}
	var cleanupErr error
	if l.Manager != nil && l.Domain != nil {
		if setter, ok := l.Manager.(interface{ UseContext(context.Context) }); ok {
			setter.UseContext(ctx)
		}
		cleanupErr = l.Manager.Cleanup(l.Domain, libvirtmgr.CleanupOptions{
			HasNVRAM:        l.firmware.VarsPath != "",
			HasTPM:          cfg.TPMEnabled,
			Context:         ctx,
			ShutdownTimeout: 20 * time.Second,
			ShutdownPoll:    time.Second,
		})
		if errors.Is(cleanupErr, libvirtmgr.ErrNotFound) {
			cleanupErr = nil
		}
	}
	if !cfg.Persist && cfg.VMName != "" {
		if cleanupErr != nil {
			return cleanupErr
		}
		vmDir, err := l.vmDirFor(cfg.VMName)
		if err != nil {
			if cleanupErr == nil {
				cleanupErr = err
			}
			return cleanupErr
		}
		if err := os.RemoveAll(vmDir); err != nil && cleanupErr == nil {
			cleanupErr = fmt.Errorf("remove VM directory: %w", err)
		}
	}
	return cleanupErr
}

func (l *ConcreteLifecycle) CleanupStale(_ context.Context, cfg config.VM) error {
	if l.Manager == nil {
		return fmt.Errorf("libvirt manager is not connected")
	}
	if cfg.VMName != "" {
		if err := l.Manager.ReconcileStaleDomain(cfg.VMName); err != nil {
			return err
		}
	}
	if !cfg.Persist && cfg.VMName != "" {
		vmDir, err := l.vmDirFor(cfg.VMName)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(vmDir); err != nil {
			return fmt.Errorf("remove stale non-persistent VM directory: %w", err)
		}
	}
	return nil
}

func (l *ConcreteLifecycle) Close(_ context.Context, _ config.VM) error {
	if l.Manager == nil {
		return nil
	}
	return l.Manager.Close()
}

func (l *ConcreteLifecycle) StopServices(ctx context.Context, _ config.VM) error {
	if l.redfishProcess != nil {
		if err := l.redfishProcess.Stop(); err != nil {
			return err
		}
		l.redfishProcess = nil
	}
	if l.ipmiResult.Started && l.IPMI != nil {
		if err := l.IPMI.Stop(ctx, l.ipmiResult); err != nil {
			return err
		}
		l.ipmiResult = ipmi.Result{}
	}
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

func (l *ConcreteLifecycle) vmDirFor(name string) (string, error) {
	if err := vmname.Validate(name); err != nil {
		return "", fmt.Errorf("invalid VM name %q: %w", name, err)
	}
	return filepath.Join(l.Layout.VMImagesDir, name), nil
}

func (l *ConcreteLifecycle) shouldCreateBackingOverlay(cfg config.VM, baseImage string, workImage string) bool {
	if filepath.Clean(baseImage) == filepath.Clean(workImage) {
		return false
	}
	if cfg.BootFrom != "" {
		return true
	}
	if pathWithin(l.Layout.VMImagesDir, baseImage) {
		return false
	}
	if pathWithin(l.Layout.BaseImagesDir, baseImage) {
		first := firstPathElement(l.Layout.BaseImagesDir, baseImage)
		return first == "boot" || first == "oci"
	}
	return true
}

func (l *ConcreteLifecycle) rejectLocalBootSourceInVMDir(cfg config.VM, vmDir string) error {
	bootFrom := strings.TrimSpace(cfg.BootFrom)
	if bootFrom == "" || isRemoteReference(bootFrom) || oci.IsReference(bootFrom) {
		return nil
	}
	sourcePath, err := filepath.Abs(bootFrom)
	if err != nil {
		return fmt.Errorf("resolve BOOT_FROM path: %w", err)
	}
	if evaluated, err := filepath.EvalSymlinks(sourcePath); err == nil {
		sourcePath = evaluated
	}
	vmPath, err := filepath.Abs(vmDir)
	if err != nil {
		return fmt.Errorf("resolve VM directory: %w", err)
	}
	if evaluated, err := filepath.EvalSymlinks(vmPath); err == nil {
		vmPath = evaluated
	}
	if pathWithinOrEqual(vmPath, sourcePath) {
		return fmt.Errorf("BOOT_FROM path %s is inside VM directory %s and would be removed during non-persistent startup", bootFrom, vmDir)
	}
	return nil
}

func pathWithin(root string, path string) bool {
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func pathWithinOrEqual(root string, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	return pathWithin(root, path)
}

func firstPathElement(root string, path string) string {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	return strings.Split(rel, string(os.PathSeparator))[0]
}

func (l *ConcreteLifecycle) prepareDisk(ctx context.Context, cfg config.VM, workImage string) error {
	diskManager := l.diskManager()
	l.warnDiskPreparationIssues(filepath.Dir(workImage), workImage, cfg.DiskSize)
	if fileExists(workImage) && cfg.Persist {
		if _, err := diskManager.ImageInfo(ctx, workImage); err != nil {
			return fmt.Errorf("validate persistent disk %s: %w", workImage, err)
		} else {
			l.infof("Reusing persistent disk %s", workImage)
			if cfg.BootFrom != "" {
				source, err := l.resolveBootSource(ctx, cfg)
				if err != nil {
					return err
				}
				if isISOImage(cfg, source) {
					l.bootISOPath = source
				}
			}
			return l.resizeDiskIfNeeded(ctx, diskManager, workImage, cfg.DiskSize)
		}
	}
	if cfg.BlankWorkDisk {
		if cfg.BootFrom != "" {
			source, err := l.resolveBootSource(ctx, cfg)
			if err != nil {
				return err
			}
			if isISOImage(cfg, source) {
				l.bootISOPath = source
			}
		}
		l.infof("Creating blank disk %s (%s)", workImage, cfg.DiskSize)
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
		l.infof("Creating blank disk %s (%s) for boot media", workImage, cfg.DiskSize)
		return l.diskManager().CreateDisk(ctx, images.CreateDiskRequest{
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
	if l.shouldCreateBackingOverlay(cfg, baseImage, workImage) {
		baseInfo, err := diskManager.ImageInfo(ctx, baseImage)
		if err != nil {
			return fmt.Errorf("validate backing image %s: %w", baseImage, err)
		}
		backingFormat := baseInfo.Format
		if backingFormat == "" {
			return fmt.Errorf("backing image format is empty: %s", baseImage)
		}
		l.infof("Creating working disk overlay %s backed by %s", workImage, baseImage)
		if err := diskManager.CreateOverlay(ctx, images.CreateOverlayRequest{
			Path:          workImage,
			Format:        defaultString(cfg.ImageFormat, "qcow2"),
			BackingPath:   baseImage,
			BackingFormat: backingFormat,
		}); err != nil {
			return err
		}
		return l.resizeDiskIfNeeded(ctx, diskManager, workImage, cfg.DiskSize)
	}
	l.infof("Creating working disk %s from %s", workImage, baseImage)
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
		l.infof("Disk already %.1f GiB (>= %s); skip resize", float64(info.VirtualSize)/(1024*1024*1024), size)
		return nil
	}
	l.infof("Resizing disk %s to %s", path, size)
	if err := diskManager.ResizeDisk(ctx, path, size); err != nil {
		return err
	}
	return nil
}

func (l *ConcreteLifecycle) warnDiskPreparationIssues(vmDir string, workImage string, diskSize string) {
	if diskSize != "" && diskSize != "0" {
		if requested, err := units.ParseSizeBytes(diskSize); err == nil {
			available := hostinfo.AvailableDiskBytes(vmDir)
			if available > 0 && available < requested {
				l.warnf("Storage may be too small for requested disk size: %.1f GiB free at %s, requested %s", float64(available)/(1024*1024*1024), vmDir, diskSize)
			}
		}
	}
	if hostinfo.NativeDiskIOUnsafe(workImage) {
		l.warnf("Filesystem may not support native disk I/O safely; disk cache/io fallback will be used for %s", workImage)
	}
}

func (l *ConcreteLifecycle) prepareExtraDisks(ctx context.Context, cfg config.VM, vmDir string) error {
	if len(cfg.ExtraDisks) == 0 {
		return nil
	}
	diskManager := l.diskManager()
	format := defaultString(cfg.ImageFormat, "qcow2")
	for _, disk := range cfg.ExtraDisks {
		path := filepath.Join(vmDir, fmt.Sprintf("disk%d.%s", disk.Index, format))
		if fileExists(path) {
			if cfg.Persist {
				l.infof("Reusing persistent extra disk %s", path)
				continue
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("replace extra disk %s: %w", path, err)
			}
		}
		l.infof("Creating extra disk %s (%s)", path, disk.Size)
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
		source, err := l.resolveBootSource(ctx, cfg)
		if err != nil {
			return "", err
		}
		if isISOImage(cfg, source) {
			l.bootISOPath = source
			return "", nil
		}
		vmName := defaultString(cfg.VMName, cfg.Distro)
		vmDir, err := l.vmDirFor(vmName)
		if err != nil {
			return "", err
		}
		return l.postProcessImage(ctx, source, filepath.Join(vmDir, "boot."+defaultString(cfg.ImageFormat, "qcow2")), imagePostProcessOptions{
			DesiredFormat:          defaultString(cfg.ImageFormat, "qcow2"),
			SourceFormat:           cfg.SourceImageFormat,
			SourceCompression:      cfg.SourceImageCompression,
			MaxExtractBytes:        cfg.ExtractMaxBytes,
			ReturnSourceWhenUsable: true,
		})
	}
	desiredFormat := defaultString(cfg.ImageFormat, "qcow2")
	baseImage := filepath.Join(l.Layout.BaseImagesDir, cacheName(cfg.Distro)+"."+desiredFormat)
	if fileExists(baseImage) {
		if err := l.validateCachedImage(ctx, baseImage, desiredFormat); err == nil {
			return baseImage, nil
		} else if cfg.ImageURL == "" {
			return "", err
		}
		_ = os.Remove(baseImage)
	}
	if cfg.ImageURL == "" {
		return "", fmt.Errorf("image URL is empty and base image is missing: %s", baseImage)
	}
	downloadPath := filepath.Join(l.Layout.BaseImagesDir, "downloads", cacheName(cfg.ImageURL))
	downloader := l.newDownloader(cfg, "Downloading base image")
	if err := downloader.DownloadWithRetryChecked(ctx, cfg.ImageURL, downloadPath, cfg.DownloadRetries, download.Checksum{
		Algorithm: cfg.ImageChecksumAlgorithm,
		Value:     cfg.ImageChecksumValue,
	}); err != nil {
		return "", err
	}
	return l.postProcessImage(ctx, downloadPath, baseImage, imagePostProcessOptions{
		DesiredFormat:     desiredFormat,
		SourceFormat:      cfg.SourceImageFormat,
		SourceCompression: cfg.SourceImageCompression,
		MaxExtractBytes:   cfg.ExtractMaxBytes,
	})
}

func (l *ConcreteLifecycle) resolveBootSource(ctx context.Context, cfg config.VM) (string, error) {
	ref := cfg.BootFrom
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		destination := filepath.Join(l.Layout.BaseImagesDir, "boot", cacheName(ref))
		checksum := download.Checksum{Algorithm: cfg.BootChecksumAlgorithm, Value: cfg.BootChecksumValue}
		if fileExists(destination) {
			if isNonEmptyFile(destination) {
				if err := download.VerifyChecksum(destination, checksum); err == nil {
					return destination, nil
				}
			}
			_ = os.Remove(destination)
		}
		downloader := l.newDownloader(cfg, "Downloading boot source")
		if checksum.Algorithm != "" || checksum.Value != "" {
			if err := downloader.DownloadWithRetryChecked(ctx, ref, destination, cfg.DownloadRetries, checksum); err != nil {
				return "", err
			}
			return destination, nil
		}
		if err := downloader.DownloadWithRetry(ctx, ref, destination, cfg.DownloadRetries); err != nil {
			return "", err
		}
		return destination, nil
	}
	if oci.IsReference(ref) {
		puller := oci.NewPuller()
		puller.MaxBytes = cfg.ExtractMaxBytes
		result, err := puller.Pull(ctx, ref, filepath.Join(l.Layout.BaseImagesDir, "oci"))
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

func (l *ConcreteLifecycle) newDownloader(cfg config.VM, label string) *download.Downloader {
	downloader := download.NewDownloader(nil)
	downloader.MaxBytes = cfg.DownloadMaxBytes
	downloader.Label = label
	if l.Output != nil {
		downloader.Progress = l.Output.DownloadProgress
	} else if l.Status != nil {
		output := Output{Stderr: l.Status, Stdout: io.Discard, Mode: OutputLog}
		downloader.Progress = output.DownloadProgress
	}
	return downloader
}

type imagePostProcessOptions struct {
	DesiredFormat          string
	SourceFormat           string
	SourceCompression      string
	MaxExtractBytes        int64
	ReturnSourceWhenUsable bool
}

func (l *ConcreteLifecycle) postProcessImage(ctx context.Context, source string, destination string, opts imagePostProcessOptions) (resultPath string, err error) {
	current := source
	intermediates := []string{source}
	var cleanupDirs []string
	defer func() {
		l.cleanupExtractionDirs(cleanupDirs)
	}()
	defer func() {
		if err == nil {
			return
		}
		if destination != "" {
			if removeErr := os.Remove(destination); removeErr != nil && !os.IsNotExist(removeErr) {
				l.warnf("Could not remove failed image destination %s: %v", destination, removeErr)
			}
		}
		l.cleanupIntermediateImages(intermediates, destination)
	}()
	extractor := archive.NewExtractor()
	extractor.MaxBytes = opts.MaxExtractBytes
	if shouldExtractByMetadata(current, opts.SourceCompression) {
		l.infof("Extracting compressed image %s", current)
		extractDir, err := l.extractDirFor(current, destination)
		if err != nil {
			return "", err
		}
		cleanupDirs = appendExtractCleanupDir(cleanupDirs, extractDir, current)
		result, err := extractor.ExtractCompressedStream(ctx, current, extractDir, opts.SourceFormat, opts.SourceCompression)
		if err != nil {
			return "", err
		}
		current = result.Path
		intermediates = append(intermediates, current)
	}
	if shouldExtractArchiveByMetadata(current, opts.SourceFormat) {
		l.infof("Extracting archive image %s", current)
		extractDir, err := l.extractDirFor(current, destination)
		if err != nil {
			return "", err
		}
		cleanupDirs = appendExtractCleanupDir(cleanupDirs, extractDir, current)
		result, err := extractor.ExtractByFormat(ctx, current, extractDir, opts.SourceFormat)
		if err != nil {
			return "", err
		}
		current = result.Path
		intermediates = append(intermediates, current)
	}
	for isArchive(current) {
		l.infof("Extracting image archive %s", current)
		extractDir, err := l.extractDirFor(current, destination)
		if err != nil {
			return "", err
		}
		cleanupDirs = appendExtractCleanupDir(cleanupDirs, extractDir, current)
		result, err := extractor.ExtractWithResult(ctx, current, extractDir)
		if err != nil {
			return "", err
		}
		current = result.Path
		intermediates = append(intermediates, current)
	}
	desiredFormat := defaultString(opts.DesiredFormat, "qcow2")
	diskManager := l.diskManager()
	info, err := diskManager.ImageInfo(ctx, current)
	if err != nil {
		return "", fmt.Errorf("validate image %s: %w", current, err)
	}
	currentFormat := info.Format
	if currentFormat == "" {
		return "", fmt.Errorf("image format is empty: %s", current)
	}
	if current == source && currentFormat == desiredFormat && (!l.shouldCleanupIntermediate(current, destination) || opts.ReturnSourceWhenUsable) {
		return current, nil
	}
	if currentFormat != "" && currentFormat != desiredFormat {
		l.infof("Converting image %s from %s to %s", current, currentFormat, desiredFormat)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return "", fmt.Errorf("create base image directory: %w", err)
		}
		if err := diskManager.ConvertDisk(ctx, current, destination, desiredFormat); err != nil {
			return "", err
		}
		if err := l.validateCachedImage(ctx, destination, desiredFormat); err != nil {
			return "", fmt.Errorf("validate converted image %s: %w", destination, err)
		}
		l.cleanupIntermediateImages(intermediates, destination)
		return destination, nil
	}
	if current != destination {
		l.infof("Placing base image %s", destination)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return "", fmt.Errorf("create base image directory: %w", err)
		}
		if err := copyFile(current, destination); err != nil {
			return "", fmt.Errorf("place base image: %w", err)
		}
		if err := l.validateCachedImage(ctx, destination, desiredFormat); err != nil {
			return "", fmt.Errorf("validate placed image %s: %w", destination, err)
		}
	}
	l.cleanupIntermediateImages(intermediates, destination)
	return destination, nil
}

func (l *ConcreteLifecycle) extractDirFor(source string, destination string) (string, error) {
	if l.shouldCleanupIntermediate(source, destination) {
		return filepath.Dir(source), nil
	}
	root := filepath.Join(l.Layout.BaseImagesDir, "extract")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create extract cache directory: %w", err)
	}
	dir, err := os.MkdirTemp(root, "image-*")
	if err != nil {
		return "", fmt.Errorf("create extract work directory: %w", err)
	}
	return dir, nil
}

func appendExtractCleanupDir(dirs []string, extractDir string, source string) []string {
	if filepath.Clean(extractDir) == filepath.Clean(filepath.Dir(source)) {
		return dirs
	}
	return append(dirs, extractDir)
}

func (l *ConcreteLifecycle) cleanupExtractionDirs(dirs []string) {
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			l.warnf("Could not remove extract directory %s: %v", dir, err)
		}
	}
}

func (l *ConcreteLifecycle) cleanupIntermediateImages(paths []string, destination string) {
	seen := map[string]bool{}
	for _, path := range paths {
		if !l.shouldCleanupIntermediate(path, destination) || seen[path] {
			continue
		}
		seen[path] = true
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			l.warnf("Could not remove intermediate image %s: %v", path, err)
		}
	}
}

func (l *ConcreteLifecycle) shouldCleanupIntermediate(path string, destination string) bool {
	if path == "" || path == destination {
		return false
	}
	base := filepath.Clean(l.Layout.BaseImagesDir)
	cleaned := filepath.Clean(path)
	if cleaned == base {
		return false
	}
	rel, err := filepath.Rel(base, cleaned)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	first := strings.Split(rel, string(os.PathSeparator))[0]
	return first == "downloads" || first == "boot" || first == "oci" || first == "extract"
}

func (l *ConcreteLifecycle) validateCachedImage(ctx context.Context, path string, desiredFormat string) error {
	info, err := l.diskManager().ImageInfo(ctx, path)
	if err != nil {
		return err
	}
	if info.Format == "" {
		return fmt.Errorf("cached image format is empty: %s", path)
	}
	if desiredFormat != "" && info.Format != desiredFormat {
		return fmt.Errorf("cached image format mismatch: got %s want %s", info.Format, desiredFormat)
	}
	return nil
}

func shouldExtractByMetadata(path string, compression string) bool {
	compression = strings.ToLower(strings.TrimSpace(compression))
	if compression == "" || compression == "none" {
		return false
	}
	return !isArchive(path)
}

func normalizedImageFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "", "none", "unknown":
		return ""
	default:
		return format
	}
}

func shouldExtractArchiveByMetadata(path string, format string) bool {
	if isArchive(path) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "zip", "tar", "ova", "7z", "sevenzip", "rar":
		return true
	default:
		return false
	}
}

func (l *ConcreteLifecycle) prepareSeedISO(ctx context.Context, cfg config.VM, outputPath string) error {
	userData := ""
	if cfg.CloudInitUserDataPath != "" {
		content, err := os.ReadFile(cfg.CloudInitUserDataPath)
		if err != nil {
			return fmt.Errorf("read cloud-init user-data: %w", err)
		}
		userData = string(content)
		if strings.TrimSpace(userData) == "" {
			l.warnf("CLOUD_INIT_USER_DATA is empty; generated seed ISO will contain empty user-data")
		}
	}
	content, err := seediso.BuildCloudInit(seediso.CloudInitRequest{
		VMName:       cfg.VMName,
		LoginUser:    cfg.LoginUser,
		LoginShell:   cfg.LoginShell,
		Password:     cfg.Password,
		PasswordHash: password.SHA512Crypt,
		SSHPubKey:    cfg.SSHPubkey,
		UserData:     userData,
		Filesystems:  seedFilesystemMounts(cfg.Filesystems),
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

func seedFilesystemMounts(shares []config.FilesystemShare) []seediso.FilesystemMount {
	if len(shares) == 0 {
		return nil
	}
	mounts := make([]seediso.FilesystemMount, 0, len(shares))
	for _, share := range shares {
		mounts = append(mounts, seediso.FilesystemMount{
			Tag:      share.Target,
			Driver:   share.Driver,
			Readonly: share.Readonly,
		})
	}
	return mounts
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

func isNonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func (l *ConcreteLifecycle) kvmAvailable() bool {
	if l.KVMAvailable != nil {
		return l.KVMAvailable()
	}
	return hostinfo.KVMAvailable()
}

func (l *ConcreteLifecycle) guestClient() guestexec.Client {
	if l.GuestClient != nil {
		return l.GuestClient
	}
	client := guestexec.NewVirshClient(&l.CommandRunner)
	client.LibvirtURI = l.libvirtURI()
	return client
}

func (l *ConcreteLifecycle) blockSectorSize(path string) (int, bool) {
	if l.BlockSectorSize != nil {
		return l.BlockSectorSize(path)
	}
	return hostinfo.BlockSectorSize(path)
}

func (l *ConcreteLifecycle) ipv6Available() bool {
	if l.IPv6Available != nil {
		return l.IPv6Available()
	}
	return hostinfo.IPv6Available()
}

func (l *ConcreteLifecycle) cpuVendor() string {
	if l.CPUVendor != nil {
		return l.CPUVendor()
	}
	return hostinfo.CPUVendor()
}

func (l *ConcreteLifecycle) cpuFlags() map[string]bool {
	if l.CPUFlags != nil {
		return l.CPUFlags()
	}
	return hostinfo.CPUFlags()
}

func isISO(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".iso")
}

func isISOImage(cfg config.VM, path string) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.SourceImageType), "iso") || isISO(path)
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

func copyFile(source string, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := io.Copy(temp, input); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return err
	}
	return nil
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

func isPasstStartError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "passt") || strings.Contains(message, "backend")
}
