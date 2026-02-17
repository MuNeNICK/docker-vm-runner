"""Shared test fixtures and libvirt stub injection for CI environments."""

from __future__ import annotations

import sys
import types
from unittest.mock import MagicMock


def _install_libvirt_stub():
    """Inject a minimal libvirt stub into sys.modules if the real library is not available."""
    if "libvirt" in sys.modules:
        return

    try:
        import libvirt  # noqa: F401

        return  # real library available
    except (ImportError, SystemExit):
        pass

    stub = types.ModuleType("libvirt")

    class libvirtError(Exception):
        def get_error_message(self):
            return str(self)

    stub.libvirtError = libvirtError
    stub.open = MagicMock(return_value=MagicMock())

    # virConnect / virDomain stubs
    stub.virConnect = MagicMock
    stub.virDomain = MagicMock

    sys.modules["libvirt"] = stub


_install_libvirt_stub()

import pytest  # noqa: E402

from app.models import NicConfig, VMConfig  # noqa: E402


@pytest.fixture
def default_vm_config() -> VMConfig:
    """Return a minimal VMConfig with sensible defaults."""
    return VMConfig(
        distro="ubuntu-2404",
        image_url="https://example.com/ubuntu.qcow2",
        login_user="user",
        image_format="qcow2",
        distro_name="Ubuntu 24.04",
        memory_mb=4096,
        cpus=2,
        disk_size="20G",
        display="none",
        graphics_type="none",
        arch="x86_64",
        cpu_model="host",
        extra_args="",
        novnc_enabled=False,
        vnc_port=5900,
        vnc_keymap="",
        novnc_port=6080,
        base_image_path=None,
        blank_work_disk=False,
        boot_iso_path=None,
        boot_iso_url=None,
        boot_order=["hd"],
        cloud_init_enabled=True,
        cloud_init_user_data_path=None,
        password="password",
        ssh_port=2222,
        vm_name="test-vm",
        persist=False,
        force_iso=False,
        ssh_pubkey=None,
        redfish_user="admin",
        redfish_password="password",
        redfish_port=8443,
        redfish_system_id="test-vm",
        redfish_enabled=False,
        nics=[NicConfig(mode="user")],
        ipxe_enabled=False,
        ipxe_rom_path=None,
        filesystems=[],
        port_forwards=[],
        boot_mode="legacy",
        disk_io="native",
        disk_cache="none",
    )


@pytest.fixture
def mock_env(monkeypatch):
    """Helper to set environment variables for tests."""

    def _set(**kwargs):
        for key, value in kwargs.items():
            if value is None:
                monkeypatch.delenv(key, raising=False)
            else:
                monkeypatch.setenv(key, str(value))

    return _set


# All environment variables that parse_env() reads — used to ensure a clean slate.
_PARSE_ENV_VARS = [
    "DISTRO",
    "MEMORY",
    "CPUS",
    "DISK_SIZE",
    "GRAPHICS",
    "ARCH",
    "CPU_MODEL",
    "EXTRA_ARGS",
    "GUEST_PASSWORD",
    "SSH_PORT",
    "GUEST_NAME",
    "HOSTNAME",
    "NETWORK_MODE",
    "PERSIST",
    "BOOT_ISO",
    "BOOT_ISO_URL",
    "CLOUD_INIT",
    "CLOUD_INIT_USER_DATA",
    "BOOT_ORDER",
    "BASE_IMAGE",
    "BLANK_DISK",
    "VNC_PORT",
    "VNC_KEYMAP",
    "NOVNC_PORT",
    "NO_CONSOLE",
    "IPXE_ENABLE",
    "IPXE_ROM_PATH",
    "SSH_PUBKEY",
    "REDFISH_ENABLE",
    "REDFISH_USERNAME",
    "REDFISH_PASSWORD",
    "REDFISH_PORT",
    "REDFISH_SYSTEM_ID",
    "FORCE_ISO",
    "PORT_FWD",
    "DATA_DIR",
    "NETWORK_BRIDGE",
    "NETWORK_DIRECT_DEV",
    "NETWORK_MAC",
    "NETWORK_MODEL",
    "NETWORK_BOOT",
    "FILESYSTEM_SOURCE",
    "FILESYSTEM_TARGET",
    "DISK_IO",
    "DISK_CACHE",
    "NETWORK_MTU",
]


@pytest.fixture
def clean_env(monkeypatch):
    """Clear all environment variables that parse_env() reads, then set DISTRO default."""
    for key in _PARSE_ENV_VARS:
        monkeypatch.delenv(key, raising=False)
    monkeypatch.setenv("DISTRO", "ubuntu-2404")
