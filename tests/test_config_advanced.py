"""Advanced tests for app.config.parse_env."""

from __future__ import annotations

import pytest
import yaml

from app.config import parse_env
from app.exceptions import ManagerError


@pytest.fixture
def distro_config_file(tmp_path):
    config = {
        "distributions": {
            "ubuntu-2404": {
                "name": "Ubuntu 24.04",
                "url": "https://example.com/ubuntu.qcow2",
                "user": "user",
                "arch": "x86_64",
            },
            "alma-aarch64": {
                "name": "AlmaLinux 9",
                "url": "https://example.com/alma-aarch64.qcow2",
                "user": "user",
                "arch": "aarch64",
            },
            "fedora-ppc64": {
                "name": "Fedora ppc64",
                "url": "https://example.com/fedora-ppc64.qcow2",
                "user": "user",
                "arch": "ppc64",
            },
        }
    }
    config_path = tmp_path / "distros.yaml"
    config_path.write_text(yaml.dump(config))
    return config_path


@pytest.fixture
def patched_config(monkeypatch, distro_config_file):
    import app.config as config_module

    monkeypatch.setattr(config_module, "DEFAULT_CONFIG_PATH", distro_config_file)


@pytest.mark.usefixtures("clean_env", "patched_config")
class TestParseEnvAdvanced:
    def test_iso_boot_auto_disables_cloud_init(self, monkeypatch):
        monkeypatch.setenv("BOOT_FROM", "https://example.com/installer.iso")
        cfg = parse_env()
        assert cfg.cloud_init_enabled is False
        assert cfg.blank_work_disk is True
        assert cfg.boot_order[0] == "cdrom"
        assert cfg.distro_name == "Custom ISO"

    def test_iso_boot_cloud_init_override(self, monkeypatch):
        monkeypatch.setenv("BOOT_FROM", "https://example.com/installer.iso")
        monkeypatch.setenv("CLOUD_INIT", "1")
        cfg = parse_env()
        assert cfg.cloud_init_enabled is True

    def test_cloud_init_user_data_missing_file_raises(self, monkeypatch, tmp_path):
        missing = tmp_path / "missing.yaml"
        monkeypatch.setenv("CLOUD_INIT_USER_DATA", str(missing))
        with pytest.raises(ManagerError, match="CLOUD_INIT_USER_DATA file not found"):
            parse_env()

    def test_cloud_init_user_data_invalid_yaml_raises(self, monkeypatch, tmp_path):
        user_data = tmp_path / "user-data.yaml"
        user_data.write_text("#cloud-config\nusers: [\n", encoding="utf-8")
        monkeypatch.setenv("CLOUD_INIT_USER_DATA", str(user_data))
        with pytest.raises(ManagerError, match="contains invalid YAML"):
            parse_env()

    def test_arch_mismatch_raises(self, monkeypatch):
        monkeypatch.setenv("DISTRO", "alma-aarch64")
        monkeypatch.setenv("ARCH", "x86_64")
        with pytest.raises(ManagerError, match="does not match distribution"):
            parse_env()

    def test_bridge_mode_requires_bridge_name(self, monkeypatch):
        monkeypatch.setenv("NETWORK_MODE", "bridge")
        with pytest.raises(ManagerError, match="NETWORK_BRIDGE is required"):
            parse_env()

    def test_secondary_nic_is_parsed(self, monkeypatch):
        monkeypatch.setenv("NETWORK2_MODE", "direct")
        monkeypatch.setenv("NETWORK2_DIRECT_DEV", "eth1")
        cfg = parse_env()
        assert len(cfg.nics) == 2
        assert cfg.nics[1].mode == "direct"
        assert cfg.nics[1].direct_device == "eth1"

    def test_host_mtu_is_applied(self, monkeypatch):
        monkeypatch.setattr("app.config.detect_host_mtu", lambda: 9000)
        cfg = parse_env()
        assert cfg.nics[0].mtu == 9000

    def test_filesystem_target_auto_derived_and_source_created(self, monkeypatch, tmp_path):
        source = tmp_path / "shared-data"
        monkeypatch.setenv("FILESYSTEM_SOURCE", str(source))
        cfg = parse_env()
        assert len(cfg.filesystems) == 1
        fs = cfg.filesystems[0]
        assert fs.target == "shared-data"
        assert fs.source == source
        assert source.exists()

    def test_readonly_filesystem_missing_source_raises(self, monkeypatch, tmp_path):
        source = tmp_path / "missing-share"
        monkeypatch.setenv("FILESYSTEM_SOURCE", str(source))
        monkeypatch.setenv("FILESYSTEM_READONLY", "1")
        with pytest.raises(ManagerError, match="cannot be created while readonly"):
            parse_env()

    def test_ipxe_without_default_rom_requires_override(self, monkeypatch):
        monkeypatch.setenv("DISTRO", "fedora-ppc64")
        monkeypatch.setenv("IPXE_ENABLE", "1")
        with pytest.raises(ManagerError, match="requires IPXE_ROM_PATH"):
            parse_env()

    def test_ipxe_override_sets_network_boot(self, monkeypatch, tmp_path):
        rom = tmp_path / "ipxe.rom"
        rom.write_bytes(b"rom")
        monkeypatch.setenv("IPXE_ENABLE", "1")
        monkeypatch.setenv("IPXE_ROM_PATH", str(rom))
        cfg = parse_env()
        assert cfg.ipxe_enabled is True
        assert cfg.ipxe_rom_path == str(rom)
        assert cfg.boot_order[0] == "network"
        assert cfg.nics[0].boot is True

    def test_invalid_disk_type_raises(self, monkeypatch):
        monkeypatch.setenv("DISK_TYPE", "bogus")
        with pytest.raises(ManagerError, match="Invalid DISK_TYPE"):
            parse_env()

    def test_parses_extra_disk_gpu_and_hyperv(self, monkeypatch):
        monkeypatch.setenv("DISK2_SIZE", "10G")
        monkeypatch.setenv("GPU", "intel")
        monkeypatch.setenv("USB", "0")
        monkeypatch.setenv("HYPERV", "1")
        cfg = parse_env()
        assert len(cfg.extra_disks) == 1
        assert cfg.extra_disks[0].index == 2
        assert cfg.extra_disks[0].size == "10G"
        assert cfg.gpu_passthrough == "intel"
        assert cfg.usb_controller is False
        assert cfg.hyperv_enabled is True

    def test_port_conflict_with_novnc_raises(self, monkeypatch):
        monkeypatch.setenv("GRAPHICS", "novnc")
        monkeypatch.setenv("SSH_PORT", "2222")
        monkeypatch.setenv("NOVNC_PORT", "2222")
        with pytest.raises(ManagerError, match="Port conflict"):
            parse_env()

    def test_device_must_be_block_device(self, monkeypatch, tmp_path):
        fake_device = tmp_path / "not-a-block-device"
        fake_device.write_text("data", encoding="utf-8")
        monkeypatch.setenv("DEVICE", str(fake_device))
        with pytest.raises(ManagerError, match="is not a block device"):
            parse_env()
