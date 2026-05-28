package hostinfo

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type Info struct {
	CPUModel        string
	CPUCount        int
	MemTotalBytes   uint64
	MemAvailBytes   uint64
	DiskAvailBytes  uint64
	DiskPath        string
	KVMAvailable    bool
	Kernel          string
	RuntimeEngine   string
	RuntimeRootless bool
	RuntimePriv     bool
}

func Detect(diskPath string) Info {
	if diskPath == "" {
		diskPath = "/"
	}
	memTotal, memAvail := parseMemInfo(readFile("/proc/meminfo"))
	runtimeEngine := runtimeEngineFrom(readFile("/proc/1/cgroup"), fileExists("/.dockerenv"), fileExists("/run/.containerenv") || fileExists("/var/run/.containerenv"))
	return Info{
		CPUModel:        firstCPUModel(readFile("/proc/cpuinfo")),
		CPUCount:        runtime.NumCPU(),
		MemTotalBytes:   memTotal,
		MemAvailBytes:   memAvail,
		DiskAvailBytes:  diskAvailable(diskPath),
		DiskPath:        diskPath,
		KVMAvailable:    KVMAvailable(),
		Kernel:          kernelRelease(),
		RuntimeEngine:   runtimeEngine,
		RuntimeRootless: runtimeRootlessFromUIDMap(readFile("/proc/self/uid_map"), os.Geteuid()),
		RuntimePriv:     runtimePrivilegedFromStatus(readFile("/proc/self/status")),
	}
}

func AvailableMemoryBytes() int64 {
	_, available := parseMemInfo(readFile("/proc/meminfo"))
	return int64(available)
}

func CPUCount() int {
	return runtime.NumCPU()
}

func CPUVendor() string {
	return cpuVendor(readFile("/proc/cpuinfo"))
}

func CPUFlags() map[string]bool {
	return cpuFlags(readFile("/proc/cpuinfo"))
}

func AvailableDiskBytes(path string) int64 {
	return int64(diskAvailable(path))
}

func DetectHostMTU() int {
	interfaces, err := net.Interfaces()
	if err != nil {
		return 1500
	}
	mtu := 0
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.MTU <= 0 {
			continue
		}
		if iface.MTU > mtu {
			mtu = iface.MTU
		}
	}
	if mtu == 0 {
		return 1500
	}
	return mtu
}

func FileExists(path string) bool {
	return fileExists(path)
}

func IsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func IsBlockDevice(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	mode := info.Mode()
	return mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0
}

func IsMount(path string) bool {
	return isMount(path, "/proc/self/mountinfo", os.ReadFile)
}

func KVMAvailable() bool {
	return kvmAvailable("/dev/kvm", os.Open)
}

func kvmAvailable(path string, open func(string) (*os.File, error)) bool {
	file, err := open(path)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func NativeDiskIOUnsafe(path string) bool {
	fsType, ok := filesystemType(path)
	if !ok {
		return false
	}
	return fsType == 0x01021994 || fsType == 0xf15f
}

func BlockSectorSize(path string) (int, bool) {
	return blockSectorSize(path, "/sys/class/block", os.ReadFile)
}

func IPv6Available() bool {
	return ipv6Available("/proc/sys/net/ipv6/conf/all/disable_ipv6", os.ReadFile)
}

func ipv6Available(path string, readFile func(string) ([]byte, error)) bool {
	content, err := readFile(path)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(content)) == "0"
}

func blockSectorSize(path string, sysClassBlock string, readFile func(string) ([]byte, error)) (int, bool) {
	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == "/" || name == "" {
		return 0, false
	}
	content, err := readFile(filepath.Join(sysClassBlock, name, "queue", "logical_block_size"))
	if err != nil {
		return 0, false
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || size <= 0 {
		return 0, false
	}
	return size, true
}

func isMount(path string, mountInfoPath string, readFile func(string) ([]byte, error)) bool {
	cleaned := filepath.Clean(path)
	content, err := readFile(mountInfoPath)
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		if unescapeMountInfoPath(fields[4]) == cleaned {
			return true
		}
	}
	return false
}

func filesystemType(path string) (int64, bool) {
	cleaned := filepath.Clean(path)
	for {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(cleaned, &stat); err == nil {
			return int64(stat.Type), true
		}
		parent := filepath.Dir(cleaned)
		if parent == cleaned {
			return 0, false
		}
		cleaned = parent
	}
}

func unescapeMountInfoPath(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return filepath.Clean(replacer.Replace(value))
}

func Lines(info Info) []string {
	cpu := info.CPUModel
	if cpu == "" {
		cpu = "unknown"
	}
	cores := info.CPUCount
	if cores == 0 {
		cores = runtime.NumCPU()
	}
	lines := []string{
		fmt.Sprintf("CPU:     %s (%d cores)", cpu, cores),
		fmt.Sprintf("Memory:  %.1f GiB free / %.1f GiB total", gib(info.MemAvailBytes), gib(info.MemTotalBytes)),
		fmt.Sprintf("Storage: %.1f GiB free at %s", gib(info.DiskAvailBytes), info.DiskPath),
	}
	if info.KVMAvailable {
		lines = append(lines, "KVM:     available")
	} else {
		lines = append(lines, "KVM:     NOT available (TCG fallback)")
	}
	if info.Kernel != "" {
		lines = append(lines, "Kernel:  "+info.Kernel)
	}
	if info.RuntimeEngine != "" {
		priv := "unprivileged"
		if info.RuntimePriv {
			priv = "privileged"
		}
		rootless := ""
		if info.RuntimeRootless {
			rootless = ", rootless"
		}
		lines = append(lines, fmt.Sprintf("Runtime: %s (%s%s)", info.RuntimeEngine, priv, rootless))
	}
	return lines
}

func parseMemInfo(content []byte) (uint64, uint64) {
	var total uint64
	var available uint64
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value * 1024
		case "MemAvailable":
			available = value * 1024
		}
	}
	return total, available
}

func firstCPUModel(content []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Hardware") {
			_, value, ok := strings.Cut(line, ":")
			if ok {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func cpuVendor(content []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "vendor_id") {
			continue
		}
		_, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		vendor := strings.ToLower(strings.TrimSpace(value))
		if strings.Contains(vendor, "amd") {
			return "amd"
		}
		if strings.Contains(vendor, "intel") {
			return "intel"
		}
		if vendor != "" {
			return vendor
		}
	}
	return "unknown"
}

func cpuFlags(content []byte) map[string]bool {
	flags := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "flags") {
			continue
		}
		_, value, ok := strings.Cut(line, ":")
		if !ok {
			return flags
		}
		for _, flag := range strings.Fields(value) {
			flags[flag] = true
		}
		return flags
	}
	return flags
}

func runtimeEngineFrom(cgroup []byte, dockerEnv bool, containerEnv bool) string {
	text := strings.ToLower(string(cgroup))
	switch {
	case strings.Contains(text, "docker"):
		return "docker"
	case strings.Contains(text, "libpod"), strings.Contains(text, "podman"):
		return "podman"
	case strings.Contains(text, "containerd"):
		return "containerd"
	case strings.Contains(text, "kubepods"):
		return "kubernetes"
	case dockerEnv:
		return "docker"
	case containerEnv:
		return "podman"
	default:
		return ""
	}
}

func runtimeRootlessFromUIDMap(content []byte, euid int) bool {
	if euid != 0 {
		return true
	}
	fields := strings.Fields(string(content))
	if len(fields) < 3 {
		return false
	}
	inside, errInside := strconv.ParseUint(fields[0], 10, 64)
	outside, errOutside := strconv.ParseUint(fields[1], 10, 64)
	if errInside != nil || errOutside != nil {
		return false
	}
	return inside == 0 && outside != 0
}

func runtimePrivilegedFromStatus(content []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		_, raw, ok := strings.Cut(line, ":")
		if !ok {
			return false
		}
		caps, err := strconv.ParseUint(strings.TrimSpace(raw), 16, 64)
		if err != nil {
			return false
		}
		const capSysAdmin = 21
		return caps&(1<<capSysAdmin) != 0
	}
	return false
}

func diskAvailable(path string) uint64 {
	path = existingPathForStatfs(path)
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return stat.Bavail * uint64(stat.Bsize)
}

func existingPathForStatfs(path string) string {
	if strings.TrimSpace(path) == "" {
		return "/"
	}
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

func kernelRelease() string {
	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err != nil {
		return ""
	}
	return charsToString(uts.Release[:])
}

func charsToString(chars []int8) string {
	bytes := make([]byte, 0, len(chars))
	for _, ch := range chars {
		if ch == 0 {
			break
		}
		bytes = append(bytes, byte(ch))
	}
	return string(bytes)
}

func readFile(path string) []byte {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return content
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func gib(bytes uint64) float64 {
	return float64(bytes) / (1024 * 1024 * 1024)
}
