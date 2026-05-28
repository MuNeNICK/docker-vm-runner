package domain

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/filesystems"
	"github.com/munenick/docker-vm-runner/internal/network"
)

const MetadataNamespace = "https://github.com/munenick/docker-qemu/v2"

type Renderer struct{}

func NewRenderer() *Renderer {
	return &Renderer{}
}

type Request struct {
	VM                config.VM
	VMDir             string
	WorkImagePath     string
	SeedISOPath       string
	BootISOPath       string
	FirmwareLoader    string
	FirmwareVars      string
	IPXEROMPath       string
	KVMAvailable      bool
	EffectiveCPUModel string
	IPv6Enabled       bool
	IntelRenderNode   bool
	DisablePasst      bool
	HostCPUVendor     string
	HostCPUFlags      map[string]bool
	BlockSectorSize   func(string) (int, bool)
}

func (r *Renderer) Render(req Request) (string, error) {
	vm := req.VM
	profile := config.SupportedArchitectures[vm.Arch]
	machine := vm.MachineType
	if vm.Arch != "x86_64" && profile.Machine != "" {
		machine = profile.Machine
	}
	if machine == "" {
		machine = "q35"
	}
	model := effectiveCPUModel(req)
	hostCPU := req.KVMAvailable && (model == "host" || model == "host-passthrough")
	bootOrder := bootOrderPriority(vm.BootOrder)

	var b strings.Builder
	rootAttrs := []attr{{"type", domainType(req.KVMAvailable)}}
	if qemuArgs(vm, req) != nil {
		rootAttrs = append(rootAttrs, attr{"xmlns:qemu", "http://libvirt.org/schemas/domain/qemu/1.0"})
	}
	start(&b, "domain", rootAttrs...)
	textElement(&b, "name", vm.VMName)
	renderMetadata(&b, vm)
	textElementAttrs(&b, "memory", strconv.Itoa(vm.MemoryMB), attr{"unit", "MiB"})
	textElementAttrs(&b, "vcpu", strconv.Itoa(vm.CPUs), attr{"placement", "static"})
	if vm.IOThread {
		textElement(&b, "iothreads", "1")
	}

	start(&b, "os")
	textElementAttrs(&b, "type", "hvm", attr{"arch", vm.Arch}, attr{"machine", machine})
	if req.FirmwareLoader != "" || req.FirmwareVars != "" {
		secure := "no"
		if vm.BootMode == "secure" {
			secure = "yes"
		}
		textElementAttrs(&b, "loader", req.FirmwareLoader, attr{"readonly", "yes"}, attr{"secure", secure}, attr{"type", "pflash"})
		textElement(&b, "nvram", req.FirmwareVars)
	}
	end(&b, "os")

	features := append([]string(nil), profile.Features...)
	if len(features) > 0 || vm.HyperVEnabled {
		start(&b, "features")
		for _, feature := range features {
			empty(&b, feature)
		}
		if vm.HyperVEnabled {
			renderHyperV(&b, req)
		}
		end(&b, "features")
	}
	if vm.HyperVEnabled {
		start(&b, "clock", attr{"offset", "localtime"})
		empty(&b, "timer", attr{"name", "hypervclock"}, attr{"present", "yes"})
		end(&b, "clock")
	}

	if hasVirtioFS(vm.Filesystems) {
		start(&b, "memoryBacking")
		empty(&b, "source", attr{"type", "memfd"})
		empty(&b, "access", attr{"mode", "shared"})
		end(&b, "memoryBacking")
	}

	if hostCPU {
		empty(&b, "cpu", attr{"mode", "host-passthrough"})
	} else {
		start(&b, "cpu", attr{"mode", "custom"}, attr{"match", "exact"})
		textElementAttrs(&b, "model", model, attr{"fallback", "allow"})
		end(&b, "cpu")
	}

	start(&b, "devices")
	if err := renderDevices(&b, req, bootOrder); err != nil {
		return "", err
	}
	end(&b, "devices")

	args := qemuArgs(vm, req)
	if len(args) > 0 {
		start(&b, "qemu:commandline")
		for _, arg := range args {
			empty(&b, "qemu:arg", attr{"value", arg})
		}
		end(&b, "qemu:commandline")
	}
	end(&b, "domain")
	return b.String(), nil
}

func renderMetadata(b *strings.Builder, vm config.VM) {
	start(b, "metadata")
	start(b, "dvr:managed", attr{"xmlns:dvr", MetadataNamespace})
	b.WriteString("true")
	end(b, "dvr:managed")
	start(b, "dvr:vm-name", attr{"xmlns:dvr", MetadataNamespace})
	xml.EscapeText(b, []byte(vm.VMName))
	end(b, "dvr:vm-name")
	end(b, "metadata")
}

func renderDevices(b *strings.Builder, req Request, bootOrder map[string]int) error {
	vm := req.VM
	ctrl := diskController(vm.DiskController)
	if vm.DiskController == "scsi" {
		empty(b, "controller", attr{"type", "scsi"}, attr{"model", "virtio-scsi-pci"})
	}
	driverAttrs := []attr{
		{"name", "qemu"},
		{"type", defaultString(vm.ImageFormat, "qcow2")},
		{"cache", defaultString(vm.DiskCache, "none")},
		{"io", defaultString(vm.DiskIO, "native")},
	}
	if vm.IOThread && ctrl.bus == "virtio" {
		driverAttrs = append(driverAttrs, attr{"iothread", "1"})
	}

	renderDisk(b, diskSpec{
		Type:        "file",
		Device:      "disk",
		DriverAttrs: driverAttrs,
		SourceAttr:  attr{"file", req.WorkImagePath},
		TargetDev:   ctrl.dev(0),
		TargetBus:   ctrl.bus,
		BootOrder:   bootOrder["hd"],
	})

	vmDir := req.VMDir
	if vmDir == "" && req.WorkImagePath != "" {
		vmDir = filepath.Dir(req.WorkImagePath)
	}
	for _, disk := range vm.ExtraDisks {
		renderDisk(b, diskSpec{
			Type:        "file",
			Device:      "disk",
			DriverAttrs: driverAttrs,
			SourceAttr:  attr{"file", filepath.Join(vmDir, fmt.Sprintf("disk%d.%s", disk.Index, defaultString(vm.ImageFormat, "qcow2")))},
			TargetDev:   ctrl.dev(disk.Index - 1),
			TargetBus:   ctrl.bus,
		})
	}
	for _, block := range vm.BlockDevices {
		start(b, "disk", attr{"type", "block"}, attr{"device", "disk"})
		empty(b, "driver", attr{"name", "qemu"}, attr{"type", "raw"}, attr{"cache", "none"})
		empty(b, "source", attr{"dev", block.Path})
		offset := len(vm.ExtraDisks) + block.Index
		empty(b, "target", attr{"dev", ctrl.dev(offset)}, attr{"bus", ctrl.bus})
		if req.BlockSectorSize != nil {
			if sector, ok := req.BlockSectorSize(block.Path); ok && sector != 512 {
				value := strconv.Itoa(sector)
				empty(b, "blockio", attr{"logical_block_size", value}, attr{"physical_block_size", value})
			}
		}
		end(b, "disk")
	}
	if req.SeedISOPath != "" {
		renderCDROM(b, req.SeedISOPath, "sda", 0)
	}
	if req.BootISOPath != "" {
		renderCDROM(b, req.BootISOPath, "sdb", bootOrder["cdrom"])
	}
	if err := renderNetworks(b, req, bootOrder["network"]); err != nil {
		return err
	}
	for _, share := range vm.Filesystems {
		fsXML, err := filesystems.RenderXML(filesystems.Share{
			Source:     share.Source,
			Target:     share.Target,
			Driver:     share.Driver,
			AccessMode: share.AccessMode,
			Readonly:   share.Readonly,
		})
		if err != nil {
			return err
		}
		b.WriteString(fsXML)
	}
	if vm.USBController {
		empty(b, "controller", attr{"type", "usb"}, attr{"model", "qemu-xhci"})
		empty(b, "input", attr{"type", "tablet"}, attr{"bus", "usb"})
	}
	if vm.TPMEnabled {
		start(b, "tpm", attr{"model", "tpm-crb"})
		empty(b, "backend", attr{"type", "emulator"}, attr{"version", "2.0"})
		end(b, "tpm")
	}
	if vm.BalloonEnabled {
		empty(b, "memballoon", attr{"model", "virtio"})
	}
	if vm.RNGEnabled {
		start(b, "rng", attr{"model", "virtio"})
		textElementAttrs(b, "backend", "/dev/urandom", attr{"model", "random"})
		end(b, "rng")
	}
	start(b, "channel", attr{"type", "unix"})
	empty(b, "target", attr{"type", "virtio"}, attr{"name", "org.qemu.guest_agent.0"})
	end(b, "channel")
	start(b, "serial", attr{"type", "pty"})
	empty(b, "target", attr{"port", "0"})
	end(b, "serial")
	start(b, "console", attr{"type", "pty"})
	empty(b, "target", attr{"type", "virtio"}, attr{"port", "0"})
	end(b, "console")
	renderGraphics(b, req)
	return nil
}

type diskSpec struct {
	Type        string
	Device      string
	DriverAttrs []attr
	SourceAttr  attr
	TargetDev   string
	TargetBus   string
	BootOrder   int
}

func renderDisk(b *strings.Builder, spec diskSpec) {
	start(b, "disk", attr{"type", spec.Type}, attr{"device", spec.Device})
	empty(b, "driver", spec.DriverAttrs...)
	empty(b, "source", spec.SourceAttr)
	empty(b, "target", attr{"dev", spec.TargetDev}, attr{"bus", spec.TargetBus})
	if spec.BootOrder != 0 {
		empty(b, "boot", attr{"order", strconv.Itoa(spec.BootOrder)})
	}
	end(b, "disk")
}

func renderCDROM(b *strings.Builder, path string, dev string, order int) {
	start(b, "disk", attr{"type", "file"}, attr{"device", "cdrom"})
	empty(b, "driver", attr{"name", "qemu"}, attr{"type", "raw"})
	empty(b, "source", attr{"file", path})
	empty(b, "target", attr{"dev", dev}, attr{"bus", "sata"})
	empty(b, "readonly")
	if order != 0 {
		empty(b, "boot", attr{"order", strconv.Itoa(order)})
	}
	end(b, "disk")
}

func renderNetworks(b *strings.Builder, req Request, networkOrder int) error {
	for idx, nic := range req.VM.NICs {
		order := 0
		if nic.Boot {
			order = networkOrder
		}
		sshPort := 0
		var forwards []network.PortForward
		if idx == 0 && nic.Mode == "user" {
			sshPort = req.VM.SSHPort
			forwards = req.VM.PortForwards
		}
		xmlText, _, err := network.RenderInterface(nic, network.RenderOptions{
			SSHPort:      sshPort,
			BootOrder:    order,
			ROMFile:      req.IPXEROMPath,
			PortForwards: forwards,
			IPv6Enabled:  req.IPv6Enabled,
			DisablePasst: req.DisablePasst,
		})
		if err != nil {
			return err
		}
		b.WriteString(xmlText)
	}
	return nil
}

func renderGraphics(b *strings.Builder, req Request) {
	vm := req.VM
	if vm.GraphicsType == "" || vm.GraphicsType == "none" {
		return
	}
	attrs := []attr{{"type", vm.GraphicsType}, {"listen", "0.0.0.0"}}
	if vm.GraphicsType == "vnc" {
		attrs = append(attrs, attr{"port", strconv.Itoa(vm.VNCPort)}, attr{"autoport", "no"})
	} else {
		attrs = append(attrs, attr{"autoport", "yes"})
	}
	if vm.VNCKeymap != "" {
		attrs = append(attrs, attr{"keymap", vm.VNCKeymap})
	}
	empty(b, "graphics", attrs...)
	start(b, "video")
	start(b, "model", attr{"type", "virtio"}, attr{"heads", "1"}, attr{"primary", "yes"})
	if !intelGPUEnabled(vm, req) {
		empty(b, "resolution", attr{"x", "1920"}, attr{"y", "1080"})
	}
	end(b, "model")
	end(b, "video")
	start(b, "channel", attr{"type", "qemu-vdagent"})
	start(b, "source")
	empty(b, "clipboard", attr{"copypaste", "yes"})
	empty(b, "mouse", attr{"mode", "client"})
	end(b, "source")
	empty(b, "target", attr{"type", "virtio"}, attr{"name", "com.redhat.spice.0"})
	end(b, "channel")
}

func renderHyperV(b *strings.Builder, req Request) {
	start(b, "hyperv", attr{"mode", "passthrough"})
	for _, feature := range []string{"relaxed", "vapic", "vpindex", "runtime", "synic", "stimer", "frequencies"} {
		empty(b, feature, attr{"state", "on"})
	}
	empty(b, "spinlocks", attr{"state", "on"}, attr{"retries", "8191"})
	renderHyperVVendorFeatures(b, req)
	end(b, "hyperv")
}

func renderHyperVVendorFeatures(b *strings.Builder, req Request) {
	switch strings.ToLower(req.HostCPUVendor) {
	case "amd":
		empty(b, "evmcs", attr{"state", "off"})
		if !req.HostCPUFlags["avic"] {
			empty(b, "avic", attr{"state", "off"})
		}
	case "intel":
		if !req.HostCPUFlags["apicv"] {
			empty(b, "apicv", attr{"state", "off"})
		}
		empty(b, "evmcs", attr{"state", "off"})
	}
}

type controller struct {
	bus    string
	prefix string
}

func diskController(name string) controller {
	switch name {
	case "scsi":
		return controller{bus: "scsi", prefix: "sd"}
	case "nvme":
		return controller{bus: "nvme", prefix: "nvme"}
	case "ide":
		return controller{bus: "ide", prefix: "hd"}
	case "usb":
		return controller{bus: "usb", prefix: "sd"}
	default:
		return controller{bus: "virtio", prefix: "vd"}
	}
}

func (c controller) dev(index int) string {
	return c.prefix + string(rune('a'+index))
}

func bootOrderPriority(order []string) map[string]int {
	priorities := map[string]int{}
	for idx, device := range order {
		priorities[device] = idx + 1
	}
	return priorities
}

func effectiveCPUModel(req Request) string {
	model := strings.ToLower(defaultString(req.EffectiveCPUModel, req.VM.CPUModel))
	if model == "" {
		model = "host"
	}
	if !req.KVMAvailable && (model == "host" || model == "host-passthrough") {
		if profile, ok := config.SupportedArchitectures[req.VM.Arch]; ok && profile.TCGFallback != "" {
			return profile.TCGFallback
		}
		return "qemu64"
	}
	return model
}

func domainType(kvm bool) string {
	if kvm {
		return "kvm"
	}
	return "qemu"
}

func hasVirtioFS(shares []config.FilesystemShare) bool {
	for _, share := range shares {
		if share.Driver == "virtiofs" {
			return true
		}
	}
	return false
}

func qemuArgs(vm config.VM, req Request) []string {
	var args []string
	if vm.ExtraArgs != "" {
		args = append(args, strings.Fields(vm.ExtraArgs)...)
	}
	if intelGPUEnabled(vm, req) {
		args = append(args, "-display", "egl-headless")
		args = append(args, "-device", "virtio-vga-gl,rendernode=/dev/dri/renderD128")
	}
	if vm.HyperVEnabled {
		args = append(args, "-global", "ICH9-LPC.disable_s3=1", "-global", "ICH9-LPC.disable_s4=1")
	}
	return args
}

func intelGPUEnabled(vm config.VM, req Request) bool {
	return vm.GPUPassthrough == "intel" && req.IntelRenderNode
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
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

func textElement(b *strings.Builder, name string, value string) {
	textElementAttrs(b, name, value)
}

func textElementAttrs(b *strings.Builder, name string, value string, attrs ...attr) {
	start(b, name, attrs...)
	writeText(b, value)
	end(b, name)
}

func writeAttrs(b *strings.Builder, attrs []attr) {
	for _, attr := range attrs {
		b.WriteByte(' ')
		b.WriteString(attr.name)
		b.WriteString(`="`)
		b.WriteString(escapeXML(attr.value))
		b.WriteByte('"')
	}
}

func writeText(b *strings.Builder, value string) {
	b.WriteString(escapeXML(value))
}

func escapeXML(value string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(value)); err != nil {
		return value
	}
	return buf.String()
}
