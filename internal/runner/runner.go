package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/domain"
	"github.com/munenick/docker-vm-runner/internal/firmware"
	"github.com/munenick/docker-vm-runner/internal/hostinfo"
	"github.com/munenick/docker-vm-runner/internal/libvirtmgr"
	"github.com/munenick/docker-vm-runner/internal/network"
	"github.com/munenick/docker-vm-runner/internal/oci"
	"github.com/munenick/docker-vm-runner/internal/paths"
	"github.com/munenick/docker-vm-runner/internal/services"
)

type Options struct {
	NoConsole   bool
	ListDistros bool
	ListArch    string
	ListType    string
	ListSearch  string
	ShowConfig  bool
	ShowXML     bool
	DryRun      bool
	Cleanup     bool
}

type Lifecycle interface {
	StartServices(context.Context, config.VM) error
	StartCleanupServices(context.Context, config.VM) error
	Connect(context.Context, config.VM) error
	Prepare(context.Context, config.VM) error
	StartVM(context.Context, config.VM) error
	WaitForGuestReady(context.Context, config.VM) error
	WaitUntilStopped(context.Context, config.VM) error
	DomainStopped(context.Context, config.VM) (bool, error)
	AttachConsole(context.Context, config.VM) (int, error)
	MarkInstalled(context.Context, config.VM) error
	Cleanup(context.Context, config.VM) error
	CleanupStale(context.Context, config.VM) error
	Close(context.Context, config.VM) error
	StopServices(context.Context, config.VM) error
}

type Runner struct {
	Stdout           io.Writer
	Stderr           io.Writer
	Env              config.MapEnv
	Resolver         *config.Resolver
	DistroConfigPath string
	Lifecycle        Lifecycle
	IsMount          func(string) bool
	DetectHostInfo   func(string) hostinfo.Info
	OutputMode       OutputMode
}

func New() *Runner {
	return &Runner{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Env:    config.OSMapEnv(),
	}
}

func (r *Runner) Run(ctx context.Context, opts Options) error {
	r.applyDefaults()
	if opts.ListDistros {
		return r.printDistros(opts)
	}
	cfg, err := r.Resolver.Resolve(r.Env)
	if err != nil {
		return err
	}
	r.printWarnings(cfg.Warnings)
	if opts.ShowConfig {
		PrintConfig(r.Stdout, cfg)
		return nil
	}
	if opts.ShowXML {
		xmlText, err := r.renderDomainXML(cfg)
		if err != nil {
			return err
		}
		fmt.Fprintln(r.Stdout, xmlText)
		return nil
	}
	if opts.DryRun {
		PrintConfig(r.Stdout, cfg)
		info := r.hostInfo(cfg)
		r.output().Section("Host", normalizedLines(hostinfo.Lines(info)))
		r.output().Section("Dry Run", DryRunLines(cfg, info))
		r.output().Section("Access", AccessLines(cfg))
		return r.validateDryRun(cfg)
	}
	if r.Lifecycle == nil {
		r.Lifecycle = r.newConcreteLifecycle()
	}
	if opts.Cleanup {
		return r.runCleanup(ctx, cfg)
	}
	return r.runLifecycle(ctx, cfg, opts)
}

func (r *Runner) validateDryRun(cfg config.VM) error {
	var failures []string
	if cfg.RequireKVM && !hostinfo.KVMAvailable() {
		failures = append(failures, "REQUIRE_KVM=1 requires /dev/kvm")
	}
	if cfg.BootFrom != "" && !isRemoteReference(cfg.BootFrom) && !oci.IsReference(cfg.BootFrom) && !hostinfo.FileExists(cfg.BootFrom) {
		failures = append(failures, fmt.Sprintf("BOOT_FROM path not found: %s", cfg.BootFrom))
	}
	if len(failures) > 0 {
		return fmt.Errorf("dry-run validation failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

func isRemoteReference(value string) bool {
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func (r *Runner) applyDefaults() {
	if r.Stdout == nil {
		r.Stdout = io.Discard
	}
	if r.Stderr == nil {
		r.Stderr = io.Discard
	}
	if r.Env == nil {
		r.Env = config.OSMapEnv()
	}
	if r.DistroConfigPath == "" {
		r.DistroConfigPath = paths.DefaultConfigPath
	}
	if r.IsMount == nil {
		r.IsMount = hostinfo.IsMount
	}
	if r.DetectHostInfo == nil {
		r.DetectHostInfo = hostinfo.Detect
	}
	if r.Resolver == nil {
		r.Resolver = &config.Resolver{DistroConfigPath: r.DistroConfigPath}
	}
	r.applyResolverDefaults()
}

func (r *Runner) applyResolverDefaults() {
	if r.Resolver.Layout == (paths.Layout{}) {
		r.Resolver.Layout = r.layout()
	}
	if r.Resolver.AvailableMemoryBytes == nil {
		r.Resolver.AvailableMemoryBytes = hostinfo.AvailableMemoryBytes
	}
	if r.Resolver.CPUCount == nil {
		r.Resolver.CPUCount = hostinfo.CPUCount
	}
	if r.Resolver.AvailableDiskBytes == nil {
		r.Resolver.AvailableDiskBytes = hostinfo.AvailableDiskBytes
	}
	if r.Resolver.DetectHostMTU == nil {
		r.Resolver.DetectHostMTU = hostinfo.DetectHostMTU
	}
	if r.Resolver.ROMExists == nil {
		r.Resolver.ROMExists = hostinfo.FileExists
	}
	if r.Resolver.FileExists == nil {
		r.Resolver.FileExists = hostinfo.FileExists
	}
	if r.Resolver.IsFile == nil {
		r.Resolver.IsFile = hostinfo.IsFile
	}
	if r.Resolver.IsBlockDevice == nil {
		r.Resolver.IsBlockDevice = hostinfo.IsBlockDevice
	}
	if r.Resolver.ReadFile == nil {
		r.Resolver.ReadFile = os.ReadFile
	}
}

func (r *Runner) layout() paths.Layout {
	return paths.ResolveLayout(r.Env.Get("DATA_DIR", ""), r.IsMount)
}

func (r *Runner) renderDomainXML(cfg config.VM) (string, error) {
	layout := r.layout()
	vmDir := filepath.Join(layout.VMImagesDir, cfg.VMName)
	imageFormat := cfg.ImageFormat
	if imageFormat == "" {
		imageFormat = "qcow2"
	}
	seedISOPath := ""
	if cfg.CloudInitEnabled {
		seedISOPath = filepath.Join(vmDir, "seed.iso")
	}
	bootISOPath := showXMLBootISOPath(cfg, layout)
	fw, err := firmware.Preview(layout.StateDir, firmware.Request{
		Arch:     cfg.Arch,
		BootMode: cfg.BootMode,
		VMName:   cfg.VMName,
		Profile:  config.SupportedArchitectures[cfg.Arch],
	})
	if err != nil {
		return "", err
	}
	return domain.NewRenderer().Render(domain.Request{
		VM:                cfg,
		VMDir:             vmDir,
		WorkImagePath:     filepath.Join(vmDir, "disk."+imageFormat),
		SeedISOPath:       seedISOPath,
		BootISOPath:       bootISOPath,
		FirmwareLoader:    fw.LoaderPath,
		FirmwareVars:      fw.VarsPath,
		IPXEROMPath:       cfg.IPXEROMPath,
		KVMAvailable:      hostinfo.KVMAvailable(),
		EffectiveCPUModel: cfg.CPUModel,
		IPv6Enabled:       hostinfo.IPv6Available(),
		IntelRenderNode:   hostinfo.FileExists("/dev/dri/renderD128"),
		HostCPUVendor:     hostinfo.CPUVendor(),
		HostCPUFlags:      hostinfo.CPUFlags(),
		BlockSectorSize:   hostinfo.BlockSectorSize,
		NativeIOUnsafe:    hostinfo.NativeDiskIOUnsafe(filepath.Join(vmDir, "disk."+imageFormat)),
	})
}

func showXMLBootISOPath(cfg config.VM, layout paths.Layout) string {
	if strings.TrimSpace(cfg.BootFrom) == "" {
		return ""
	}
	sourceType := strings.ToLower(strings.TrimSpace(cfg.SourceImageType))
	if sourceType != "iso" && !isISOBootReference(cfg.BootFrom) {
		return ""
	}
	if isRemoteReference(cfg.BootFrom) {
		return filepath.Join(layout.BaseImagesDir, "boot", cacheName(cfg.BootFrom))
	}
	return cfg.BootFrom
}

func (r *Runner) newConcreteLifecycle() *ConcreteLifecycle {
	lifecycle := NewConcreteLifecycle(r.layout())
	output := r.output()
	lifecycle.Status = r.Stderr
	lifecycle.Output = &output
	applyRuntimeInfo(lifecycle, r.hostInfo(config.VM{}), r.Stderr)
	applyRuntimeEnv(lifecycle, r.Env)
	return lifecycle
}

func (r *Runner) hostInfo(cfg config.VM) hostinfo.Info {
	if r.DetectHostInfo == nil {
		r.DetectHostInfo = hostinfo.Detect
	}
	return r.DetectHostInfo(workImageProbePath(cfg))
}

func applyRuntimeInfo(lifecycle *ConcreteLifecycle, info hostinfo.Info, warningWriter io.Writer) {
	if lifecycle == nil {
		return
	}
	runtimeInfo := services.RuntimeInfo{Rootless: info.RuntimeRootless, Privileged: info.RuntimePriv}
	if supervisor, ok := lifecycle.Service.(*services.Supervisor); ok {
		supervisor.Options.Runtime = runtimeInfo
		if supervisor.Options.WarningWriter == nil {
			supervisor.Options.WarningWriter = warningWriter
		}
		return
	}
	if lifecycle.Service == nil {
		lifecycle.Service = services.NewSupervisor(services.Options{Runtime: runtimeInfo, WarningWriter: warningWriter})
	}
}

func (r *Runner) printWarnings(warnings []string) {
	output := r.output()
	for _, warning := range warnings {
		if strings.TrimSpace(warning) != "" {
			output.Warn(warning)
		}
	}
}

func (r *Runner) output() Output {
	mode := r.OutputMode
	if mode == OutputAuto {
		mode = OutputLog
		if isTerminalWriter(r.Stdout) && isTerminalWriter(r.Stderr) && strings.TrimSpace(r.Env.Get("NO_COLOR", "")) == "" && strings.TrimSpace(r.Env.Get("TERM", "")) != "dumb" {
			mode = OutputTerminal
		}
	}
	return Output{Stdout: r.Stdout, Stderr: r.Stderr, Mode: mode}
}

func applyRuntimeEnv(lifecycle *ConcreteLifecycle, env config.MapEnv) {
	if lifecycle == nil {
		return
	}
	if libvirtURI := strings.TrimSpace(env.Get("LIBVIRT_URI", "")); libvirtURI != "" {
		lifecycle.LibvirtURI = libvirtURI
	} else if lifecycle.LibvirtURI == "" {
		lifecycle.LibvirtURI = libvirtmgr.DefaultURI
	}
	if pool := strings.TrimSpace(env.Get("REDFISH_STORAGE_POOL", "")); pool != "" {
		lifecycle.RedfishPool.Name = pool
	}
	if targetPath := strings.TrimSpace(env.Get("REDFISH_STORAGE_PATH", "")); targetPath != "" {
		lifecycle.RedfishPool.TargetPath = targetPath
	}
}

func (r *Runner) printDistros(opts Options) error {
	filter := config.DistroListFilter{
		Arch:      opts.ListArch,
		ImageType: opts.ListType,
		Search:    opts.ListSearch,
	}
	catalogSource, err := config.ResolveCatalogSource(r.Env, r.DistroConfigPath)
	if err != nil {
		return err
	}
	distros, normalizedArch, err := config.ListDistrosFromSource(catalogSource, filter)
	if err != nil {
		return err
	}
	if normalizedArch != "" {
		fmt.Fprintf(r.Stderr, "Showing distributions for arch: %s\n", normalizedArch)
	}
	if len(distros) == 0 {
		fmt.Fprintln(r.Stderr, "No distributions found")
		return nil
	}
	width := 0
	for _, distro := range distros {
		if len(distro.Key) > width {
			width = len(distro.Key)
		}
	}
	for _, distro := range distros {
		fmt.Fprintf(r.Stdout, "  %-*s  %s  (type=%s, arch=%s, user=%s)\n", width, distro.Key, distro.Name, distro.ImageType, distro.Arch, distro.User)
	}
	return nil
}

func (r *Runner) runLifecycle(ctx context.Context, cfg config.VM, opts Options) (retErr error) {
	vmStarted := false
	vmStopped := false
	output := r.output()
	output.Startup(cfg)
	output.Step("Starting libvirt services")
	if err := r.Lifecycle.StartServices(ctx, cfg); err != nil {
		stopCtx, cancel := lifecycleCleanupContext()
		defer cancel()
		_ = r.Lifecycle.StopServices(stopCtx, cfg)
		return err
	}
	defer func() {
		cleanupCtx, cancel := lifecycleCleanupContext()
		defer cancel()
		if err := r.Lifecycle.StopServices(cleanupCtx, cfg); retErr == nil && err != nil {
			retErr = err
		}
	}()
	if err := r.Lifecycle.Connect(ctx, cfg); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := lifecycleCleanupContext()
		defer cancel()
		if err := r.Lifecycle.Close(cleanupCtx, cfg); retErr == nil && err != nil {
			retErr = err
		}
	}()
	defer func() {
		cleanupCtx, cancel := lifecycleCleanupContext()
		defer cancel()
		if err := r.Lifecycle.Cleanup(cleanupCtx, cfg); retErr == nil && err != nil {
			retErr = err
		}
	}()
	output.Step("Preparing VM image and configuration")
	if err := r.Lifecycle.Prepare(ctx, cfg); err != nil {
		return err
	}
	output.Step("Starting VM")
	if err := r.Lifecycle.StartVM(ctx, cfg); err != nil {
		return err
	}
	vmStarted = true
	output.Success("VM started")
	output.Section("Access", AccessLines(cfg))
	if opts.NoConsole {
		output.HeadlessWait()
		if cfg.CloudInitEnabled {
			if err := r.Lifecycle.WaitForGuestReady(ctx, cfg); err != nil {
				output.Warn("Guest readiness check did not complete", "VM will keep running. "+err.Error())
			}
		}
		if err := r.Lifecycle.WaitUntilStopped(ctx, cfg); err != nil {
			return err
		}
		vmStopped = true
	} else {
		output.ConsoleAttach()
		if code, err := r.Lifecycle.AttachConsole(ctx, cfg); err != nil {
			return err
		} else if code != 0 {
			return fmt.Errorf("console exited with status %d", code)
		}
		stopped, err := r.Lifecycle.DomainStopped(ctx, cfg)
		if err != nil {
			output.Warn("Could not determine VM state after console exit", err.Error())
		}
		vmStopped = stopped
	}
	if shouldMarkInstalled(cfg, vmStarted, vmStopped) {
		if err := r.Lifecycle.MarkInstalled(ctx, cfg); err != nil {
			return err
		}
	}
	return nil
}

func shouldMarkInstalled(cfg config.VM, vmStarted bool, vmStopped bool) bool {
	if !vmStarted || !vmStopped || !cfg.Persist {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(cfg.SourceImageType), "iso") || isISOBootReference(cfg.BootFrom)
}

func isISOBootReference(value string) bool {
	cleaned := strings.Split(strings.Split(value, "?")[0], "#")[0]
	return strings.HasSuffix(strings.ToLower(cleaned), ".iso")
}

func (r *Runner) runCleanup(ctx context.Context, cfg config.VM) (retErr error) {
	if err := r.Lifecycle.StartCleanupServices(ctx, cfg); err != nil {
		stopCtx, cancel := lifecycleCleanupContext()
		defer cancel()
		_ = r.Lifecycle.StopServices(stopCtx, cfg)
		return err
	}
	defer func() {
		cleanupCtx, cancel := lifecycleCleanupContext()
		defer cancel()
		if err := r.Lifecycle.StopServices(cleanupCtx, cfg); retErr == nil && err != nil {
			retErr = err
		}
	}()
	if err := r.Lifecycle.Connect(ctx, cfg); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := lifecycleCleanupContext()
		defer cancel()
		if err := r.Lifecycle.Close(cleanupCtx, cfg); retErr == nil && err != nil {
			retErr = err
		}
	}()
	return r.Lifecycle.CleanupStale(ctx, cfg)
}

func lifecycleCleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func PrintConfig(w io.Writer, cfg config.VM) {
	value := reflect.ValueOf(cfg)
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := toSnake(field.Name)
		if config.SensitiveFields[name] {
			fmt.Fprintf(w, "  %s: ********\n", name)
			continue
		}
		printConfigValue(w, "  ", name, value.Field(i))
	}
}

func printConfigValue(w io.Writer, indent string, name string, value reflect.Value) {
	if value.Kind() == reflect.Slice {
		if value.Len() == 0 {
			fmt.Fprintf(w, "%s%s: []\n", indent, name)
			return
		}
		fmt.Fprintf(w, "%s%s:\n", indent, name)
		for i := 0; i < value.Len(); i++ {
			item := value.Index(i)
			if item.Kind() != reflect.Struct && (item.Kind() != reflect.Pointer || item.IsNil() || item.Elem().Kind() != reflect.Struct) {
				fmt.Fprintf(w, "%s  [%d]: %v\n", indent, i, item.Interface())
				continue
			}
			fmt.Fprintf(w, "%s  [%d]:\n", indent, i)
			printConfigStructFields(w, indent+"    ", item)
		}
		return
	}
	fmt.Fprintf(w, "%s%s: %v\n", indent, name, value.Interface())
}

func printConfigStructFields(w io.Writer, indent string, value reflect.Value) {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			fmt.Fprintf(w, "%s<nil>\n", indent)
			return
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		fmt.Fprintf(w, "%s%v\n", indent, value.Interface())
		return
	}
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		printConfigValue(w, indent, toSnake(field.Name), value.Field(i))
	}
}

func PrintAccess(w io.Writer, cfg config.VM) {
	Output{Stdout: w, Stderr: io.Discard, Mode: OutputLog}.Section("Access", AccessLines(cfg))
}

func PrintHost(w io.Writer, cfg config.VM) {
	Output{Stdout: w, Stderr: io.Discard, Mode: OutputLog}.Section("Host", normalizedLines(hostinfo.Lines(hostinfo.Detect(workImageProbePath(cfg)))))
}

func PrintVMSummary(w io.Writer, cfg config.VM) {
	Output{Stdout: w, Stderr: io.Discard, Mode: OutputLog}.Section("VM: "+cfg.Distro, VMSummaryLines(cfg))
}

func workImageProbePath(cfg config.VM) string {
	if cfg.Persist {
		return "/data"
	}
	return "/images"
}

func AccessLines(cfg config.VM) []string {
	lines := []string{}
	hasUserNIC := false
	for _, nic := range cfg.NICs {
		if nic.Mode == "user" {
			hasUserNIC = true
			break
		}
	}
	portsToPublish := []string{}
	if hasUserNIC && cfg.SSHPort != 0 {
		if cfg.CloudInitEnabled {
			lines = append(lines, fmt.Sprintf("SSH      ssh -p %d %s@localhost", cfg.SSHPort, cfg.LoginUser))
		} else {
			lines = append(lines, fmt.Sprintf("SSH      port %d -> guest:22", cfg.SSHPort))
		}
		portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", cfg.SSHPort, cfg.SSHPort))
	}
	if cfg.CloudInitEnabled {
		lines = append(lines, fmt.Sprintf("Login    %s / %s", cfg.LoginUser, cfg.Password))
	}
	if cfg.NoVNCEnabled {
		lines = append(lines, fmt.Sprintf("Console  https://localhost:%d/vnc.html?autoconnect=1&resize=scale", cfg.NoVNCPort))
		portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", cfg.NoVNCPort, cfg.NoVNCPort))
	} else if cfg.GraphicsType == "vnc" {
		lines = append(lines, fmt.Sprintf("VNC      localhost:%d", cfg.VNCPort))
		portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", cfg.VNCPort, cfg.VNCPort))
	}
	if cfg.RedfishEnabled {
		lines = append(lines, fmt.Sprintf("Redfish  https://localhost:%d/", cfg.RedfishPort))
		portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", cfg.RedfishPort, cfg.RedfishPort))
	}
	if len(cfg.PortForwards) > 0 && hasUserNIC {
		forwardLines := make([]string, 0, len(cfg.PortForwards))
		for _, forward := range cfg.PortForwards {
			forwardLines = append(forwardLines, fmt.Sprintf("%d->%d", forward.HostPort, forward.GuestPort))
			portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", forward.HostPort, forward.HostPort))
		}
		lines = append(lines, "Ports    "+strings.Join(forwardLines, ", "))
	}
	if len(portsToPublish) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Publish  "+strings.Join(portsToPublish, " "))
	}
	return lines
}

func VMSummaryLines(cfg config.VM) []string {
	lines := []string{
		"Image    " + defaultString(cfg.DistroName, cfg.Distro),
		fmt.Sprintf("Compute  %d vCPU / %d MiB RAM", cfg.CPUs, cfg.MemoryMB),
		fmt.Sprintf("Disk     %s %s on %s", cfg.DiskSize, defaultString(cfg.ImageFormat, "qcow2"), cfg.DiskController),
		fmt.Sprintf("Boot     %s / %s", strings.ToUpper(cfg.BootMode), strings.Join(cfg.BootOrder, ", ")),
	}
	if len(cfg.NICs) > 0 {
		networkLines := make([]string, 0, len(cfg.NICs))
		for _, nic := range cfg.NICs {
			mode := nic.Mode
			if mode == "user" {
				mode = "user mode"
			}
			networkLines = append(networkLines, fmt.Sprintf("%s, %s", mode, nic.Model))
		}
		lines = append(lines, "Network  "+strings.Join(networkLines, "; "))
	}
	features := []string{}
	if cfg.TPMEnabled {
		features = append(features, "TPM")
	}
	if cfg.HyperVEnabled {
		features = append(features, "Hyper-V")
	}
	if cfg.IOThread {
		features = append(features, "IOThread")
	}
	if cfg.BalloonEnabled {
		features = append(features, "Balloon")
	}
	if cfg.RNGEnabled {
		features = append(features, "RNG")
	}
	if cfg.GPUPassthrough != "" && cfg.GPUPassthrough != "off" {
		features = append(features, "GPU:"+cfg.GPUPassthrough)
	}
	if len(features) > 0 {
		lines = append(lines, "Features "+strings.Join(features, ", "))
	}
	if len(cfg.ExtraDisks) > 0 {
		diskLines := make([]string, 0, len(cfg.ExtraDisks))
		for _, disk := range cfg.ExtraDisks {
			diskLines = append(diskLines, fmt.Sprintf("disk%d=%s", disk.Index, disk.Size))
		}
		lines = append(lines, "Extra    "+strings.Join(diskLines, ", "))
	}
	if len(cfg.BlockDevices) > 0 {
		devices := make([]string, 0, len(cfg.BlockDevices))
		for _, device := range cfg.BlockDevices {
			devices = append(devices, device.Path)
		}
		lines = append(lines, "Devices  "+strings.Join(devices, ", "))
	}
	return lines
}

func DryRunLines(cfg config.VM, info hostinfo.Info) []string {
	lines := []string{}
	if info.RuntimeEngine != "" {
		priv := "unprivileged"
		if info.RuntimePriv {
			priv = "privileged"
		}
		rootless := ""
		if info.RuntimeRootless {
			rootless = ", rootless"
		}
		lines = append(lines, fmt.Sprintf("Runtime     %s (%s%s)", info.RuntimeEngine, priv, rootless))
	} else {
		lines = append(lines, "Runtime     unknown")
	}
	if info.KVMAvailable {
		lines = append(lines, "KVM         available (/dev/kvm)")
	} else if cfg.RequireKVM {
		lines = append(lines, "KVM         NOT available (REQUIRE_KVM=1 will fail)")
	} else {
		lines = append(lines, "KVM         NOT available (TCG fallback)")
	}
	lines = append(lines, "Boot order  "+strings.Join(cfg.BootOrder, ", "))
	if cfg.Persist {
		lines = append(lines, "Persistence enabled")
	} else {
		lines = append(lines, "Persistence disabled (ephemeral)")
	}
	if cfg.CloudInitEnabled {
		lines = append(lines, "Cloud-init  enabled (user="+cfg.LoginUser+")")
	} else {
		lines = append(lines, "Cloud-init  disabled")
	}
	if strings.TrimSpace(cfg.BootFrom) != "" {
		if isRemoteReference(cfg.BootFrom) {
			lines = append(lines, "BOOT_FROM   "+cfg.BootFrom+" (will download)")
		} else if oci.IsReference(cfg.BootFrom) {
			lines = append(lines, "BOOT_FROM   "+cfg.BootFrom+" (OCI pull)")
		} else if hostinfo.FileExists(cfg.BootFrom) {
			lines = append(lines, "BOOT_FROM   "+cfg.BootFrom+" (found)")
		} else {
			lines = append(lines, "BOOT_FROM   "+cfg.BootFrom+" (NOT FOUND)")
		}
	}
	for i, nic := range cfg.NICs {
		mac := strings.TrimSpace(nic.MACAddress)
		if mac == "" {
			mac = "auto"
		}
		lines = append(lines, fmt.Sprintf("NIC #%d     mode=%s, model=%s, mac=%s", i+1, nic.Mode, nic.Model, mac))
	}
	lines = append(lines, "Result      no VM started")
	return lines
}

func toSnake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	replacements := map[string]string{
		"v_m":       "vm",
		"c_p_u":     "cpu",
		"i_p_x_e":   "ipxe",
		"r_o_m":     "rom",
		"i_d":       "id",
		"m_b":       "mb",
		"v_n_c":     "vnc",
		"no_v_n_c":  "novnc",
		"s_s_h":     "ssh",
		"t_p_m":     "tpm",
		"c_p_us":    "cpus",
		"u_s_b":     "usb",
		"r_n_g":     "rng",
		"g_p_u":     "gpu",
		"i_o":       "io",
		"h_t_t_p":   "http",
		"redfish_i": "redfish_i",
		"u_r_l":     "url",
		"m_a_c":     "mac",
		"m_t_u":     "mtu",
		"i_pv":      "ipv",
		"k_vm":      "kvm",
		"i_s_o":     "iso",
		"n_i_cs":    "nics",
	}
	out := b.String()
	keys := make([]string, 0, len(replacements))
	for key := range replacements {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, key := range keys {
		out = strings.ReplaceAll(out, key, replacements[key])
	}
	return out
}

func HasUserNIC(nics []network.Config) bool {
	for _, nic := range nics {
		if nic.Mode == "user" {
			return true
		}
	}
	return false
}
