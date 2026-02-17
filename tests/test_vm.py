"""Tests for app.vm module (XML generation)."""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from app.models import FilesystemConfig, NicConfig, PortForward
from app.vm import VMManager


@pytest.fixture
def vm_config_for_xml(default_vm_config):
    """VMConfig tailored for XML generation tests."""
    return default_vm_config


@pytest.fixture
def mock_service_manager():
    return MagicMock()


def _make_mgr(
    vm_config,
    *,
    kvm=False,
    cpu_model="qemu64",
    arch_profile=None,
    tmp_path=None,
    firmware_loader=None,
    firmware_vars=None,
    seed_iso=None,
    boot_iso=None,
):
    """Create a VMManager instance with mocked __init__ for XML generation tests."""
    if arch_profile is None:
        arch_profile = {
            "machine": "pc",
            "features": ("acpi", "apic", "pae"),
            "tcg_fallback": "qemu64",
        }
    with patch.object(VMManager, "__init__", lambda self, *a, **kw: None):
        mgr = VMManager.__new__(VMManager)
        mgr.cfg = vm_config
        mgr._kvm_available = kvm
        mgr._effective_cpu_model = cpu_model
        mgr._arch_profile = arch_profile
        mgr._firmware_loader_path = firmware_loader
        mgr._firmware_vars_path = firmware_vars
        mgr.work_image = (tmp_path or Path("/tmp")) / "disk.qcow2"
        mgr.seed_iso = seed_iso
        mgr.boot_iso = boot_iso
        mgr._network_macs = {}
        mgr._ipxe_rom_path = None
        return mgr


class TestRenderDomainXml:
    @patch("app.vm.kvm_available", return_value=False)
    def test_basic_xml_structure(self, mock_kvm, vm_config_for_xml, mock_service_manager, tmp_path):
        vm_config_for_xml.vm_name = "test-xml-vm"
        vm_config_for_xml.nics = [NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")]

        mgr = _make_mgr(vm_config_for_xml, tmp_path=tmp_path)
        xml = mgr._render_domain_xml()

        assert "<domain" in xml
        assert 'type="qemu"' in xml
        assert "<name>test-xml-vm</name>" in xml
        assert '<memory unit="MiB">4096</memory>' in xml
        assert '<vcpu placement="static">2</vcpu>' in xml
        assert 'machine="pc"' in xml
        assert '<model fallback="allow">qemu64</model>' in xml
        assert "<acpi/>" in xml
        assert "<apic/>" in xml
        assert "<pae/>" in xml

    @patch("app.vm.kvm_available", return_value=True)
    def test_kvm_domain_type(self, mock_kvm, vm_config_for_xml, mock_service_manager, tmp_path):
        vm_config_for_xml.nics = [NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")]

        mgr = _make_mgr(vm_config_for_xml, kvm=True, cpu_model="host", tmp_path=tmp_path)
        xml = mgr._render_domain_xml()

        assert 'type="kvm"' in xml
        assert '<cpu mode="host-passthrough"/>' in xml

    @patch("app.vm.kvm_available", return_value=False)
    def test_xml_with_vnc(self, mock_kvm, vm_config_for_xml, mock_service_manager, tmp_path):
        vm_config_for_xml.graphics_type = "vnc"
        vm_config_for_xml.vnc_port = 5900
        vm_config_for_xml.vnc_keymap = "en-us"
        vm_config_for_xml.nics = [NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")]

        mgr = _make_mgr(vm_config_for_xml, tmp_path=tmp_path)
        xml = mgr._render_domain_xml()

        assert 'type="vnc"' in xml
        assert 'port="5900"' in xml
        assert 'keymap="en-us"' in xml
        assert "<video>" in xml
        assert 'type="virtio"' in xml
        assert 'resolution x="1920" y="1080"' in xml
        assert "qemu-vdagent" in xml

    @patch("app.vm.kvm_available", return_value=False)
    def test_xml_with_seed_iso(self, mock_kvm, vm_config_for_xml, mock_service_manager, tmp_path):
        vm_config_for_xml.nics = [NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")]
        seed = tmp_path / "seed.iso"

        mgr = _make_mgr(vm_config_for_xml, tmp_path=tmp_path, seed_iso=seed)
        xml = mgr._render_domain_xml()

        assert str(seed) in xml
        assert 'device="cdrom"' in xml
        assert 'dev="sda"' in xml

    @patch("app.vm.kvm_available", return_value=False)
    def test_xml_with_boot_iso(self, mock_kvm, vm_config_for_xml, mock_service_manager, tmp_path):
        vm_config_for_xml.boot_order = ["cdrom", "hd"]
        vm_config_for_xml.nics = [NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")]
        boot = tmp_path / "boot.iso"

        mgr = _make_mgr(vm_config_for_xml, tmp_path=tmp_path, boot_iso=boot)
        xml = mgr._render_domain_xml()

        assert str(boot) in xml
        assert 'dev="sdb"' in xml
        assert '<boot order="1"/>' in xml  # cdrom is first

    @patch("app.vm.kvm_available", return_value=False)
    def test_xml_with_filesystem(self, mock_kvm, vm_config_for_xml, mock_service_manager, tmp_path):
        vm_config_for_xml.filesystems = [
            FilesystemConfig(source=Path("/host/data"), target="data", driver="virtiofs"),
        ]
        vm_config_for_xml.nics = [NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")]

        mgr = _make_mgr(vm_config_for_xml, tmp_path=tmp_path)
        xml = mgr._render_domain_xml()

        assert '<filesystem type="mount"' in xml
        assert 'accessmode="passthrough"' in xml
        assert '<source dir="/host/data"/>' in xml
        assert '<target dir="data"/>' in xml
        assert "virtiofsd" in xml
        assert "<memoryBacking>" in xml
        assert '<source type="memfd"/>' in xml

    @patch("app.vm.kvm_available", return_value=False)
    def test_xml_with_extra_args(self, mock_kvm, vm_config_for_xml, mock_service_manager, tmp_path):
        vm_config_for_xml.extra_args = "-device virtio-rng-pci"
        vm_config_for_xml.nics = [NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")]

        mgr = _make_mgr(vm_config_for_xml, tmp_path=tmp_path)
        xml = mgr._render_domain_xml()

        assert "<qemu:commandline>" in xml
        assert 'value="-device"' in xml
        assert 'value="virtio-rng-pci"' in xml

    @patch("app.vm.kvm_available", return_value=False)
    def test_xml_with_firmware(self, mock_kvm, vm_config_for_xml, mock_service_manager, tmp_path):
        vm_config_for_xml.arch = "aarch64"
        vm_config_for_xml.nics = [NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")]

        loader = tmp_path / "AAVMF_CODE.fd"
        nvram = tmp_path / "test-vm-vars.fd"

        mgr = _make_mgr(
            vm_config_for_xml,
            cpu_model="cortex-a72",
            tmp_path=tmp_path,
            firmware_loader=loader,
            firmware_vars=nvram,
            arch_profile={
                "machine": "virt",
                "features": ("acpi",),
                "tcg_fallback": "cortex-a72",
                "firmware": {
                    "loader": Path("/usr/share/AAVMF/AAVMF_CODE.fd"),
                    "vars_template": Path("/usr/share/AAVMF/AAVMF_VARS.fd"),
                },
            },
        )
        xml = mgr._render_domain_xml()

        assert 'type="pflash"' in xml
        assert str(loader) in xml
        assert str(nvram) in xml
        assert 'machine="virt"' in xml

    @patch("app.vm.kvm_available", return_value=False)
    def test_xml_port_forwards_on_primary_nic(self, mock_kvm, vm_config_for_xml, mock_service_manager, tmp_path):
        vm_config_for_xml.port_forwards = [PortForward(8080, 80)]
        vm_config_for_xml.nics = [NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")]

        mgr = _make_mgr(vm_config_for_xml, tmp_path=tmp_path)
        xml = mgr._render_domain_xml()

        assert '<range start="8080" to="80"/>' in xml
        assert '<range start="2222" to="22"/>' in xml  # SSH forward
