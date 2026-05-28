package network

import (
	"encoding/xml"
	"strings"
	"testing"
)

type xmlElement struct {
	Name  string
	Attrs map[string]string
}

func collectElements(t *testing.T, doc string) []xmlElement {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(doc))
	var out []xmlElement
	for {
		token, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return out
			}
			t.Fatalf("decode XML: %v\n%s", err, doc)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		attrs := make(map[string]string, len(start.Attr))
		for _, attr := range start.Attr {
			attrs[attr.Name.Local] = attr.Value
		}
		out = append(out, xmlElement{Name: start.Name.Local, Attrs: attrs})
	}
}

func hasElement(elements []xmlElement, name string, attrs map[string]string) bool {
	for _, elem := range elements {
		if elem.Name != name {
			continue
		}
		matches := true
		for key, value := range attrs {
			if elem.Attrs[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func requireElement(t *testing.T, doc string, name string, attrs map[string]string) {
	t.Helper()
	if !hasElement(collectElements(t, doc), name, attrs) {
		t.Fatalf("missing <%s> with attrs %#v in:\n%s", name, attrs, doc)
	}
}

func requireNoElement(t *testing.T, doc string, name string) {
	t.Helper()
	for _, elem := range collectElements(t, doc) {
		if elem.Name == name {
			t.Fatalf("unexpected <%s> in:\n%s", name, doc)
		}
	}
}

func TestRenderUserNetwork(t *testing.T) {
	doc, mac, err := RenderInterface(Config{Mode: "user", MACAddress: "52:54:00:aa:bb:cc"}, RenderOptions{})
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	if mac != "52:54:00:aa:bb:cc" {
		t.Fatalf("mac = %q", mac)
	}
	requireElement(t, doc, "interface", map[string]string{"type": "user"})
	requireElement(t, doc, "mac", map[string]string{"address": "52:54:00:aa:bb:cc"})
	requireElement(t, doc, "backend", map[string]string{"type": "passt"})
	requireElement(t, doc, "ip", map[string]string{"family": "ipv4", "address": "10.0.2.15", "prefix": "24"})
	requireElement(t, doc, "model", map[string]string{"type": "virtio"})
}

func TestRenderUserNetworkOptions(t *testing.T) {
	doc, _, err := RenderInterface(
		Config{Mode: "user", MACAddress: "52:54:00:aa:bb:cc"},
		RenderOptions{
			SSHPort:   2222,
			BootOrder: 1,
			ROMFile:   "/usr/share/qemu/pxe-virtio.rom",
			PortForwards: []PortForward{
				{HostPort: 8080, GuestPort: 80},
				{HostPort: 8443, GuestPort: 443},
			},
		},
	)
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	requireElement(t, doc, "boot", map[string]string{"order": "1"})
	requireElement(t, doc, "rom", map[string]string{"file": "/usr/share/qemu/pxe-virtio.rom"})
	requireElement(t, doc, "range", map[string]string{"start": "2222", "to": "22"})
	requireElement(t, doc, "range", map[string]string{"start": "8080", "to": "80"})
	requireElement(t, doc, "range", map[string]string{"start": "8443", "to": "443"})
}

func TestRenderUserNetworkGeneratesMAC(t *testing.T) {
	doc, mac, err := RenderInterface(Config{Mode: "user"}, RenderOptions{})
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	if !strings.HasPrefix(mac, "52:54:00:") {
		t.Fatalf("generated mac = %q", mac)
	}
	requireElement(t, doc, "mac", map[string]string{"address": mac})
}

func TestRenderUserNetworkGuestAddresses(t *testing.T) {
	doc, _, err := RenderInterface(
		Config{
			Mode:       "user",
			MACAddress: "52:54:00:aa:bb:cc",
			GuestIPv4: []GuestAddress{
				{Address: "192.168.1.100", Prefix: 24},
				{Address: "172.16.0.10", Prefix: 12},
			},
			GuestIPv6: []GuestAddress{
				{Address: "fd00::1", Prefix: 64},
				{Address: "fd00::2", Prefix: 128},
			},
		},
		RenderOptions{IPv6Enabled: true},
	)
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	requireElement(t, doc, "ip", map[string]string{"family": "ipv4", "address": "192.168.1.100", "prefix": "24"})
	requireElement(t, doc, "ip", map[string]string{"family": "ipv4", "address": "172.16.0.10", "prefix": "12"})
	requireElement(t, doc, "ip", map[string]string{"family": "ipv6", "address": "fd00::1", "prefix": "64"})
	requireElement(t, doc, "ip", map[string]string{"family": "ipv6", "address": "fd00::2", "prefix": "128"})
	if strings.Contains(doc, "10.0.2.15") {
		t.Fatalf("default IPv4 should not be rendered when explicit IPv4 exists:\n%s", doc)
	}
	if strings.Contains(doc, "fec0::2") {
		t.Fatalf("default IPv6 should not be rendered when explicit IPv6 exists:\n%s", doc)
	}
}

func TestRenderUserNetworkDefaultIPv6(t *testing.T) {
	doc, _, err := RenderInterface(Config{Mode: "user", MACAddress: "52:54:00:aa:bb:cc"}, RenderOptions{IPv6Enabled: true})
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	requireElement(t, doc, "ip", map[string]string{"family": "ipv6", "address": "fec0::2", "prefix": "64"})
}

func TestRenderNetworkMTU(t *testing.T) {
	mtu := 9000
	doc, _, err := RenderInterface(Config{Mode: "user", MACAddress: "52:54:00:aa:bb:cc", MTU: &mtu}, RenderOptions{})
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	requireElement(t, doc, "mtu", map[string]string{"size": "9000"})
}

func TestRenderUserNetworkCanDisablePasstBackend(t *testing.T) {
	doc, _, err := RenderInterface(
		Config{Mode: "user", MACAddress: "52:54:00:aa:bb:cc"},
		RenderOptions{DisablePasst: true},
	)
	if err != nil {
		t.Fatalf("RenderInterface returned error: %v", err)
	}
	requireNoElement(t, doc, "backend")
}

func TestRenderBridgeNetwork(t *testing.T) {
	doc, _, err := RenderInterface(Config{Mode: "bridge", BridgeName: "br0", MACAddress: "52:54:00:11:22:33"}, RenderOptions{})
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	requireElement(t, doc, "interface", map[string]string{"type": "bridge"})
	requireElement(t, doc, "source", map[string]string{"bridge": "br0"})
	requireElement(t, doc, "driver", map[string]string{"name": "vhost"})
}

func TestRenderBridgeNetworkWithoutName(t *testing.T) {
	_, _, err := RenderInterface(Config{Mode: "bridge"}, RenderOptions{})
	if err == nil {
		t.Fatal("expected bridge name error")
	}
	if !strings.Contains(err.Error(), "NETWORK_BRIDGE must be set") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderBridgeNetworkNonVirtioOmitsVhost(t *testing.T) {
	doc, _, err := RenderInterface(Config{Mode: "bridge", BridgeName: "br0", Model: "e1000", MACAddress: "52:54:00:11:22:33"}, RenderOptions{})
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	if hasElement(collectElements(t, doc), "driver", map[string]string{"name": "vhost"}) {
		t.Fatalf("vhost driver should not be rendered for e1000:\n%s", doc)
	}
	requireElement(t, doc, "model", map[string]string{"type": "e1000"})
}

func TestRenderDirectNetwork(t *testing.T) {
	doc, _, err := RenderInterface(Config{Mode: "direct", DirectDevice: "eth0", MACAddress: "52:54:00:11:22:33"}, RenderOptions{})
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	requireElement(t, doc, "interface", map[string]string{"type": "direct"})
	requireElement(t, doc, "source", map[string]string{"dev": "eth0", "mode": "bridge"})
}

func TestRenderDirectNetworkWithoutDevice(t *testing.T) {
	_, _, err := RenderInterface(Config{Mode: "direct"}, RenderOptions{})
	if err == nil {
		t.Fatal("expected direct device error")
	}
	if !strings.Contains(err.Error(), "NETWORK_DIRECT_DEV must be set") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderUnsupportedNetwork(t *testing.T) {
	_, _, err := RenderInterface(Config{Mode: "invalid"}, RenderOptions{})
	if err == nil {
		t.Fatal("expected unsupported mode error")
	}
	if !strings.Contains(err.Error(), "unsupported network mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMACOverride(t *testing.T) {
	_, mac, err := RenderInterface(
		Config{Mode: "user", MACAddress: "52:54:00:aa:bb:cc"},
		RenderOptions{MACAddress: "52:54:00:11:22:33"},
	)
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	if mac != "52:54:00:11:22:33" {
		t.Fatalf("mac = %q", mac)
	}
}
