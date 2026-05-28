package config

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/munenick/docker-vm-runner/internal/netutil"
	"github.com/munenick/docker-vm-runner/internal/network"
)

var macAddressPattern = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

type NetworkConfig struct {
	NICs         []network.Config
	PortForwards []network.PortForward
	IPXEEnabled  bool
	IPXEROMPath  string
	Warnings     []string
}

type NetworkParseOptions struct {
	VMName        string
	Arch          string
	DetectHostMTU func() int
	ROMExists     func(string) bool
}

func ParseNetwork(env MapEnv, opts NetworkParseOptions) (NetworkConfig, error) {
	if opts.DetectHostMTU == nil {
		opts.DetectHostMTU = func() int { return 1500 }
	}
	if opts.ROMExists == nil {
		opts.ROMExists = func(string) bool { return false }
	}
	if opts.Arch == "" {
		opts.Arch = "x86_64"
	}

	var nics []network.Config
	primary, err := parseNIC(env, opts, 1)
	if err != nil {
		return NetworkConfig{}, err
	}
	nics = append(nics, primary)

	for index := 2; ; index++ {
		nic, ok, err := parseOptionalNIC(env, opts, index)
		if err != nil {
			return NetworkConfig{}, err
		}
		if !ok {
			break
		}
		nics = append(nics, nic)
	}

	portForwards, err := parsePortForwards(env.Get("PORT_FWD", ""))
	if err != nil {
		return NetworkConfig{}, err
	}

	result := NetworkConfig{NICs: nics, PortForwards: portForwards}
	ipxe, err := env.Bool("IPXE_ENABLE", false)
	if err != nil {
		return NetworkConfig{}, err
	}
	if ipxe {
		rom, err := resolveIPXEROM(env, opts, result.NICs[0].Model)
		if err != nil {
			return NetworkConfig{}, err
		}
		result.IPXEEnabled = true
		result.IPXEROMPath = rom
		result.NICs[0].Boot = true
		if result.NICs[0].Mode == "user" {
			result.Warnings = append(result.Warnings, "IPXE_ENABLE=1 with NETWORK_MODE=nat relies on user-mode DHCP/TFTP; for real PXE environments prefer bridge or direct networking")
		}
	}
	return result, nil
}

func parseOptionalNIC(env MapEnv, opts NetworkParseOptions, index int) (network.Config, bool, error) {
	modeRaw, ok := lookupIndexed(env, "NETWORK_MODE", index)
	if !ok || strings.TrimSpace(modeRaw) == "" {
		return network.Config{}, false, nil
	}
	nic, err := parseNIC(env, opts, index)
	return nic, true, err
}

func parseNIC(env MapEnv, opts NetworkParseOptions, index int) (network.Config, error) {
	modeRaw, ok := lookupIndexed(env, "NETWORK_MODE", index)
	if !ok || strings.TrimSpace(modeRaw) == "" {
		if index == 1 {
			modeRaw = "nat"
		} else {
			return network.Config{}, nil
		}
	}

	mode, err := normalizeNetworkMode(modeRaw)
	if err != nil {
		return network.Config{}, indexedError("Unsupported NETWORK%s_MODE %q. Expected one of nat, bridge, direct.", index, modeRaw)
	}

	nic := network.Config{Mode: mode}
	if mode == "bridge" {
		bridge, ok := lookupIndexed(env, "NETWORK_BRIDGE", index)
		if !ok || strings.TrimSpace(bridge) == "" {
			return network.Config{}, indexedError("NETWORK%s_BRIDGE is required when NETWORK%s_MODE=bridge", index, index)
		}
		nic.BridgeName = strings.TrimSpace(bridge)
	}
	if mode == "direct" {
		device, ok := lookupIndexed(env, "NETWORK_DIRECT_DEV", index)
		if !ok || strings.TrimSpace(device) == "" {
			return network.Config{}, indexedError("NETWORK%s_DIRECT_DEV is required when NETWORK%s_MODE=direct", index, index)
		}
		nic.DirectDevice = strings.TrimSpace(device)
	}

	mac, ok := lookupIndexed(env, "NETWORK_MAC", index)
	if ok && strings.TrimSpace(mac) != "" {
		normalized := strings.ToLower(strings.TrimSpace(mac))
		if !macAddressPattern.MatchString(normalized) {
			return network.Config{}, indexedError("Invalid NETWORK%s_MAC %q. Use format aa:bb:cc:dd:ee:ff", index, mac)
		}
		nic.MACAddress = normalized
	} else {
		nic.MACAddress = netutil.DeterministicMAC(fmt.Sprintf("%s:%d", opts.VMName, index))
	}

	model, ok := lookupIndexed(env, "NETWORK_MODEL", index)
	if ok && strings.TrimSpace(model) != "" {
		nic.Model = strings.ToLower(strings.TrimSpace(model))
	} else {
		nic.Model = "virtio"
	}
	if !SupportedNetworkModels[nic.Model] {
		return network.Config{}, indexedError("Unsupported NETWORK%s_MODEL %q. Supported: %s", index, nic.Model, supportedNetworkModels())
	}

	if mtuRaw, ok := lookupIndexed(env, "NETWORK_MTU", index); ok && strings.TrimSpace(mtuRaw) != "" {
		mtu, err := strconv.Atoi(strings.TrimSpace(mtuRaw))
		if err != nil {
			return network.Config{}, indexedError("Invalid NETWORK%s_MTU %q: must be an integer", index, mtuRaw)
		}
		nic.MTU = &mtu
	} else if index == 1 {
		mtu := opts.DetectHostMTU()
		if mtu != 1500 {
			nic.MTU = &mtu
		}
	}

	ipv4, err := parseGuestAddresses(lookupIndexedValue(env, "NETWORK_GUEST_IP", index), 4, index)
	if err != nil {
		return network.Config{}, err
	}
	ipv6, err := parseGuestAddresses(lookupIndexedValue(env, "NETWORK_GUEST_IP6", index), 6, index)
	if err != nil {
		return network.Config{}, err
	}
	nic.GuestIPv4 = ipv4
	nic.GuestIPv6 = ipv6

	if bootRaw, ok := lookupIndexed(env, "NETWORK_BOOT", index); ok {
		boot, err := BoolValue(indexedName("NETWORK_BOOT", index), bootRaw)
		if err != nil {
			return network.Config{}, err
		}
		nic.Boot = boot
	}
	return nic, nil
}

func normalizeNetworkMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "nat":
		return "user", nil
	case "bridge":
		return "bridge", nil
	case "direct":
		return "direct", nil
	default:
		return "", fmt.Errorf("unsupported mode")
	}
}

func lookupIndexed(env MapEnv, base string, index int) (string, bool) {
	if index == 1 {
		return env.Lookup(base)
	}
	prefix, suffix, ok := strings.Cut(base, "_")
	if !ok {
		return env.Lookup(fmt.Sprintf("%s%d", base, index))
	}
	return env.Lookup(fmt.Sprintf("%s%d_%s", prefix, index, suffix))
}

func lookupIndexedValue(env MapEnv, base string, index int) string {
	value, _ := lookupIndexed(env, base, index)
	return value
}

func parseGuestAddresses(raw string, family int, index int) ([]network.GuestAddress, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	envName := indexedName("NETWORK_GUEST_IP", index)
	if family == 6 {
		envName = indexedName("NETWORK_GUEST_IP6", index)
	}
	defaultPrefix := 24
	maxPrefix := 32
	if family == 6 {
		defaultPrefix = 64
		maxPrefix = 128
	}

	var result []network.GuestAddress
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		address := entry
		prefix := defaultPrefix
		if before, after, ok := strings.Cut(entry, "/"); ok {
			address = strings.TrimSpace(before)
			parsedPrefix, err := strconv.Atoi(strings.TrimSpace(after))
			if err != nil {
				return nil, fmt.Errorf("Invalid %s prefix '/%s': must be an integer", envName, after)
			}
			if parsedPrefix < 1 || parsedPrefix > maxPrefix {
				return nil, fmt.Errorf("Invalid %s prefix '/%d': must be between 1 and %d", envName, parsedPrefix, maxPrefix)
			}
			prefix = parsedPrefix
		}
		ip := net.ParseIP(address)
		if family == 4 {
			if ip == nil || ip.To4() == nil {
				return nil, fmt.Errorf("Invalid %s %q: must be a valid IPv4 address", envName, address)
			}
		} else if ip == nil || ip.To4() != nil {
			return nil, fmt.Errorf("Invalid %s %q: must be a valid IPv6 address", envName, address)
		}
		result = append(result, network.GuestAddress{Address: address, Prefix: prefix})
	}
	return result, nil
}

func parsePortForwards(raw string) ([]network.PortForward, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var forwards []network.PortForward
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		hostRaw, guestRaw, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("Invalid PORT_FWD entry %q: expected format host_port:guest_port", entry)
		}
		host, err := parsePort(hostRaw)
		if err != nil {
			return nil, fmt.Errorf("Invalid PORT_FWD entry %q: %w", entry, err)
		}
		guest, err := parsePort(guestRaw)
		if err != nil {
			return nil, fmt.Errorf("Invalid PORT_FWD entry %q: %w", entry, err)
		}
		forwards = append(forwards, network.PortForward{HostPort: host, GuestPort: guest})
	}
	return forwards, nil
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("ports must be integers")
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %d out of range (1-65535)", port)
	}
	return port, nil
}

func resolveIPXEROM(env MapEnv, opts NetworkParseOptions, model string) (string, error) {
	if override := strings.TrimSpace(env.Get("IPXE_ROM_PATH", "")); override != "" {
		if !opts.ROMExists(override) {
			return "", fmt.Errorf("iPXE ROM not found at %s", override)
		}
		return override, nil
	}
	if byModel, ok := IPXEDefaultROMs[opts.Arch]; ok {
		if rom := byModel[model]; rom != "" {
			if opts.ROMExists(rom) {
				return rom, nil
			}
			return "", fmt.Errorf("iPXE ROM not found at %s", rom)
		}
	}
	return "", fmt.Errorf("IPXE_ENABLE=1 requires IPXE_ROM_PATH when a default ROM is not available for ARCH=%q with NETWORK_MODEL=%q", opts.Arch, model)
}

func indexedError(format string, index int, args ...any) error {
	suffix := ""
	if index != 1 {
		suffix = strconv.Itoa(index)
	}
	replaced := strings.ReplaceAll(format, "%s", suffix)
	return fmt.Errorf(replaced, args...)
}

func indexedName(base string, index int) string {
	if index == 1 {
		return base
	}
	prefix, suffix, ok := strings.Cut(base, "_")
	if !ok {
		return fmt.Sprintf("%s%d", base, index)
	}
	return fmt.Sprintf("%s%d_%s", prefix, index, suffix)
}

func supportedNetworkModels() string {
	models := make([]string, 0, len(SupportedNetworkModels))
	for model := range SupportedNetworkModels {
		models = append(models, model)
	}
	sort.Strings(models)
	return strings.Join(models, ", ")
}
