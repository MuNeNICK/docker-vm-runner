package hostinfo

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
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
	return Info{
		CPUModel:       firstCPUModel(readFile("/proc/cpuinfo")),
		CPUCount:       runtime.NumCPU(),
		MemTotalBytes:  memTotal,
		MemAvailBytes:  memAvail,
		DiskAvailBytes: diskAvailable(diskPath),
		DiskPath:       diskPath,
		KVMAvailable:   fileExists("/dev/kvm"),
		Kernel:         kernelRelease(),
	}
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

func diskAvailable(path string) uint64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return stat.Bavail * uint64(stat.Bsize)
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
