"""Global constants and path configuration for Docker-VM-Runner."""

from __future__ import annotations

import os
import re
from pathlib import Path

DEFAULT_CONFIG_PATH = Path("/config/distros.yaml")

# DATA_DIR provides a single mount point for all persistent data.
# When set, base images, VM disks, and state all live under DATA_DIR.
_DATA_DIR = os.environ.get("DATA_DIR")
# Auto-detect: if /data is a mount point, use it as DATA_DIR
if not _DATA_DIR and os.path.ismount("/data"):
    _DATA_DIR = "/data"
if _DATA_DIR:
    _data = Path(_DATA_DIR)
    IMAGES_DIR = _data
    BASE_IMAGES_DIR = _data / "base"
    VM_IMAGES_DIR = _data / "vms"
    STATE_DIR = _data / "state"
else:
    IMAGES_DIR = Path("/images")
    BASE_IMAGES_DIR = IMAGES_DIR / "base"
    VM_IMAGES_DIR = IMAGES_DIR / "vms"
    STATE_DIR = Path("/var/lib/docker-vm-runner")
INSTALLED_MARKER_NAME = ".installed"
BOOT_ISO_CACHE_DIR = STATE_DIR / "boot-isos"
LIBVIRT_URI = os.environ.get("LIBVIRT_URI", "qemu:///system")
TRUTHY = {"1", "true", "yes", "on"}
MAC_ADDRESS_RE = re.compile(r"^[0-9a-f]{2}(:[0-9a-f]{2}){5}$")

SUPPORTED_ARCHES = {
    "x86_64": {
        "machine": "q35",
        "features": ("acpi", "apic", "pae"),
        "tcg_fallback": "qemu64",
        "firmware": {
            "uefi": {
                "loader": Path("/usr/share/OVMF/OVMF_CODE_4M.fd"),
                "vars_template": Path("/usr/share/OVMF/OVMF_VARS_4M.fd"),
            },
            "secure": {
                "loader": Path("/usr/share/OVMF/OVMF_CODE_4M.ms.fd"),
                "vars_template": Path("/usr/share/OVMF/OVMF_VARS_4M.ms.fd"),
            },
        },
    },
    "aarch64": {
        "machine": "virt",
        "features": ("acpi",),
        "tcg_fallback": "cortex-a72",
        "firmware": {
            "loader": Path("/usr/share/AAVMF/AAVMF_CODE.fd"),
            "vars_template": Path("/usr/share/AAVMF/AAVMF_VARS.fd"),
        },
    },
    "ppc64": {
        "machine": "pseries",
        "features": (),
        "tcg_fallback": "power8",
    },
    "s390x": {
        "machine": "s390-ccw-virtio",
        "features": (),
        "tcg_fallback": "qemu",
    },
    "riscv64": {
        "machine": "virt",
        "features": (),
        "tcg_fallback": "rv64",
    },
}

ARCH_ALIASES = {
    "amd64": "x86_64",
    "arm64": "aarch64",
    "ppc64le": "ppc64",
    "ppc64el": "ppc64",
    "powerpc64": "ppc64",
    "riscv": "riscv64",
}

SUPPORTED_NETWORK_MODELS = {"virtio", "e1000", "e1000e", "rtl8139", "ne2k_pci", "pcnet", "vmxnet3"}

IPXE_DEFAULT_ROMS = {
    "x86_64": {
        "virtio": Path("/usr/share/qemu/pxe-virtio.rom"),
        "e1000": Path("/usr/share/qemu/pxe-e1000.rom"),
        "e1000e": Path("/usr/share/qemu/pxe-e1000e.rom"),
        "rtl8139": Path("/usr/share/qemu/pxe-rtl8139.rom"),
        "ne2k_pci": Path("/usr/share/qemu/pxe-ne2k_pci.rom"),
        "pcnet": Path("/usr/share/qemu/pxe-pcnet.rom"),
        "vmxnet3": Path("/usr/share/qemu/pxe-vmxnet3.rom"),
    },
    "aarch64": {
        "virtio": Path("/usr/share/qemu/efi-virtio.rom"),
        "e1000": Path("/usr/share/qemu/efi-e1000.rom"),
        "e1000e": Path("/usr/share/qemu/efi-e1000e.rom"),
        "rtl8139": Path("/usr/share/qemu/efi-rtl8139.rom"),
        "ne2k_pci": Path("/usr/share/qemu/efi-ne2k_pci.rom"),
        "pcnet": Path("/usr/share/qemu/efi-pcnet.rom"),
        "vmxnet3": Path("/usr/share/qemu/efi-vmxnet3.rom"),
    },
}

_LOG_VERBOSE = os.environ.get("LOG_VERBOSE", "").lower() in {"1", "true", "yes", "on"}

DISK_SIZE_RE = re.compile(r"^\d+[KMGTkmgt]?$")

_CONTAINER_ID_RE = re.compile(r"^[0-9a-f]{12,64}$")

_SENSITIVE_FIELDS = {"password", "redfish_password"}

DISK_CONTROLLERS = {
    "virtio": {"bus": "virtio", "dev_prefix": "vd"},
    "scsi": {"bus": "scsi", "dev_prefix": "sd"},
    "nvme": {"bus": "nvme", "dev_prefix": "nvme"},
    "ide": {"bus": "ide", "dev_prefix": "hd"},
    "usb": {"bus": "usb", "dev_prefix": "sd"},
}

DISK_IO_MODES = {"native", "threads", "io_uring"}
DISK_CACHE_MODES = {"none", "writeback", "writethrough", "directsync", "unsafe"}

CONVERTIBLE_FORMATS = {"vhd", "vhdx", "vmdk", "vdi"}
COMPRESSED_EXTENSIONS = {".gz", ".xz", ".7z", ".zip", ".bz2", ".rar", ".tar", ".ova"}
