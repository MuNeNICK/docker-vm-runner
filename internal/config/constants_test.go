package config

import "testing"

func TestTruthyValues(t *testing.T) {
	for _, value := range []string{"1", "true", "yes", "on"} {
		if !Truthy[value] {
			t.Fatalf("expected %q to be truthy", value)
		}
	}
	if Truthy["false"] {
		t.Fatal("false must not be truthy")
	}
}

func TestSupportedArchitectures(t *testing.T) {
	expected := []string{"x86_64", "aarch64", "ppc64", "s390x", "riscv64"}
	for _, arch := range expected {
		profile, ok := SupportedArchitectures[arch]
		if !ok {
			t.Fatalf("missing supported architecture %q", arch)
		}
		if profile.Machine == "" {
			t.Fatalf("%s missing machine", arch)
		}
		if profile.TCGFallback == "" {
			t.Fatalf("%s missing TCG fallback", arch)
		}
		if profile.Features == nil {
			t.Fatalf("%s features should be an explicit slice", arch)
		}
	}
	if len(SupportedArchitectures) != len(expected) {
		t.Fatalf("unexpected supported arch count: got %d want %d", len(SupportedArchitectures), len(expected))
	}
}

func TestArchitectureAliasesMapToSupportedArchitectures(t *testing.T) {
	for alias, target := range ArchitectureAliases {
		if _, ok := SupportedArchitectures[target]; !ok {
			t.Fatalf("alias %q maps to unsupported architecture %q", alias, target)
		}
	}
	if ArchitectureAliases["amd64"] != "x86_64" {
		t.Fatalf("amd64 alias = %q", ArchitectureAliases["amd64"])
	}
	if ArchitectureAliases["arm64"] != "aarch64" {
		t.Fatalf("arm64 alias = %q", ArchitectureAliases["arm64"])
	}
}

func TestSupportedNetworkModels(t *testing.T) {
	for _, model := range []string{"virtio", "e1000"} {
		if !SupportedNetworkModels[model] {
			t.Fatalf("missing network model %q", model)
		}
	}
}

func TestIPXEROMsOnlyReferenceSupportedArchitectures(t *testing.T) {
	for arch := range IPXEDefaultROMs {
		if _, ok := SupportedArchitectures[arch]; !ok {
			t.Fatalf("iPXE ROMs reference unsupported architecture %q", arch)
		}
	}
}

func TestSensitiveFields(t *testing.T) {
	for _, field := range []string{"password", "redfish_password"} {
		if !SensitiveFields[field] {
			t.Fatalf("missing sensitive field %q", field)
		}
	}
}

func TestAArch64HasFirmware(t *testing.T) {
	firmware := SupportedArchitectures["aarch64"].Firmware
	if len(firmware) == 0 {
		t.Fatal("aarch64 firmware configuration is missing")
	}
	if firmware["default"].Loader == "" {
		t.Fatal("aarch64 firmware loader is missing")
	}
	if firmware["default"].VarsTemplate == "" {
		t.Fatal("aarch64 firmware vars template is missing")
	}
}
