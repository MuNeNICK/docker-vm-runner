"""Data models for Docker-VM-Runner."""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import List, NamedTuple, Optional


class PortForward(NamedTuple):
    host_port: int
    guest_port: int


@dataclass
class NicConfig:
    mode: str
    bridge_name: Optional[str] = None
    direct_device: Optional[str] = None
    mac_address: Optional[str] = None
    model: str = "virtio"
    boot: bool = False


@dataclass
class FilesystemConfig:
    source: Path
    target: str
    driver: str = "virtiofs"
    accessmode: str = "passthrough"
    readonly: bool = False


@dataclass
class VMConfig:
    distro: str
    image_url: str
    login_user: str
    image_format: str
    distro_name: str
    memory_mb: int
    cpus: int
    disk_size: str
    display: str
    graphics_type: str
    arch: str
    cpu_model: str
    extra_args: str
    novnc_enabled: bool
    vnc_port: int
    vnc_keymap: str
    novnc_port: int
    base_image_path: Optional[str]
    blank_work_disk: bool
    boot_iso_path: Optional[str]
    boot_iso_url: Optional[str]
    boot_order: List[str]
    cloud_init_enabled: bool
    cloud_init_user_data_path: Optional[Path]
    password: str
    ssh_port: int
    vm_name: str
    persist: bool
    force_iso: bool
    ssh_pubkey: Optional[str]
    redfish_user: str
    redfish_password: str
    redfish_port: int
    redfish_system_id: str
    redfish_enabled: bool
    nics: List[NicConfig]
    ipxe_enabled: bool
    ipxe_rom_path: Optional[str]
    filesystems: List[FilesystemConfig]
    port_forwards: List[PortForward]
