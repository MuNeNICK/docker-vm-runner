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
	"github.com/munenick/docker-vm-runner/internal/hostinfo"
	"github.com/munenick/docker-vm-runner/internal/libvirtmgr"
	"github.com/munenick/docker-vm-runner/internal/network"
	"github.com/munenick/docker-vm-runner/internal/paths"
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
	Connect(context.Context, config.VM) error
	Prepare(context.Context, config.VM) error
	StartVM(context.Context, config.VM) error
	WaitForGuestReady(context.Context, config.VM) error
	WaitUntilStopped(context.Context, config.VM) error
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
		PrintAccess(r.Stdout, cfg)
		return nil
	}
	if r.Lifecycle == nil {
		r.Lifecycle = r.newConcreteLifecycle()
	}
	if opts.Cleanup {
		return r.runCleanup(ctx, cfg)
	}
	return r.runLifecycle(ctx, cfg, opts)
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
	return paths.ResolveLayout(r.Env.Get("DATA_DIR", ""), nil)
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
	return domain.NewRenderer().Render(domain.Request{
		VM:                cfg,
		VMDir:             vmDir,
		WorkImagePath:     filepath.Join(vmDir, "disk."+imageFormat),
		SeedISOPath:       seedISOPath,
		BootISOPath:       cfg.BootFrom,
		IPXEROMPath:       cfg.IPXEROMPath,
		KVMAvailable:      hostinfo.FileExists("/dev/kvm"),
		EffectiveCPUModel: cfg.CPUModel,
		IPv6Enabled:       hostinfo.IPv6Available(),
		IntelRenderNode:   hostinfo.FileExists("/dev/dri/renderD128"),
		HostCPUVendor:     hostinfo.CPUVendor(),
		HostCPUFlags:      hostinfo.CPUFlags(),
		BlockSectorSize:   hostinfo.BlockSectorSize,
	})
}

func (r *Runner) newConcreteLifecycle() *ConcreteLifecycle {
	lifecycle := NewConcreteLifecycle(r.layout())
	applyRuntimeEnv(lifecycle, r.Env)
	return lifecycle
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
	if err := r.Lifecycle.Prepare(ctx, cfg); err != nil {
		return err
	}
	if err := r.Lifecycle.StartVM(ctx, cfg); err != nil {
		return err
	}
	vmStarted = true
	PrintAccess(r.Stdout, cfg)
	if opts.NoConsole {
		if cfg.CloudInitEnabled {
			if err := r.Lifecycle.WaitForGuestReady(ctx, cfg); err != nil {
				return err
			}
		}
		if err := r.Lifecycle.WaitUntilStopped(ctx, cfg); err != nil {
			return err
		}
		vmStopped = true
	} else {
		if code, err := r.Lifecycle.AttachConsole(ctx, cfg); err != nil {
			return err
		} else if code != 0 {
			return fmt.Errorf("console exited with status %d", code)
		}
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
		fmt.Fprintf(w, "  %s: %v\n", name, value.Field(i).Interface())
	}
}

func PrintAccess(w io.Writer, cfg config.VM) {
	printBlock(w, "Access", AccessLines(cfg))
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
			lines = append(lines, fmt.Sprintf("SSH:     ssh -p %d %s@localhost", cfg.SSHPort, cfg.LoginUser))
		} else {
			lines = append(lines, fmt.Sprintf("SSH:     port %d -> guest:22", cfg.SSHPort))
		}
		portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", cfg.SSHPort, cfg.SSHPort))
	}
	if cfg.CloudInitEnabled {
		lines = append(lines, fmt.Sprintf("Login:   %s / %s", cfg.LoginUser, cfg.Password))
	}
	if cfg.NoVNCEnabled {
		lines = append(lines, fmt.Sprintf("Console: https://localhost:%d/vnc.html", cfg.NoVNCPort))
		portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", cfg.NoVNCPort, cfg.NoVNCPort))
	} else if cfg.GraphicsType == "vnc" {
		lines = append(lines, fmt.Sprintf("VNC:     localhost:%d", cfg.VNCPort))
		portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", cfg.VNCPort, cfg.VNCPort))
	}
	if cfg.RedfishEnabled {
		lines = append(lines, fmt.Sprintf("Redfish: https://localhost:%d/", cfg.RedfishPort))
		portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", cfg.RedfishPort, cfg.RedfishPort))
	}
	if len(cfg.PortForwards) > 0 && hasUserNIC {
		forwardLines := make([]string, 0, len(cfg.PortForwards))
		for _, forward := range cfg.PortForwards {
			forwardLines = append(forwardLines, fmt.Sprintf("%d->%d", forward.HostPort, forward.GuestPort))
			portsToPublish = append(portsToPublish, fmt.Sprintf("-p %d:%d", forward.HostPort, forward.HostPort))
		}
		lines = append(lines, "Ports:   "+strings.Join(forwardLines, ", "))
	}
	if len(portsToPublish) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Publish: "+strings.Join(portsToPublish, " "))
	}
	return lines
}

func VMSummaryLines(cfg config.VM) []string {
	lines := []string{
		fmt.Sprintf("%d vCPU | %d MiB RAM | %s disk", cfg.CPUs, cfg.MemoryMB, cfg.DiskSize),
		fmt.Sprintf("%s boot (%s) | %s bus", strings.ToUpper(cfg.BootMode), cfg.MachineType, cfg.DiskController),
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
		lines = append(lines, strings.Join(features, " | "))
	}
	for index, nic := range cfg.NICs {
		label := "NIC"
		if len(cfg.NICs) > 1 {
			label = fmt.Sprintf("NIC%d", index+1)
		}
		lines = append(lines, fmt.Sprintf("%s: %s (%s)", label, nic.Mode, nic.Model))
	}
	lines = append(lines, "Boot: "+strings.Join(cfg.BootOrder, ", "))
	return lines
}

func printBlock(w io.Writer, title string, lines []string) {
	fmt.Fprintf(w, "== %s ==\n", title)
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
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
