package config

type FirmwareProfile struct {
	Loader       string
	VarsTemplate string
}

type ArchitectureProfile struct {
	Machine     string
	Features    []string
	TCGFallback string
	Firmware    map[string]FirmwareProfile
}

var Truthy = map[string]bool{
	"1":    true,
	"true": true,
	"yes":  true,
	"on":   true,
}

var SupportedArchitectures = map[string]ArchitectureProfile{
	"x86_64": {
		Machine:     "q35",
		Features:    []string{"acpi", "apic", "pae"},
		TCGFallback: "qemu64",
		Firmware: map[string]FirmwareProfile{
			"uefi": {
				Loader:       "/usr/share/OVMF/OVMF_CODE_4M.fd",
				VarsTemplate: "/usr/share/OVMF/OVMF_VARS_4M.fd",
			},
			"secure": {
				Loader:       "/usr/share/OVMF/OVMF_CODE_4M.ms.fd",
				VarsTemplate: "/usr/share/OVMF/OVMF_VARS_4M.ms.fd",
			},
		},
	},
	"aarch64": {
		Machine:     "virt",
		Features:    []string{"acpi"},
		TCGFallback: "cortex-a72",
		Firmware: map[string]FirmwareProfile{
			"default": {
				Loader:       "/usr/share/AAVMF/AAVMF_CODE.fd",
				VarsTemplate: "/usr/share/AAVMF/AAVMF_VARS.fd",
			},
		},
	},
	"ppc64": {
		Machine:     "pseries",
		Features:    []string{},
		TCGFallback: "power8",
	},
	"s390x": {
		Machine:     "s390-ccw-virtio",
		Features:    []string{},
		TCGFallback: "qemu",
	},
	"riscv64": {
		Machine:     "virt",
		Features:    []string{},
		TCGFallback: "rv64",
	},
}

var ArchitectureAliases = map[string]string{
	"amd64":     "x86_64",
	"arm64":     "aarch64",
	"ppc64le":   "ppc64",
	"ppc64el":   "ppc64",
	"powerpc64": "ppc64",
	"riscv":     "riscv64",
}

var SupportedNetworkModels = map[string]bool{
	"virtio":   true,
	"e1000":    true,
	"e1000e":   true,
	"rtl8139":  true,
	"ne2k_pci": true,
	"pcnet":    true,
	"vmxnet3":  true,
}

var IPXEDefaultROMs = map[string]map[string]string{
	"x86_64": {
		"virtio":   "/usr/share/qemu/pxe-virtio.rom",
		"e1000":    "/usr/share/qemu/pxe-e1000.rom",
		"e1000e":   "/usr/share/qemu/pxe-e1000e.rom",
		"rtl8139":  "/usr/share/qemu/pxe-rtl8139.rom",
		"ne2k_pci": "/usr/share/qemu/pxe-ne2k_pci.rom",
		"pcnet":    "/usr/share/qemu/pxe-pcnet.rom",
		"vmxnet3":  "/usr/share/qemu/pxe-vmxnet3.rom",
	},
	"aarch64": {
		"virtio":   "/usr/share/qemu/efi-virtio.rom",
		"e1000":    "/usr/share/qemu/efi-e1000.rom",
		"e1000e":   "/usr/share/qemu/efi-e1000e.rom",
		"rtl8139":  "/usr/share/qemu/efi-rtl8139.rom",
		"ne2k_pci": "/usr/share/qemu/efi-ne2k_pci.rom",
		"pcnet":    "/usr/share/qemu/efi-pcnet.rom",
		"vmxnet3":  "/usr/share/qemu/efi-vmxnet3.rom",
	},
}

var SensitiveFields = map[string]bool{
	"password":         true,
	"redfish_password": true,
	"ipmi_password":    true,
}
