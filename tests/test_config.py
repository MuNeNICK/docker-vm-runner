"""Tests for app.config module."""

from __future__ import annotations

import pytest
import yaml

from app.config import load_distro_config, parse_env
from app.exceptions import ManagerError


@pytest.fixture
def distro_config_file(tmp_path):
    """Create a temporary distros.yaml file."""
    config = {
        "distributions": {
            "ubuntu-2404": {
                "name": "Ubuntu 24.04",
                "url": "https://example.com/ubuntu.qcow2",
                "user": "ubuntu",
                "format": "qcow2",
            },
            "debian-12": {
                "name": "Debian 12",
                "url": "https://example.com/debian.qcow2",
                "user": "debian",
                "format": "qcow2",
            },
            "alma-aarch64": {
                "name": "AlmaLinux 9 (aarch64)",
                "url": "https://example.com/alma-aarch64.qcow2",
                "user": "almalinux",
                "format": "qcow2",
                "arch": "aarch64",
            },
        }
    }
    config_path = tmp_path / "distros.yaml"
    config_path.write_text(yaml.dump(config))
    return config_path


@pytest.fixture
def patched_config(monkeypatch, distro_config_file):
    """Point parse_env() at the temporary distro config file."""
    import app.config as config_module
    monkeypatch.setattr(config_module, "DEFAULT_CONFIG_PATH", distro_config_file)


class TestLoadDistroConfig:
    def test_valid_distro(self, distro_config_file):
        info = load_distro_config("ubuntu-2404", distro_config_file)
        assert info["name"] == "Ubuntu 24.04"
        assert info["user"] == "ubuntu"
        assert "url" in info

    def test_unknown_distro_raises(self, distro_config_file):
        with pytest.raises(ManagerError, match="Unknown distro 'nonexistent'"):
            load_distro_config("nonexistent", distro_config_file)

    def test_missing_config_raises(self, tmp_path):
        missing = tmp_path / "missing.yaml"
        with pytest.raises(ManagerError, match="Distribution config missing"):
            load_distro_config("ubuntu-2404", missing)


@pytest.mark.usefixtures("clean_env", "patched_config")
class TestParseEnv:
    def test_default_config(self):
        """Test parse_env with minimal environment setup."""
        cfg = parse_env()
        assert cfg.distro == "ubuntu-2404"
        assert cfg.memory_mb == 4096
        assert cfg.cpus == 2
        assert cfg.disk_size == "20G"
        assert cfg.arch == "x86_64"
        assert cfg.cpu_model == "host"
        assert cfg.ssh_port == 2222
        assert cfg.cloud_init_enabled is True
        assert cfg.persist is False
        assert cfg.force_iso is False
        assert cfg.vnc_keymap == ""
        assert cfg.port_forwards == []
        assert len(cfg.nics) == 1
        assert cfg.nics[0].mode == "user"

    def test_custom_memory_and_cpus(self, monkeypatch):
        monkeypatch.setenv("MEMORY", "8192")
        monkeypatch.setenv("CPUS", "4")

        cfg = parse_env()
        assert cfg.memory_mb == 8192
        assert cfg.cpus == 4

    def test_invalid_arch_raises(self, monkeypatch):
        monkeypatch.setenv("ARCH", "mips64")

        with pytest.raises(ManagerError, match="Unsupported ARCH 'mips64'"):
            parse_env()

    def test_port_fwd_parsing(self, monkeypatch):
        monkeypatch.setenv("PORT_FWD", "8080:80,8443:443")

        cfg = parse_env()
        assert len(cfg.port_forwards) == 2
        assert cfg.port_forwards[0].host_port == 8080
        assert cfg.port_forwards[0].guest_port == 80
        assert cfg.port_forwards[1].host_port == 8443
        assert cfg.port_forwards[1].guest_port == 443

    def test_invalid_port_fwd_raises(self, monkeypatch):
        monkeypatch.setenv("PORT_FWD", "invalid")

        with pytest.raises(ManagerError, match="Invalid PORT_FWD entry"):
            parse_env()

    def test_port_conflict_raises(self, monkeypatch):
        monkeypatch.setenv("SSH_PORT", "8080")
        monkeypatch.setenv("PORT_FWD", "8080:80")

        with pytest.raises(ManagerError, match="Port conflict"):
            parse_env()

    def test_arch_alias_resolution(self, monkeypatch):
        monkeypatch.setenv("ARCH", "amd64")

        cfg = parse_env()
        assert cfg.arch == "x86_64"
