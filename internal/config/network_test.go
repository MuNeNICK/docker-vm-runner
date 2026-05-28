package config

import (
	"strings"
	"testing"
)

func TestParseNetworkDefaultPrimaryNIC(t *testing.T) {
	cfg, err := ParseNetwork(MapEnv{}, NetworkParseOptions{
		VMName:        "ubuntu-24.04-server",
		DetectHostMTU: func() int { return 1500 },
		ROMExists:     func(string) bool { return false },
	})
	if err != nil {
		t.Fatalf("ParseNetwork returned error: %v", err)
	}
	if len(cfg.NICs) != 1 {
		t.Fatalf("NIC count = %d", len(cfg.NICs))
	}
	nic := cfg.NICs[0]
	if nic.Mode != "user" {
		t.Fatalf("Mode = %q", nic.Mode)
	}
	if nic.Model != "virtio" {
		t.Fatalf("Model = %q", nic.Model)
	}
	if nic.MACAddress == "" {
		t.Fatal("MACAddress is empty")
	}
	if nic.MTU != nil {
		t.Fatalf("MTU = %d", *nic.MTU)
	}
}

func TestParseNetworkAutoDetectsPrimaryMTU(t *testing.T) {
	cfg, err := ParseNetwork(MapEnv{}, NetworkParseOptions{
		VMName:        "ubuntu-24.04-server",
		DetectHostMTU: func() int { return 9000 },
		ROMExists:     func(string) bool { return false },
	})
	if err != nil {
		t.Fatalf("ParseNetwork returned error: %v", err)
	}
	if cfg.NICs[0].MTU == nil || *cfg.NICs[0].MTU != 9000 {
		t.Fatalf("MTU = %#v", cfg.NICs[0].MTU)
	}
}

func TestParseNetworkBridgeRequiresBridgeName(t *testing.T) {
	_, err := ParseNetwork(MapEnv{"NETWORK_MODE": "bridge"}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "NETWORK_BRIDGE is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseNetworkBridgeNIC(t *testing.T) {
	cfg, err := ParseNetwork(MapEnv{
		"NETWORK_MODE":   "bridge",
		"NETWORK_BRIDGE": "br0",
	}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err != nil {
		t.Fatalf("ParseNetwork returned error: %v", err)
	}
	if cfg.NICs[0].Mode != "bridge" {
		t.Fatalf("Mode = %q", cfg.NICs[0].Mode)
	}
	if cfg.NICs[0].BridgeName != "br0" {
		t.Fatalf("BridgeName = %q", cfg.NICs[0].BridgeName)
	}
}

func TestParseNetworkDirectRequiresDevice(t *testing.T) {
	_, err := ParseNetwork(MapEnv{"NETWORK_MODE": "direct"}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "NETWORK_DIRECT_DEV is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseNetworkSecondaryNIC(t *testing.T) {
	cfg, err := ParseNetwork(MapEnv{
		"NETWORK2_MODE":       "direct",
		"NETWORK2_DIRECT_DEV": "eth1",
	}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err != nil {
		t.Fatalf("ParseNetwork returned error: %v", err)
	}
	if len(cfg.NICs) != 2 {
		t.Fatalf("NIC count = %d", len(cfg.NICs))
	}
	if cfg.NICs[1].Mode != "direct" || cfg.NICs[1].DirectDevice != "eth1" {
		t.Fatalf("secondary NIC = %#v", cfg.NICs[1])
	}
}

func TestParseNetworkInvalidMode(t *testing.T) {
	_, err := ParseNetwork(MapEnv{"NETWORK_MODE": "bogus"}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Unsupported NETWORK_MODE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseNetworkMACValidation(t *testing.T) {
	_, err := ParseNetwork(MapEnv{"NETWORK_MAC": "not-a-mac"}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Invalid NETWORK_MAC") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseNetworkModelValidation(t *testing.T) {
	_, err := ParseNetwork(MapEnv{"NETWORK_MODEL": "badmodel"}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Unsupported NETWORK_MODEL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseNetworkGuestIPv4(t *testing.T) {
	cfg, err := ParseNetwork(MapEnv{
		"NETWORK_GUEST_IP": "10.0.2.15/24,192.168.1.100/16",
	}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err != nil {
		t.Fatalf("ParseNetwork returned error: %v", err)
	}
	got := cfg.NICs[0].GuestIPv4
	if len(got) != 2 {
		t.Fatalf("GuestIPv4 count = %d", len(got))
	}
	if got[0].Address != "10.0.2.15" || got[0].Prefix != 24 {
		t.Fatalf("first IPv4 = %#v", got[0])
	}
	if got[1].Address != "192.168.1.100" || got[1].Prefix != 16 {
		t.Fatalf("second IPv4 = %#v", got[1])
	}
}

func TestParseNetworkGuestIPv6(t *testing.T) {
	cfg, err := ParseNetwork(MapEnv{
		"NETWORK_GUEST_IP6": "fec0::2/64,fd00::1/128",
	}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err != nil {
		t.Fatalf("ParseNetwork returned error: %v", err)
	}
	got := cfg.NICs[0].GuestIPv6
	if len(got) != 2 {
		t.Fatalf("GuestIPv6 count = %d", len(got))
	}
	if got[0].Address != "fec0::2" || got[0].Prefix != 64 {
		t.Fatalf("first IPv6 = %#v", got[0])
	}
	if got[1].Address != "fd00::1" || got[1].Prefix != 128 {
		t.Fatalf("second IPv6 = %#v", got[1])
	}
}

func TestParseNetworkGuestIPValidation(t *testing.T) {
	tests := []struct {
		name string
		env  MapEnv
		want string
	}{
		{name: "invalid IPv4", env: MapEnv{"NETWORK_GUEST_IP": "not-an-ip"}, want: "must be a valid IPv4 address"},
		{name: "invalid IPv4 prefix", env: MapEnv{"NETWORK_GUEST_IP": "10.0.2.15/abc"}, want: "must be an integer"},
		{name: "IPv4 prefix out of range", env: MapEnv{"NETWORK_GUEST_IP": "10.0.2.15/0"}, want: "must be between 1 and 32"},
		{name: "invalid IPv6", env: MapEnv{"NETWORK_GUEST_IP6": "not-ipv6"}, want: "must be a valid IPv6 address"},
		{name: "IPv6 prefix out of range", env: MapEnv{"NETWORK_GUEST_IP6": "fd00::1/0"}, want: "must be between 1 and 128"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseNetwork(tt.env, NetworkParseOptions{
				VMName:        "vm",
				DetectHostMTU: func() int { return 1500 },
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseNetworkPortForwards(t *testing.T) {
	cfg, err := ParseNetwork(MapEnv{"PORT_FWD": "8080:80,8443:443"}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err != nil {
		t.Fatalf("ParseNetwork returned error: %v", err)
	}
	if len(cfg.PortForwards) != 2 {
		t.Fatalf("PortForwards count = %d", len(cfg.PortForwards))
	}
	if cfg.PortForwards[0].HostPort != 8080 || cfg.PortForwards[0].GuestPort != 80 {
		t.Fatalf("first forward = %#v", cfg.PortForwards[0])
	}
}

func TestParseNetworkInvalidPortForward(t *testing.T) {
	_, err := ParseNetwork(MapEnv{"PORT_FWD": "invalid"}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Invalid PORT_FWD entry") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseNetworkIPXEOverride(t *testing.T) {
	rom := "/tmp/ipxe.rom"
	cfg, err := ParseNetwork(MapEnv{
		"IPXE_ENABLE":   "1",
		"IPXE_ROM_PATH": rom,
	}, NetworkParseOptions{
		VMName:        "vm",
		Arch:          "x86_64",
		DetectHostMTU: func() int { return 1500 },
		ROMExists:     func(path string) bool { return path == rom },
	})
	if err != nil {
		t.Fatalf("ParseNetwork returned error: %v", err)
	}
	if !cfg.IPXEEnabled {
		t.Fatal("IPXEEnabled is false")
	}
	if cfg.IPXEROMPath != rom {
		t.Fatalf("IPXEROMPath = %q", cfg.IPXEROMPath)
	}
	if !cfg.NICs[0].Boot {
		t.Fatal("primary NIC boot flag is false")
	}
}

func TestParseNetworkIPXERequiresROM(t *testing.T) {
	_, err := ParseNetwork(MapEnv{"IPXE_ENABLE": "1", "NETWORK_MODEL": "virtio"}, NetworkParseOptions{
		VMName:        "vm",
		Arch:          "ppc64",
		DetectHostMTU: func() int { return 1500 },
		ROMExists:     func(string) bool { return false },
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "requires IPXE_ROM_PATH") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseNetworkRejectsInvalidBooleanEnv(t *testing.T) {
	_, err := ParseNetwork(MapEnv{"IPXE_ENABLE": "ture"}, NetworkParseOptions{
		VMName:        "vm",
		DetectHostMTU: func() int { return 1500 },
	})
	if err == nil {
		t.Fatal("expected boolean error")
	}
	if !strings.Contains(err.Error(), "IPXE_ENABLE must be a boolean") {
		t.Fatalf("unexpected error: %v", err)
	}
}
