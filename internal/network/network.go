package network

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/munenick/docker-vm-runner/internal/netutil"
)

type GuestAddress struct {
	Address string
	Prefix  int
}

type PortForward struct {
	HostPort  int
	GuestPort int
}

type Config struct {
	Mode         string
	BridgeName   string
	DirectDevice string
	MACAddress   string
	Model        string
	Boot         bool
	MTU          *int
	GuestIPv4    []GuestAddress
	GuestIPv6    []GuestAddress
}

type RenderOptions struct {
	SSHPort      int
	MACAddress   string
	BootOrder    int
	ROMFile      string
	PortForwards []PortForward
	IPv6Enabled  bool
}

var (
	defaultIPv4 = GuestAddress{Address: "10.0.2.15", Prefix: 24}
	defaultIPv6 = GuestAddress{Address: "fec0::2", Prefix: 64}
)

func RenderInterface(cfg Config, opts RenderOptions) (string, string, error) {
	model := cfg.Model
	if model == "" {
		model = "virtio"
	}
	mac := strings.ToLower(opts.MACAddress)
	if mac == "" {
		mac = strings.ToLower(cfg.MACAddress)
	}
	if mac == "" {
		mac = netutil.RandomMAC()
	}

	var b strings.Builder
	switch cfg.Mode {
	case "user":
		renderUser(&b, cfg, opts, mac, model)
	case "bridge":
		if cfg.BridgeName == "" {
			return "", "", fmt.Errorf("NETWORK_BRIDGE must be set when NETWORK_MODE=bridge")
		}
		renderBridge(&b, cfg, opts, mac, model)
	case "direct":
		if cfg.DirectDevice == "" {
			return "", "", fmt.Errorf("NETWORK_DIRECT_DEV must be set when NETWORK_MODE=direct")
		}
		renderDirect(&b, cfg, opts, mac, model)
	default:
		return "", "", fmt.Errorf("unsupported network mode: %s", cfg.Mode)
	}
	return b.String(), mac, nil
}

func renderUser(b *strings.Builder, cfg Config, opts RenderOptions, mac string, model string) {
	start(b, "interface", attr{"type", "user"})
	commonLeading(b, opts, mac)
	empty(b, "backend", attr{"type", "passt"})
	ipv4 := cfg.GuestIPv4
	if len(ipv4) == 0 {
		ipv4 = []GuestAddress{defaultIPv4}
	}
	for _, ip := range ipv4 {
		empty(b, "ip", attr{"family", "ipv4"}, attr{"address", ip.Address}, attr{"prefix", strconv.Itoa(ip.Prefix)})
	}
	for _, ip := range cfg.GuestIPv6 {
		empty(b, "ip", attr{"family", "ipv6"}, attr{"address", ip.Address}, attr{"prefix", strconv.Itoa(ip.Prefix)})
	}
	if len(cfg.GuestIPv6) == 0 && opts.IPv6Enabled {
		empty(b, "ip", attr{"family", "ipv6"}, attr{"address", defaultIPv6.Address}, attr{"prefix", strconv.Itoa(defaultIPv6.Prefix)})
	}
	commonTrailing(b, cfg, opts, model)
	if opts.SSHPort != 0 {
		start(b, "portForward", attr{"proto", "tcp"})
		empty(b, "range", attr{"start", strconv.Itoa(opts.SSHPort)}, attr{"to", "22"})
		end(b, "portForward")
	}
	for _, forward := range opts.PortForwards {
		start(b, "portForward", attr{"proto", "tcp"})
		empty(b, "range", attr{"start", strconv.Itoa(forward.HostPort)}, attr{"to", strconv.Itoa(forward.GuestPort)})
		end(b, "portForward")
	}
	end(b, "interface")
}

func renderBridge(b *strings.Builder, cfg Config, opts RenderOptions, mac string, model string) {
	start(b, "interface", attr{"type", "bridge"})
	commonLeading(b, opts, mac)
	if model == "virtio" {
		empty(b, "driver", attr{"name", "vhost"})
	}
	commonTrailing(b, cfg, opts, model)
	empty(b, "source", attr{"bridge", cfg.BridgeName})
	end(b, "interface")
}

func renderDirect(b *strings.Builder, cfg Config, opts RenderOptions, mac string, model string) {
	start(b, "interface", attr{"type", "direct"})
	commonLeading(b, opts, mac)
	if model == "virtio" {
		empty(b, "driver", attr{"name", "vhost"})
	}
	commonTrailing(b, cfg, opts, model)
	empty(b, "source", attr{"dev", cfg.DirectDevice}, attr{"mode", "bridge"})
	end(b, "interface")
}

func commonLeading(b *strings.Builder, opts RenderOptions, mac string) {
	if opts.BootOrder != 0 {
		empty(b, "boot", attr{"order", strconv.Itoa(opts.BootOrder)})
	}
	empty(b, "mac", attr{"address", mac})
}

func commonTrailing(b *strings.Builder, cfg Config, opts RenderOptions, model string) {
	empty(b, "model", attr{"type", model})
	if cfg.MTU != nil && *cfg.MTU != 1500 {
		empty(b, "mtu", attr{"size", strconv.Itoa(*cfg.MTU)})
	}
	if opts.ROMFile != "" {
		empty(b, "rom", attr{"file", opts.ROMFile})
	}
}

type attr struct {
	name  string
	value string
}

func start(b *strings.Builder, name string, attrs ...attr) {
	b.WriteByte('<')
	b.WriteString(name)
	writeAttrs(b, attrs)
	b.WriteByte('>')
}

func end(b *strings.Builder, name string) {
	b.WriteString("</")
	b.WriteString(name)
	b.WriteByte('>')
}

func empty(b *strings.Builder, name string, attrs ...attr) {
	b.WriteByte('<')
	b.WriteString(name)
	writeAttrs(b, attrs)
	b.WriteString("/>")
}

func writeAttrs(b *strings.Builder, attrs []attr) {
	for _, a := range attrs {
		b.WriteByte(' ')
		b.WriteString(a.name)
		b.WriteString(`="`)
		b.WriteString(escapeAttr(a.value))
		b.WriteByte('"')
	}
}

func escapeAttr(value string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(value)); err != nil {
		return value
	}
	return buf.String()
}
