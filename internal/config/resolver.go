package config

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/munenick/docker-vm-runner/internal/network"
	"github.com/munenick/docker-vm-runner/internal/paths"
	"github.com/munenick/docker-vm-runner/internal/units"
	"github.com/munenick/docker-vm-runner/internal/vmname"
	"gopkg.in/yaml.v3"
)

type Resolver struct {
	DistroConfigPath     string
	Layout               paths.Layout
	AvailableMemoryBytes func() int64
	CPUCount             func() int
	AvailableDiskBytes   func(string) int64
	DetectHostMTU        func() int
	ROMExists            func(string) bool
	FileExists           func(string) bool
	IsFile               func(string) bool
	IsBlockDevice        func(string) bool
	ReadFile             func(string) ([]byte, error)
}

type Disk struct {
	Size       string
	Index      int
	Controller string
}

type BlockDevice struct {
	Path  string
	Index int
}

type VM struct {
	Distro                string
	ImageURL              string
	LoginUser             string
	ImageFormat           string
	DistroName            string
	MemoryMB              int
	CPUs                  int
	DiskSize              string
	Display               string
	GraphicsType          string
	Arch                  string
	CPUModel              string
	ExtraArgs             string
	NoVNCEnabled          bool
	VNCPort               int
	VNCKeymap             string
	NoVNCPort             int
	BootFrom              string
	BlankWorkDisk         bool
	BootOrder             []string
	CloudInitEnabled      bool
	CloudInitUserDataPath string
	Password              string
	SSHPort               int
	VMName                string
	Persist               bool
	ForceISO              bool
	SSHPubkey             string
	RedfishUser           string
	RedfishPassword       string
	RedfishPort           int
	RedfishSystemID       string
	RedfishEnabled        bool
	NICs                  []network.Config
	PortForwards          []network.PortForward
	Filesystems           []FilesystemShare
	BootMode              string
	TPMEnabled            bool
	MachineType           string
	ExtraDisks            []Disk
	BlockDevices          []BlockDevice
	DiskController        string
	DiskPreallocate       bool
	DiskIO                string
	DiskCache             string
	IOThread              bool
	BalloonEnabled        bool
	RNGEnabled            bool
	USBController         bool
	HyperVEnabled         bool
	GPUPassthrough        string
	RequireKVM            bool
	LoginShell            string
	DownloadRetries       int
}

func (r *Resolver) Resolve(env MapEnv) (VM, error) {
	r.setDefaults()

	distro := env.Get("DISTRO", "ubuntu-2404")
	distroInfo, err := LoadDistroConfig(r.DistroConfigPath, distro)
	if err != nil {
		return VM{}, err
	}

	arch, err := ResolveArchitecture(distroInfo.Arch, env.Get("ARCH", ""))
	if err != nil {
		return VM{}, err
	}

	memoryMB, err := r.resolveMemory(env.Get("MEMORY", "4096"))
	if err != nil {
		return VM{}, err
	}
	cpus, err := r.resolveCPUs(env.Get("CPUS", "2"))
	if err != nil {
		return VM{}, err
	}
	diskSize, err := r.resolveDiskSize(env.Get("DISK_SIZE", "20G"))
	if err != nil {
		return VM{}, err
	}

	display := strings.ToLower(strings.TrimSpace(env.Get("GRAPHICS", "none")))
	if display == "" {
		display = "none"
	}
	graphicsType, novncEnabled, err := resolveGraphics(display)
	if err != nil {
		return VM{}, err
	}

	vncPort, err := env.Int("VNC_PORT", "5900", 1, ptr(65535))
	if err != nil {
		return VM{}, err
	}
	novncPort, err := env.Int("NOVNC_PORT", "6080", 1, ptr(65535))
	if err != nil {
		return VM{}, err
	}
	sshPort, err := env.Int("SSH_PORT", "2222", 1, ptr(65535))
	if err != nil {
		return VM{}, err
	}
	redfishPort, err := env.Int("REDFISH_PORT", "8443", 1, ptr(65535))
	if err != nil {
		return VM{}, err
	}

	bootFrom := strings.TrimSpace(env.Get("BOOT_FROM", ""))
	blankDisk, _ := env.Bool("BLANK_DISK", false)
	if strings.EqualFold(bootFrom, "blank") {
		blankDisk = true
		bootFrom = ""
	}
	isoRequested := bootFrom != "" && isISOReference(bootFrom)
	bootOrder, err := resolveBootOrder(env.Get("BOOT_ORDER", "hd"), isoRequested)
	if err != nil {
		return VM{}, err
	}
	if isoRequested && env.Get("BLANK_DISK", "") == "" {
		blankDisk = true
	}

	cloudInit, err := r.resolveCloudInit(env, isoRequested)
	if err != nil {
		return VM{}, err
	}

	vmName := vmname.Derive(distro, isoRequested, env.Lookup)
	networkConfig, err := ParseNetwork(env, NetworkParseOptions{
		VMName:        vmName,
		Arch:          arch,
		DetectHostMTU: r.DetectHostMTU,
		ROMExists:     r.ROMExists,
	})
	if err != nil {
		return VM{}, err
	}
	filesystems, err := ParseFilesystems(env, FilesystemParseOptions{})
	if err != nil {
		return VM{}, err
	}

	persistDefault := r.Layout.DataDir != ""
	persist, _ := env.Bool("PERSIST", persistDefault)
	forceISO, _ := env.Bool("FORCE_ISO", false)
	redfishEnabled, _ := env.Bool("REDFISH_ENABLE", false)
	bootMode, err := resolveBootModeSetting(env.Get("BOOT_MODE", "uefi"))
	if err != nil {
		return VM{}, err
	}
	tpmEnabled := bootMode == "secure"
	if _, ok := env.Lookup("TPM"); ok {
		tpmEnabled, _ = env.Bool("TPM", false)
	}
	machineType, err := resolveMachineType(env.Get("MACHINE", ""), arch)
	if err != nil {
		return VM{}, err
	}
	diskController, err := resolveChoice("DISK_TYPE", env.Get("DISK_TYPE", "virtio"), DiskControllers)
	if err != nil {
		return VM{}, err
	}
	diskIO, err := resolveChoice("DISK_IO", env.Get("DISK_IO", "native"), DiskIOModes)
	if err != nil {
		return VM{}, err
	}
	diskCache, err := resolveChoice("DISK_CACHE", env.Get("DISK_CACHE", "none"), DiskCacheModes)
	if err != nil {
		return VM{}, err
	}
	diskPreallocate, _ := env.Bool("ALLOCATE", false)
	extraDisks, err := resolveExtraDisks(env, diskController)
	if err != nil {
		return VM{}, err
	}
	blockDevices, err := r.resolveBlockDevices(env)
	if err != nil {
		return VM{}, err
	}
	gpu, err := resolveGPU(env.Get("GPU", "off"))
	if err != nil {
		return VM{}, err
	}
	usb, _ := env.Bool("USB", true)
	hyperv, _ := env.Bool("HYPERV", false)
	balloon, _ := env.Bool("BALLOON", true)
	rng, _ := env.Bool("RNG", true)
	ioThread, _ := env.Bool("IO_THREAD", true)
	requireKVM, _ := env.Bool("REQUIRE_KVM", false)
	downloadRetries, err := env.Int("DOWNLOAD_RETRIES", "3", 1, ptr(10))
	if err != nil {
		return VM{}, err
	}
	loginShell := strings.TrimSpace(env.Get("SHELL", ""))
	if loginShell == "" {
		loginShell = distroInfo.Shell
	}
	if loginShell == "" {
		loginShell = "/bin/bash"
	}

	activePorts := map[string]int{"SSH_PORT": sshPort}
	if graphicsType == "vnc" || novncEnabled {
		activePorts["VNC_PORT"] = vncPort
	}
	if novncEnabled {
		activePorts["NOVNC_PORT"] = novncPort
	}
	if redfishEnabled {
		activePorts["REDFISH_PORT"] = redfishPort
	}
	for _, forward := range networkConfig.PortForwards {
		activePorts[fmt.Sprintf("PORT_FWD(%d:%d)", forward.HostPort, forward.GuestPort)] = forward.HostPort
	}
	if err := checkPortConflicts(activePorts); err != nil {
		return VM{}, err
	}

	distroName := distroInfo.Name
	if isoRequested {
		distroName = "Custom ISO"
	}
	loginUser := distroInfo.User
	if loginUser == "" {
		loginUser = "user"
	}

	return VM{
		Distro:                distro,
		ImageURL:              distroInfo.URL,
		LoginUser:             loginUser,
		ImageFormat:           "qcow2",
		DistroName:            distroName,
		MemoryMB:              memoryMB,
		CPUs:                  cpus,
		DiskSize:              diskSize,
		Display:               display,
		GraphicsType:          graphicsType,
		Arch:                  arch,
		CPUModel:              env.Get("CPU_MODEL", "host"),
		ExtraArgs:             env.Get("EXTRA_ARGS", ""),
		NoVNCEnabled:          novncEnabled,
		VNCPort:               vncPort,
		VNCKeymap:             strings.TrimSpace(env.Get("VNC_KEYMAP", "")),
		NoVNCPort:             novncPort,
		BootFrom:              bootFrom,
		BlankWorkDisk:         blankDisk,
		BootOrder:             bootOrder,
		CloudInitEnabled:      cloudInit.Enabled,
		CloudInitUserDataPath: cloudInit.UserDataPath,
		Password:              env.Get("GUEST_PASSWORD", "password"),
		SSHPort:               sshPort,
		VMName:                vmName,
		Persist:               persist,
		ForceISO:              forceISO,
		SSHPubkey:             env.Get("SSH_PUBKEY", ""),
		RedfishUser:           env.Get("REDFISH_USERNAME", "admin"),
		RedfishPassword:       env.Get("REDFISH_PASSWORD", "password"),
		RedfishPort:           redfishPort,
		RedfishSystemID:       env.Get("REDFISH_SYSTEM_ID", vmName),
		RedfishEnabled:        redfishEnabled,
		NICs:                  networkConfig.NICs,
		PortForwards:          networkConfig.PortForwards,
		Filesystems:           filesystems,
		BootMode:              bootMode,
		TPMEnabled:            tpmEnabled,
		MachineType:           machineType,
		ExtraDisks:            extraDisks,
		BlockDevices:          blockDevices,
		DiskController:        diskController,
		DiskPreallocate:       diskPreallocate,
		DiskIO:                diskIO,
		DiskCache:             diskCache,
		IOThread:              ioThread,
		BalloonEnabled:        balloon,
		RNGEnabled:            rng,
		USBController:         usb,
		HyperVEnabled:         hyperv,
		GPUPassthrough:        gpu,
		RequireKVM:            requireKVM,
		LoginShell:            loginShell,
		DownloadRetries:       downloadRetries,
	}, nil
}

func (r *Resolver) setDefaults() {
	if r.DistroConfigPath == "" {
		r.DistroConfigPath = paths.DefaultConfigPath
	}
	if r.AvailableMemoryBytes == nil {
		r.AvailableMemoryBytes = func() int64 { return 0 }
	}
	if r.CPUCount == nil {
		r.CPUCount = func() int { return 1 }
	}
	if r.AvailableDiskBytes == nil {
		r.AvailableDiskBytes = func(string) int64 { return 0 }
	}
	if r.DetectHostMTU == nil {
		r.DetectHostMTU = func() int { return 1500 }
	}
	if r.ROMExists == nil {
		r.ROMExists = func(string) bool { return false }
	}
	if r.IsBlockDevice == nil {
		r.IsBlockDevice = func(string) bool { return false }
	}
}

func (r *Resolver) resolveMemory(raw string) (int, error) {
	if isSpecialResource(raw) {
		value, err := units.ParseResourceSize(raw, units.MemoryResource, r.resourceProbe())
		return int(value), err
	}
	return strconvAtoiEnv("MEMORY", raw, 1, nil)
}

func (r *Resolver) resolveCPUs(raw string) (int, error) {
	if isSpecialResource(raw) {
		value, err := units.ParseResourceSize(raw, units.CPUResource, r.resourceProbe())
		return int(value), err
	}
	return strconvAtoiEnv("CPUS", raw, 1, nil)
}

func (r *Resolver) resolveDiskSize(raw string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(raw), "max") || strings.EqualFold(strings.TrimSpace(raw), "half") {
		available := r.AvailableDiskBytes(r.Layout.VMImagesDir)
		diskBytes := available / 2
		if strings.EqualFold(strings.TrimSpace(raw), "max") {
			diskBytes = int64(float64(available) * 0.9)
		}
		gb := diskBytes / (1024 * 1024 * 1024)
		if gb < 1 {
			gb = 1
		}
		return fmt.Sprintf("%dG", gb), nil
	}
	if err := units.ValidateDiskSize(raw); err != nil {
		return "", err
	}
	return raw, nil
}

func (r *Resolver) resourceProbe() units.ResourceProbe {
	return units.ResourceProbe{
		AvailableMemoryBytes: r.AvailableMemoryBytes,
		CPUCount:             r.CPUCount,
	}
}

type cloudInitResolution struct {
	Enabled      bool
	UserDataPath string
}

func (r *Resolver) resolveCloudInit(env MapEnv, isoRequested bool) (cloudInitResolution, error) {
	enabled := true
	if raw, ok := env.Lookup("CLOUD_INIT"); ok {
		enabled = Truthy[strings.ToLower(strings.TrimSpace(raw))]
	} else if isoRequested {
		enabled = false
	}
	userDataPath := strings.TrimSpace(env.Get("CLOUD_INIT_USER_DATA", ""))
	if userDataPath == "" {
		return cloudInitResolution{Enabled: enabled}, nil
	}
	if r.FileExists != nil && !r.FileExists(userDataPath) {
		return cloudInitResolution{}, fmt.Errorf("CLOUD_INIT_USER_DATA file not found: %s", userDataPath)
	}
	if r.IsFile != nil && !r.IsFile(userDataPath) {
		return cloudInitResolution{}, fmt.Errorf("CLOUD_INIT_USER_DATA must point to a regular file: %s", userDataPath)
	}
	if r.ReadFile != nil {
		content, err := r.ReadFile(userDataPath)
		if err != nil {
			return cloudInitResolution{}, fmt.Errorf("cannot read CLOUD_INIT_USER_DATA: %w", err)
		}
		firstLine := strings.TrimSpace(strings.SplitN(string(content), "\n", 2)[0])
		if firstLine == "#cloud-config" {
			var parsed any
			if err := yaml.Unmarshal(content, &parsed); err != nil {
				return cloudInitResolution{}, fmt.Errorf("CLOUD_INIT_USER_DATA contains invalid YAML: %w", err)
			}
		}
	}
	return cloudInitResolution{Enabled: enabled, UserDataPath: userDataPath}, nil
}

func resolveGraphics(display string) (string, bool, error) {
	switch display {
	case "none":
		return "none", false, nil
	case "vnc":
		return "vnc", false, nil
	case "novnc":
		return "vnc", true, nil
	default:
		return "", false, fmt.Errorf("Invalid GRAPHICS %q. Supported: none, novnc, vnc", display)
	}
}

func resolveBootOrder(raw string, isoRequested bool) ([]string, error) {
	valid := map[string]bool{"hd": true, "cdrom": true, "network": true}
	var order []string
	for _, part := range strings.Split(raw, ",") {
		dev := strings.ToLower(strings.TrimSpace(part))
		if dev == "" {
			continue
		}
		if !valid[dev] {
			return nil, fmt.Errorf("Unknown BOOT_ORDER device %q. Supported: hd, cdrom, network", dev)
		}
		order = append(order, dev)
	}
	if len(order) == 0 {
		order = []string{"hd"}
	}
	if isoRequested && !contains(order, "cdrom") {
		order = append([]string{"cdrom"}, order...)
	}
	return order, nil
}

func checkPortConflicts(ports map[string]int) error {
	seen := map[int]string{}
	for label, port := range ports {
		if previous, ok := seen[port]; ok {
			return fmt.Errorf("Port conflict: %s=%d collides with %s=%d. Each service needs a unique port", label, port, previous, port)
		}
		seen[port] = label
	}
	return nil
}

func isISOReference(value string) bool {
	cleaned := strings.Split(strings.Split(value, "?")[0], "#")[0]
	return strings.HasSuffix(strings.ToLower(cleaned), ".iso")
}

func isSpecialResource(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	return value == "max" || value == "half"
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func ptr(value int) *int {
	return &value
}

func strconvAtoiEnv(name string, raw string, min int, max *int) (int, error) {
	return IntFrom(func(string) (string, bool) { return raw, true }, name, raw, min, max)
}

func workImageDir(layout paths.Layout) string {
	if layout.VMImagesDir != "" {
		return layout.VMImagesDir
	}
	return filepath.Join(layout.ImagesDir, "vms")
}

var DiskControllers = map[string]bool{
	"virtio": true,
	"scsi":   true,
	"nvme":   true,
	"ide":    true,
	"usb":    true,
}

var DiskIOModes = map[string]bool{
	"native":   true,
	"threads":  true,
	"io_uring": true,
}

var DiskCacheModes = map[string]bool{
	"none":         true,
	"writeback":    true,
	"writethrough": true,
	"directsync":   true,
	"unsafe":       true,
}

func resolveBootModeSetting(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		value = "uefi"
	}
	if value != "legacy" && value != "uefi" && value != "secure" {
		return "", fmt.Errorf("Invalid BOOT_MODE %q. Supported: legacy, uefi, secure", raw)
	}
	return value, nil
}

func resolveMachineType(raw string, arch string) (string, error) {
	if strings.TrimSpace(raw) != "" {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value != "q35" && value != "pc" {
			return "", fmt.Errorf("Invalid MACHINE %q. Supported: q35, pc", raw)
		}
		return value, nil
	}
	if profile, ok := SupportedArchitectures[arch]; ok && profile.Machine != "" {
		return profile.Machine, nil
	}
	return "q35", nil
}

func resolveChoice(name string, raw string, allowed map[string]bool) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if !allowed[value] {
		return "", fmt.Errorf("Invalid %s %q. Supported: %s", name, raw, sortedKeys(allowed))
	}
	return value, nil
}

func resolveExtraDisks(env MapEnv, controller string) ([]Disk, error) {
	var disks []Disk
	for index := 2; index <= 6; index++ {
		key := fmt.Sprintf("DISK%d_SIZE", index)
		raw := strings.TrimSpace(env.Get(key, ""))
		if raw == "" {
			continue
		}
		if err := units.ValidateDiskSize(raw); err != nil {
			return nil, fmt.Errorf("invalid %s: %w", key, err)
		}
		disks = append(disks, Disk{Size: raw, Index: index, Controller: controller})
	}
	return disks, nil
}

func (r *Resolver) resolveBlockDevices(env MapEnv) ([]BlockDevice, error) {
	var devices []BlockDevice
	for index := 1; index <= 6; index++ {
		key := "DEVICE"
		if index != 1 {
			key = fmt.Sprintf("DEVICE%d", index)
		}
		path := strings.TrimSpace(env.Get(key, ""))
		if path == "" {
			continue
		}
		if r.FileExists != nil && !r.FileExists(path) {
			return nil, fmt.Errorf("%s=%s does not exist", key, path)
		}
		if !r.IsBlockDevice(path) {
			return nil, fmt.Errorf("%s=%s is not a block device", key, path)
		}
		devices = append(devices, BlockDevice{Path: path, Index: index})
	}
	return devices, nil
}

func resolveGPU(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		value = "off"
	}
	if value != "off" && value != "intel" {
		return "", fmt.Errorf("Invalid GPU %q. Supported: off, intel", raw)
	}
	return value, nil
}

func sortedKeys(values map[string]bool) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
