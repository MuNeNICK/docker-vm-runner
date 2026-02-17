"""Tests for app.models module."""

from pathlib import Path

from app.models import FilesystemConfig, NicConfig, PortForward, VMConfig


class TestPortForward:
    def test_creation(self):
        pf = PortForward(host_port=8080, guest_port=80)
        assert pf.host_port == 8080
        assert pf.guest_port == 80

    def test_is_named_tuple(self):
        pf = PortForward(8080, 80)
        assert pf[0] == 8080
        assert pf[1] == 80


class TestNicConfig:
    def test_defaults(self):
        nic = NicConfig(mode="user")
        assert nic.mode == "user"
        assert nic.bridge_name is None
        assert nic.direct_device is None
        assert nic.mac_address is None
        assert nic.model == "virtio"
        assert nic.boot is False

    def test_bridge_mode(self):
        nic = NicConfig(mode="bridge", bridge_name="br0", model="e1000")
        assert nic.mode == "bridge"
        assert nic.bridge_name == "br0"
        assert nic.model == "e1000"

    def test_direct_mode(self):
        nic = NicConfig(mode="direct", direct_device="eth0")
        assert nic.direct_device == "eth0"


class TestFilesystemConfig:
    def test_defaults(self):
        fs = FilesystemConfig(source=Path("/host/data"), target="data")
        assert fs.driver == "virtiofs"
        assert fs.accessmode == "passthrough"
        assert fs.readonly is False

    def test_9p_driver(self):
        fs = FilesystemConfig(source=Path("/host/data"), target="data", driver="9p")
        assert fs.driver == "9p"


class TestVMConfig:
    def test_creation(self, default_vm_config):
        cfg = default_vm_config
        assert cfg.distro == "ubuntu-2404"
        assert cfg.memory_mb == 4096
        assert cfg.cpus == 2
        assert cfg.arch == "x86_64"
        assert cfg.vm_name == "test-vm"
        assert cfg.persist is False
        assert cfg.force_iso is False
        assert cfg.vnc_keymap == ""
        assert cfg.port_forwards == []

    def test_nics_is_list(self, default_vm_config):
        assert isinstance(default_vm_config.nics, list)
        assert len(default_vm_config.nics) == 1
        assert default_vm_config.nics[0].mode == "user"

    def test_filesystems_empty_by_default(self, default_vm_config):
        assert default_vm_config.filesystems == []
