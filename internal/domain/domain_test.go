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
