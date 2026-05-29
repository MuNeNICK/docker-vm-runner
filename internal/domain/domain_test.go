package domain

import (
	"encoding/xml"
	"path/filepath"
	"strings"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/network"
)

func testVM() config.VM {
	return config.VM{
		VMName:           "test-xml-vm",
		MemoryMB:         4096,
		CPUs:             2,
		ImageFormat:      "qcow2",
		Arch:             "x86_64",
		CPUModel:         "host",
		MachineType:      "q35",
		BootOrder:        []string{"hd"},
		GraphicsType:     "none",
		VNCPort:          5900,
		DiskController:   "virtio",
		DiskIO:           "native",
		DiskCache:        "none",
		IOThread:         true,
		USBController:    true,
		BalloonEnabled:   true,
		RNGEnabled:       true,
		CloudInitEnabled: true,
		NICs: []network.Config{
			{Mode: "user", MACAddress: "52:54:00:aa:bb:cc", Model: "virtio"},
		},
	}
}

func renderForTest(t *testing.T, req Request) string {
	t.Helper()
	xmlText, err := NewRenderer().Render(req)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	var parsed any
	if err := xml.Unmarshal([]byte(xmlText), &parsed); err != nil {
		t.Fatalf("rendered XML is not well-formed: %v\n%s", err, xmlText)
	}
	return xmlText
}

func TestRenderMinimalDomainXML(t *testing.T) {
	vm := testVM()
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		KVMAvailable:      false,
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<domain type="qemu">`,
		`<name>test-xml-vm</name>`,
		`<memory unit="MiB">4096</memory>`,
		`<vcpu placement="static">2</vcpu>`,
		`<type arch="x86_64" machine="q35">hvm</type>`,
		`<model fallback="allow">qemu64</model>`,
		`<acpi/>`,
		`<apic/>`,
		`<pae/>`,
		`<disk type="file" device="disk">`,
		`<source file="/vm/disk.qcow2"/>`,
		`<target dev="vda" bus="virtio"/>`,
		`<interface type="user">`,
		`<channel type="unix">`,
		`org.qemu.guest_agent.0`,
		`<serial type="pty">`,
		`<console type="pty">`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderFallsBackDiskIOWhenNativeIOUnsafe(t *testing.T) {
	vm := testVM()
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		KVMAvailable:      true,
		EffectiveCPUModel: "host",
		NativeIOUnsafe:    true,
	})

	if !strings.Contains(xmlText, `cache="writeback"`) || !strings.Contains(xmlText, `io="threads"`) {
		t.Fatalf("expected disk IO fallback in:\n%s", xmlText)
	}
}

func TestRenderNVMEDiskTargets(t *testing.T) {
	vm := testVM()
	vm.DiskController = "nvme"
	vm.ExtraDisks = []config.Disk{{Size: "5G", Index: 2, Controller: "nvme"}}
	xmlText := renderForTest(t, Request{
		VM:                vm,
		VMDir:             "/vm",
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<target dev="nvme0n1" bus="nvme"/>`,
		`<target dev="nvme0n2" bus="nvme"/>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
	if strings.Contains(xmlText, `target dev="nvmea"`) {
		t.Fatalf("XML contains invalid NVMe target:\n%s", xmlText)
	}
}

func TestRenderRunnerOwnershipMetadata(t *testing.T) {
	vm := testVM()
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		KVMAvailable:      false,
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<metadata>`,
		`<dvr:managed xmlns:dvr="https://github.com/munenick/docker-qemu/v2">true</dvr:managed>`,
		`<dvr:vm-name xmlns:dvr="https://github.com/munenick/docker-qemu/v2">test-xml-vm</dvr:vm-name>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderKVMDomainXML(t *testing.T) {
	vm := testVM()
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		KVMAvailable:      true,
		EffectiveCPUModel: "host",
	})

	if !strings.Contains(xmlText, `<domain type="kvm">`) {
		t.Fatalf("XML missing kvm domain type:\n%s", xmlText)
	}
	if !strings.Contains(xmlText, `<cpu mode="host-passthrough"/>`) {
		t.Fatalf("XML missing host CPU passthrough:\n%s", xmlText)
	}
}

func TestRenderCPUFallbackWithoutKVM(t *testing.T) {
	vm := testVM()
	vm.CPUModel = "host"
	xmlText := renderForTest(t, Request{
		VM:            vm,
		WorkImagePath: "/vm/disk.qcow2",
		KVMAvailable:  false,
	})

	if !strings.Contains(xmlText, `<model fallback="allow">qemu64</model>`) {
		t.Fatalf("XML missing TCG CPU fallback:\n%s", xmlText)
	}
}

func TestRenderArchitectureFirmwareXML(t *testing.T) {
	vm := testVM()
	vm.Arch = "aarch64"
	vm.BootMode = "uefi"
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "cortex-a72",
		FirmwareLoader:    "/firmware/AAVMF_CODE.fd",
		FirmwareVars:      "/state/test-vm-vars.fd",
	})

	for _, want := range []string{
		`<type arch="aarch64" machine="virt">hvm</type>`,
		`<loader readonly="yes" secure="no" type="pflash">/firmware/AAVMF_CODE.fd</loader>`,
		`<nvram>/state/test-vm-vars.fd</nvram>`,
		`<acpi/>`,
		`<model fallback="allow">cortex-a72</model>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderSecureBootLoaderXML(t *testing.T) {
	vm := testVM()
	vm.BootMode = "secure"
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
		FirmwareLoader:    "/firmware/OVMF_CODE.secure.fd",
		FirmwareVars:      "/state/test-vm-vars.fd",
	})

	if !strings.Contains(xmlText, `secure="yes"`) {
		t.Fatalf("XML missing secure loader flag:\n%s", xmlText)
	}
}

func TestRenderIntelGPURequiresRenderNode(t *testing.T) {
	vm := testVM()
	vm.GPUPassthrough = "intel"
	vm.GraphicsType = "vnc"

	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
		IntelRenderNode:   false,
	})
	if strings.Contains(xmlText, "virtio-vga-gl") || strings.Contains(xmlText, "egl-headless") {
		t.Fatalf("XML should not include Intel GPU qemu args without rendernode:\n%s", xmlText)
	}
	if !strings.Contains(xmlText, `<resolution x="1920" y="1080"/>`) {
		t.Fatalf("XML should keep normal video resolution without rendernode:\n%s", xmlText)
	}

	xmlText = renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
		IntelRenderNode:   true,
	})
	if !strings.Contains(xmlText, `<qemu:arg value="egl-headless"/>`) ||
		!strings.Contains(xmlText, `<qemu:arg value="virtio-vga-gl,rendernode=/dev/dri/renderD128"/>`) {
		t.Fatalf("XML missing Intel GPU qemu args:\n%s", xmlText)
	}
}

func TestRenderCanDisablePasstBackend(t *testing.T) {
	vm := testVM()
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
		DisablePasst:      true,
	})
	if strings.Contains(xmlText, `<backend type="passt"/>`) {
		t.Fatalf("XML should not include passt backend:\n%s", xmlText)
	}
}

func TestRenderHyperVFeatures(t *testing.T) {
	vm := testVM()
	vm.HyperVEnabled = true
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<hyperv mode="passthrough">`,
		`<relaxed state="on"/>`,
		`<spinlocks state="on" retries="8191"/>`,
		`<clock offset="localtime">`,
		`<timer name="hypervclock" present="yes"/>`,
		`<qemu:arg value="-global"/>`,
		`<qemu:arg value="ICH9-LPC.disable_s3=1"/>`,
		`<qemu:arg value="ICH9-LPC.disable_s4=1"/>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderHyperVVendorFeatures(t *testing.T) {
	vm := testVM()
	vm.HyperVEnabled = true

	intelXML := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
		HostCPUVendor:     "intel",
		HostCPUFlags:      map[string]bool{},
	})
	for _, want := range []string{
		`<apicv state="off"/>`,
		`<evmcs state="off"/>`,
	} {
		if !strings.Contains(intelXML, want) {
			t.Fatalf("Intel XML missing %q:\n%s", want, intelXML)
		}
	}

	amdXML := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
		HostCPUVendor:     "amd",
		HostCPUFlags:      map[string]bool{},
	})
	for _, want := range []string{
		`<evmcs state="off"/>`,
		`<avic state="off"/>`,
	} {
		if !strings.Contains(amdXML, want) {
			t.Fatalf("AMD XML missing %q:\n%s", want, amdXML)
		}
	}
}

func TestRenderDomainWithGraphics(t *testing.T) {
	vm := testVM()
	vm.GraphicsType = "vnc"
	vm.VNCKeymap = "en-us"
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<graphics type="vnc" listen="0.0.0.0" port="5900" autoport="no" keymap="en-us"/>`,
		`<video>`,
		`<model type="virtio" heads="1" primary="yes">`,
		`<resolution x="1920" y="1080"/>`,
		`qemu-vdagent`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderDomainWithNoVNCBackend(t *testing.T) {
	vm := testVM()
	vm.GraphicsType = "vnc"
	vm.NoVNCEnabled = true
	vm.VNCPort = 5901
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<graphics type="vnc" listen="0.0.0.0" port="5901" autoport="no"/>`,
		`<video>`,
		`<model type="virtio" heads="1" primary="yes">`,
		`<channel type="qemu-vdagent">`,
		`<clipboard copypaste="yes"/>`,
		`<mouse mode="client"/>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderDomainDeviceOrdering(t *testing.T) {
	vm := testVM()
	vm.GraphicsType = "vnc"
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		SeedISOPath:       "/vm/seed.iso",
		EffectiveCPUModel: "qemu64",
	})
	names := deviceChildNames(t, xmlText)
	assertOrder(t, names, "disk", "interface")
	assertOrder(t, names, "interface", "controller")
	assertOrder(t, names, "controller", "channel")
	assertOrder(t, names, "channel", "serial")
	assertOrder(t, names, "serial", "console")
	assertOrder(t, names, "console", "graphics")
	assertOrder(t, names, "graphics", "video")
}

func TestRenderDomainWithSeedAndBootISO(t *testing.T) {
	vm := testVM()
	vm.BootOrder = []string{"cdrom", "hd"}
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		SeedISOPath:       "/vm/seed.iso",
		BootISOPath:       "/vm/boot.iso",
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<source file="/vm/seed.iso"/>`,
		`<target dev="sda" bus="sata"/>`,
		`<source file="/vm/boot.iso"/>`,
		`<target dev="sdb" bus="sata"/>`,
		`<boot order="1"/>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderDomainWithFilesystemShare(t *testing.T) {
	vm := testVM()
	vm.Filesystems = []config.FilesystemShare{
		{Source: "/host/data", Target: "data", Driver: "virtiofs", AccessMode: "passthrough"},
	}
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<memoryBacking>`,
		`<source type="memfd"/>`,
		`<access mode="shared"/>`,
		`<filesystem type="mount" accessmode="passthrough">`,
		`<driver type="virtiofs"/>`,
		`<binary path="/usr/lib/qemu/virtiofsd"/>`,
		`<source dir="/host/data"/>`,
		`<target dir="data"/>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderDomainOptionalDevices(t *testing.T) {
	vm := testVM()
	vm.TPMEnabled = true
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<controller type="usb" model="qemu-xhci"/>`,
		`<input type="tablet" bus="usb"/>`,
		`<tpm model="tpm-crb">`,
		`<backend type="emulator" version="2.0"/>`,
		`<memballoon model="virtio"/>`,
		`<rng model="virtio">`,
		`<backend model="random">/dev/urandom</backend>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderDomainAvoidsSCSIDiskAndSeedISOTargetCollision(t *testing.T) {
	vm := testVM()
	vm.DiskController = "scsi"
	xmlText := renderForTest(t, Request{
		VM:                vm,
		WorkImagePath:     "/vm/disk.qcow2",
		SeedISOPath:       "/vm/seed.iso",
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<target dev="sda" bus="scsi"/>`,
		`<target dev="sdb" bus="sata"/>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderDomainWithExtraDisksBlockDeviceAndQEMUArgs(t *testing.T) {
	vm := testVM()
	vm.ExtraArgs = "-device virtio-rng-pci"
	vm.ExtraDisks = []config.Disk{{Size: "5G", Index: 2}}
	vm.BlockDevices = []config.BlockDevice{{Path: "/dev/testblk", Index: 1}}
	vmDir := t.TempDir()

	xmlText := renderForTest(t, Request{
		VM:                vm,
		VMDir:             vmDir,
		WorkImagePath:     filepath.Join(vmDir, "disk.qcow2"),
		EffectiveCPUModel: "qemu64",
		BlockSectorSize: func(path string) (int, bool) {
			if path == "/dev/testblk" {
				return 4096, true
			}
			return 0, false
		},
	})

	for _, want := range []string{
		`<source file="` + filepath.Join(vmDir, "disk2.qcow2") + `"/>`,
		`<target dev="vdb" bus="virtio"/>`,
		`<disk type="block" device="disk">`,
		`<source dev="/dev/testblk"/>`,
		`<target dev="vdc" bus="virtio"/>`,
		`<blockio logical_block_size="4096" physical_block_size="4096"/>`,
		`<qemu:commandline>`,
		`<qemu:arg value="-device"/>`,
		`<qemu:arg value="virtio-rng-pci"/>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

func TestRenderDomainAvoidsSparseExtraDiskAndBlockDeviceTargetCollision(t *testing.T) {
	vm := testVM()
	vm.ExtraDisks = []config.Disk{{Size: "5G", Index: 3}}
	vm.BlockDevices = []config.BlockDevice{{Path: "/dev/testblk", Index: 1}}
	vmDir := t.TempDir()

	xmlText := renderForTest(t, Request{
		VM:                vm,
		VMDir:             vmDir,
		WorkImagePath:     filepath.Join(vmDir, "disk.qcow2"),
		EffectiveCPUModel: "qemu64",
	})

	for _, want := range []string{
		`<source file="` + filepath.Join(vmDir, "disk3.qcow2") + `"/>`,
		`<target dev="vdb" bus="virtio"/>`,
		`<source dev="/dev/testblk"/>`,
		`<target dev="vdc" bus="virtio"/>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Fatalf("XML missing %q:\n%s", want, xmlText)
		}
	}
}

type domainXML struct {
	Devices struct {
		Children []xml.Name `xml:",any"`
	} `xml:"devices"`
}

func deviceChildNames(t *testing.T, xmlText string) []string {
	t.Helper()
	var parsed domainXML
	if err := xml.Unmarshal([]byte(xmlText), &parsed); err != nil {
		t.Fatalf("unmarshal domain XML: %v", err)
	}
	var names []string
	for _, name := range parsed.Devices.Children {
		names = append(names, name.Local)
	}
	return names
}

func assertOrder(t *testing.T, names []string, before string, after string) {
	t.Helper()
	beforeIndex := indexOf(names, before)
	afterIndex := indexOf(names, after)
	if beforeIndex == -1 || afterIndex == -1 {
		t.Fatalf("names missing %q or %q: %#v", before, after, names)
	}
	if beforeIndex >= afterIndex {
		t.Fatalf("device order %q before %q not satisfied: %#v", before, after, names)
	}
}

func indexOf(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}
	return -1
}
