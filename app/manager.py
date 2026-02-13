#!/usr/bin/env python3
"""
Docker-VM-Runner manager rewritten around libvirt + sushy.
Maintains the existing UX (single `docker run` attaches directly to the guest
console) while provisioning the VM via libvirt and exposing Redfish control
through sushy-emulator.
"""

from __future__ import annotations

import argparse
import errno
import hashlib
import json
import os
import random
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import textwrap
import time
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, List, NamedTuple, Optional, Tuple
from urllib.parse import urlparse
from urllib.request import urlopen, Request
from urllib.error import URLError, HTTPError

try:
    import yaml  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit("PyYAML is required but not installed") from exc

try:
    import bcrypt  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit("bcrypt is required but not installed") from exc

try:
    import libvirt  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit(f"libvirt python bindings not available: {exc}")


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
        "machine": "pc",
        "features": ("acpi", "apic", "pae"),
        "tcg_fallback": "qemu64",
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
}

ARCH_ALIASES = {
    "amd64": "x86_64",
    "arm64": "aarch64",
}

SUPPORTED_NETWORK_MODELS = {"virtio", "e1000", "e1000e", "rtl8139", "ne2k_pci", "pcnet", "vmxnet3"}

IPXE_DEFAULT_ROMS = {
    "x86_64": {
        "virtio": Path("/usr/lib/ipxe/qemu/pxe-virtio.rom"),
        "e1000": Path("/usr/lib/ipxe/qemu/pxe-e1000.rom"),
        "e1000e": Path("/usr/lib/ipxe/qemu/pxe-e1000e.rom"),
        "rtl8139": Path("/usr/lib/ipxe/qemu/pxe-rtl8139.rom"),
        "ne2k_pci": Path("/usr/lib/ipxe/qemu/pxe-ne2k_pci.rom"),
        "pcnet": Path("/usr/lib/ipxe/qemu/pxe-pcnet.rom"),
        "vmxnet3": Path("/usr/lib/ipxe/qemu/pxe-vmxnet3.rom"),
    },
    "aarch64": {
        "virtio": Path("/usr/lib/ipxe/qemu/efi-virtio.rom"),
        "e1000": Path("/usr/lib/ipxe/qemu/efi-e1000.rom"),
        "e1000e": Path("/usr/lib/ipxe/qemu/efi-e1000e.rom"),
        "rtl8139": Path("/usr/lib/ipxe/qemu/efi-rtl8139.rom"),
        "ne2k_pci": Path("/usr/lib/ipxe/qemu/efi-ne2k_pci.rom"),
        "pcnet": Path("/usr/lib/ipxe/qemu/efi-pcnet.rom"),
        "vmxnet3": Path("/usr/lib/ipxe/qemu/efi-vmxnet3.rom"),
    },
}


class ManagerError(RuntimeError):
    """Raised on unrecoverable configuration or runtime errors."""


_LOG_VERBOSE = os.environ.get("LOG_VERBOSE", "").lower() in {"1", "true", "yes", "on"}


def log(level: str, message: str) -> None:
    """Lightweight structured logging compatible with existing colour expectation."""
    if level == "DEBUG" and not _LOG_VERBOSE:
        return
    colours = {
        "INFO": "\033[0;34m",
        "WARN": "\033[1;33m",
        "ERROR": "\033[0;31m",
        "SUCCESS": "\033[0;32m",
        "DEBUG": "\033[0;90m",
    }
    colour = colours.get(level, "")
    reset = "\033[0m" if colour else ""
    print(f"{colour}[{level}]{reset} {message}", flush=True)


def get_env(name: str, default: Optional[str] = None) -> Optional[str]:
    return os.environ.get(name, default)


def get_env_bool(name: str, default: bool = False) -> bool:
    raw = os.environ.get(name)
    if raw is None:
        return default
    return raw.lower() in TRUTHY


def parse_int_env(name: str, default: str, min_val: int = 1, max_val: Optional[int] = None) -> int:
    raw = get_env(name, default)
    assert raw is not None
    try:
        value = int(raw)
    except ValueError:
        raise ManagerError(f"{name} must be an integer (got '{raw}')")
    if value < min_val:
        raise ManagerError(f"{name} must be >= {min_val} (got {value})")
    if max_val is not None and value > max_val:
        raise ManagerError(f"{name} must be <= {max_val} (got {value})")
    return value


DISK_SIZE_RE = re.compile(r"^\d+[KMGTkmgt]?$")


def validate_disk_size(raw: str) -> str:
    if not DISK_SIZE_RE.match(raw):
        raise ManagerError(
            f"Invalid DISK_SIZE '{raw}'. Use a number with optional suffix: K, M, G, T (e.g. '20G')"
        )
    return raw


def download_file(url: str, destination: Path, label: str = "Downloading") -> None:
    """Download a file with a progress bar using Python urllib."""
    log("INFO", f"{label}: {url}")
    req = Request(url, headers={"User-Agent": "docker-vm-runner/1.0"})
    try:
        response = urlopen(req, timeout=60)
    except HTTPError as exc:
        raise ManagerError(f"HTTP error downloading {url}: {exc.code} {exc.reason}")
    except URLError as exc:
        raise ManagerError(f"Failed to download {url}: {exc.reason}")

    total = response.headers.get("Content-Length")
    total_bytes = int(total) if total else None
    downloaded = 0
    start_time = time.time()

    with tempfile.NamedTemporaryFile(delete=False, dir=destination.parent) as tmp:
        tmp_path = Path(tmp.name)
        try:
            chunk_size = 1024 * 256  # 256 KiB
            while True:
                chunk = response.read(chunk_size)
                if not chunk:
                    break
                tmp.write(chunk)
                downloaded += len(chunk)

                elapsed = time.time() - start_time
                speed = downloaded / elapsed if elapsed > 0 else 0
                downloaded_mb = downloaded / (1024 * 1024)

                if total_bytes:
                    total_mb = total_bytes / (1024 * 1024)
                    pct = downloaded * 100 / total_bytes
                    remaining = (total_bytes - downloaded) / speed if speed > 0 else 0
                    eta_str = time.strftime("%M:%S", time.gmtime(remaining))
                    bar_len = 30
                    filled = int(bar_len * downloaded / total_bytes)
                    bar = "#" * filled + "-" * (bar_len - filled)
                    print(
                        f"\r  [{bar}] {pct:5.1f}% {downloaded_mb:.1f}/{total_mb:.1f} MiB "
                        f"({speed / (1024 * 1024):.1f} MiB/s, ETA {eta_str})",
                        end="", flush=True,
                    )
                else:
                    print(
                        f"\r  {downloaded_mb:.1f} MiB downloaded "
                        f"({speed / (1024 * 1024):.1f} MiB/s)",
                        end="", flush=True,
                    )
            print(flush=True)  # newline after progress
            tmp_path.replace(destination)
            elapsed = time.time() - start_time
            final_mb = downloaded / (1024 * 1024)
            log("SUCCESS", f"Downloaded {final_mb:.1f} MiB in {elapsed:.1f}s")
        except Exception:
            if tmp_path.exists():
                tmp_path.unlink(missing_ok=True)
            raise


def kvm_available() -> bool:
    """Return True if /dev/kvm exists and can be opened."""
    kvm_path = Path("/dev/kvm")
    if not kvm_path.exists():
        return False
    try:
        fd = os.open(kvm_path, os.O_RDONLY)
    except OSError:
        return False
    else:
        os.close(fd)
        return True


def has_controlling_tty() -> bool:
    """Return True if both stdin and stdout are attached to a TTY."""
    for stream in (sys.stdin, sys.stdout):
        try:
            if not stream.isatty():
                return False
        except (AttributeError, ValueError):
            return False
    return True


def wait_for_path(path: Path, timeout: float = 10.0, interval: float = 0.1) -> bool:
    """Poll for a filesystem path to show up (e.g., libvirt socket)."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if path.exists():
            return True
        time.sleep(interval)
    return False


def ensure_directory(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)


def random_mac() -> str:
    """Generate a deterministic-looking, locally-administered MAC address."""
    octets = [0x52, 0x54, 0x00]  # qemu prefix
    octets += [random.randint(0x00, 0x7F) for _ in range(3)]
    return ":".join(f"{octet:02x}" for octet in octets)


def hash_password(password: str) -> str:
    """Generate a bcrypt hash for cloud-init."""
    hashed = bcrypt.hashpw(password.encode("utf-8"), bcrypt.gensalt())
    return hashed.decode("utf-8")


def detect_cloud_init_content_type(payload: str) -> str:
    """Infer the MIME type for cloud-init user data."""
    stripped = payload.lstrip()
    if not stripped:
        return "text/cloud-config"
    first_line = stripped.splitlines()[0].strip().lower()
    if stripped.startswith("#!"):
        return "text/x-shellscript"
    if first_line.startswith("#cloud-config-archive"):
        return "text/cloud-config-archive"
    if first_line.startswith("#cloud-config"):
        return "text/cloud-config"
    if first_line.startswith("#cloud-boothook"):
        return "text/cloud-boothook"
    if first_line.startswith("#include"):
        return "text/x-include-url"
    if first_line.startswith("#part-handler"):
        return "text/part-handler"
    return "text/cloud-config"


_CONTAINER_ID_RE = re.compile(r"^[0-9a-f]{12,64}$")


def derive_vm_name(distro: str, iso_mode: bool = False) -> str:
    explicit = get_env("GUEST_NAME")
    if explicit:
        return explicit.strip()

    hostname_env = os.environ.get("HOSTNAME")
    if hostname_env:
        candidate = hostname_env.strip()
        if candidate and not _CONTAINER_ID_RE.match(candidate):
            return candidate

    if iso_mode:
        return "custom-vm"

    return distro


def deterministic_mac(seed: str) -> str:
    digest = hashlib.sha256(seed.encode("utf-8")).digest()
    octets = [0x52, 0x54, 0x00, digest[0], digest[1], digest[2]]
    octets[3] = octets[3] | 0x02  # ensure locally administered bit
    octets[3] = octets[3] & 0xFE  # clear multicast bit
    return ":".join(f"{octet:02x}" for octet in octets)


def sanitize_mount_target(tag: str) -> str:
    """Return a filesystem-safe name for mounting inside the guest."""
    safe = re.sub(r"[^0-9A-Za-z._-]", "-", tag)
    safe = safe.strip("-")
    return safe or "share"


def render_network_xml(
    config: NicConfig,
    ssh_port: Optional[int] = None,
    mac_address: Optional[str] = None,
    boot_order: Optional[int] = None,
    rom_file: Optional[str] = None,
    port_forwards: Optional[List[PortForward]] = None,
) -> Tuple[str, str]:
    """Render a libvirt interface definition based on the requested network mode."""
    mac = (mac_address or config.mac_address or random_mac()).lower()
    model = config.model

    if config.mode == "user":
        body = ["<interface type='user'>"]
        if boot_order is not None:
            body.append(f"  <boot order='{boot_order}'/>")
        body.append(f"  <mac address='{mac}'/>")
        body.append("  <backend type='passt'/>")
        body.append("  <ip family='ipv4' address='10.0.2.15' prefix='24'/>")
        body.append(f"  <model type='{model}'/>")
        if rom_file:
            body.append(f"  <rom file='{rom_file}'/>")
        if ssh_port is not None:
            body.extend(
                [
                    "  <portForward proto='tcp'>",
                    f"    <range start='{ssh_port}' to='22'/>",
                    "  </portForward>",
                ]
            )
        for pf in (port_forwards or []):
            body.extend(
                [
                    "  <portForward proto='tcp'>",
                    f"    <range start='{pf.host_port}' to='{pf.guest_port}'/>",
                    "  </portForward>",
                ]
            )
        body.append("</interface>")
        return "\n".join(body), mac

    if config.mode == "bridge":
        if not config.bridge_name:
            raise ManagerError("NETWORK_BRIDGE must be set when NETWORK_MODE=bridge")
        body = ["<interface type='bridge'>"]
        if boot_order is not None:
            body.append(f"  <boot order='{boot_order}'/>")
        body.append(f"  <mac address='{mac}'/>")
        if model == "virtio":
            body.append("  <driver name='vhost'/>")
        body.append(f"  <model type='{model}'/>")
        if rom_file:
            body.append(f"  <rom file='{rom_file}'/>")
        body.append(f"  <source bridge='{config.bridge_name}'/>")
        body.append("</interface>")
        return "\n".join(body), mac

    if config.mode == "direct":
        if not config.direct_device:
            raise ManagerError("NETWORK_DIRECT_DEV must be set when NETWORK_MODE=direct")
        body = ["<interface type='direct'>"]
        if boot_order is not None:
            body.append(f"  <boot order='{boot_order}'/>")
        body.append(f"  <mac address='{mac}'/>")
        if model == "virtio":
            body.append("  <driver name='vhost'/>")
        body.append(f"  <model type='{model}'/>")
        if rom_file:
            body.append(f"  <rom file='{rom_file}'/>")
        body.append(f"  <source dev='{config.direct_device}' mode='bridge'/>")
        body.append("</interface>")
        return "\n".join(body), mac

    raise ManagerError(f"Unsupported network mode: {config.mode}")


def run(cmd: List[str], check: bool = True, **kwargs) -> subprocess.CompletedProcess:
    """Run command with logging."""
    log("DEBUG", f"Running: {' '.join(cmd)}")
    result = subprocess.run(cmd, check=check, text=True, **kwargs)
    return result


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


class ServiceManager:
    """Wrapper responsible for starting libvirt + sushy daemons."""

    def __init__(self, vm_config: VMConfig) -> None:
        self.vm_config = vm_config
        self.processes: List[subprocess.Popen] = []
        self._shutdown = False
        self.cert_dir = STATE_DIR / "certs"
        self.config_dir = STATE_DIR / "sushy"
        ensure_directory(self.cert_dir)
        ensure_directory(self.config_dir)
        self._novnc_started = False
        self._storage_pool_name = os.environ.get("REDFISH_STORAGE_POOL", "default")
        self._storage_pool_path = Path(
            os.environ.get("REDFISH_STORAGE_PATH", "/var/lib/libvirt/images")
        )

    def start(self) -> None:
        self._start_libvirt()
        self._wait_for_libvirt()
        if self.vm_config.redfish_enabled:
            self._ensure_storage_pool()
            self._start_sushy()
        else:
            log("INFO", "Redfish disabled (set REDFISH_ENABLE=1 to enable)")

    def _start_libvirt(self) -> None:
        ensure_directory(Path("/run/libvirt"))
        ensure_directory(Path("/var/run/libvirt"))
        sockets = [
            Path("/run/libvirt/libvirt-sock"),
            Path("/var/run/libvirt/libvirt-sock"),
            Path("/run/libvirt/virtlogd-sock"),
            Path("/var/run/libvirt/virtlogd-sock"),
        ]
        for sock in sockets:
            self._cleanup_socket(sock)

        virtlogd_cmd = ["/usr/sbin/virtlogd"]
        virtlogd_conf = Path("/etc/libvirt/virtlogd.conf")
        if virtlogd_conf.exists():
            virtlogd_cmd.extend(["-f", str(virtlogd_conf)])
        else:  # pragma: no cover - only hit in slim images
            log("WARN", "virtlogd.conf not found; using built-in defaults")
        virtlogd = subprocess.Popen(
            virtlogd_cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.processes.append(virtlogd)

        libvirtd_cmd = ["/usr/sbin/libvirtd"]
        libvirtd_conf = Path("/etc/libvirt/libvirtd.conf")
        if libvirtd_conf.exists():
            libvirtd_cmd.extend(["-f", str(libvirtd_conf)])
        else:  # pragma: no cover - only hit in slim images
            log("WARN", "libvirtd.conf not found; using built-in defaults")
        libvirtd = subprocess.Popen(
            libvirtd_cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.processes.append(libvirtd)
        log("INFO", "libvirt services spawned")
        self._assert_running(virtlogd, "virtlogd")
        self._assert_running(libvirtd, "libvirtd")

    def _cleanup_socket(self, path: Path) -> None:
        """Remove stale libvirt sockets without touching active host instances."""
        if not path.exists() or not path.is_socket():
            return

        stale = False
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
                client.settimeout(0.2)
                client.connect(str(path))
        except socket.timeout:
            stale = True
        except OSError as exc:
            if exc.errno in {errno.ECONNREFUSED, errno.ENOENT}:
                stale = True
            else:
                log("WARN", f"Skipping removal of socket {path}: {exc}")
                return
        else:
            log("INFO", f"Detected active libvirt socket at {path}; leaving in place")
            return

        if stale:
            try:
                path.unlink()
                log("INFO", f"Removed stale libvirt socket {path}")
            except FileNotFoundError:
                return
            except OSError as exc:
                log("WARN", f"Failed to remove stale socket {path}: {exc}")

    def _assert_running(self, proc: subprocess.Popen, name: str) -> None:
        time.sleep(0.5)
        if proc.poll() is not None:
            stdout, stderr = proc.communicate()
            log("ERROR", f"{name} failed to start")
            if stdout:
                log("ERROR", f"{name} stdout:\n{stdout}")
            if stderr:
                log("ERROR", f"{name} stderr:\n{stderr}")
            raise ManagerError(f"{name} exited prematurely (code {proc.returncode})")

    def _wait_for_libvirt(self) -> None:
        libvirt_paths = [
            Path("/run/libvirt/libvirt-sock"),
            Path("/var/run/libvirt/libvirt-sock"),
        ]
        virtlogd_paths = [
            Path("/run/libvirt/virtlogd-sock"),
            Path("/var/run/libvirt/virtlogd-sock"),
        ]
        if not any(wait_for_path(path, timeout=15) for path in libvirt_paths):
            raise ManagerError(
                "libvirt socket did not appear.\n"
                "  Possible fixes:\n"
                "    - Run with --privileged\n"
                "    - Or add --cgroupns=host --device /dev/kvm:/dev/kvm\n"
                "    - Ensure the container has sufficient capabilities (SYS_ADMIN, NET_ADMIN)"
            )
        if not any(wait_for_path(path, timeout=15) for path in virtlogd_paths):
            raise ManagerError(
                "virtlogd socket did not appear.\n"
                "  Possible fixes:\n"
                "    - Run with --privileged\n"
                "    - Or add --cgroupns=host\n"
                "    - Check container logs for virtlogd errors"
            )

    def _ensure_certificates(self) -> None:
        crt = self.cert_dir / "sushy.crt"
        key = self.cert_dir / "sushy.key"
        if crt.exists() and key.exists():
            return
        log("INFO", "Generating self-signed certificate for Redfish endpoint")
        run(
            [
                "openssl",
                "req",
                "-x509",
                "-nodes",
                "-days",
                "365",
                "-newkey",
                "rsa:2048",
                "-keyout",
                str(key),
                "-out",
                str(crt),
                "-subj",
                "/CN=docker-vm-runner/O=docker-vm-runner",
            ]
        )

    def _write_auth_file(self) -> Path:
        auth_path = self.config_dir / "htpasswd"
        hashed = bcrypt.hashpw(
            self.vm_config.redfish_password.encode("utf-8"), bcrypt.gensalt()
        ).decode("utf-8")
        auth_path.write_text(f"{self.vm_config.redfish_user}:{hashed}\n")
        return auth_path

    def _write_config(self, cert: Path, key: Path, auth_file: Path) -> Path:
        config_path = self.config_dir / "sushy.conf"
        lines = [
            f"SUSHY_EMULATOR_LIBVIRT_URI = {LIBVIRT_URI!r}",
            'SUSHY_EMULATOR_LISTEN_IP = "0.0.0.0"',
            f"SUSHY_EMULATOR_LISTEN_PORT = {self.vm_config.redfish_port}",
            f"SUSHY_EMULATOR_SSL_CERT = {str(cert)!r}",
            f"SUSHY_EMULATOR_SSL_KEY = {str(key)!r}",
            f"SUSHY_EMULATOR_AUTH_FILE = {str(auth_file)!r}",
        ]
        config_path.write_text("\n".join(lines) + "\n")
        return config_path

    def _start_sushy(self) -> None:
        self._ensure_certificates()
        cert = self.cert_dir / "sushy.crt"
        key = self.cert_dir / "sushy.key"
        auth_file = self._write_auth_file()
        config_file = self._write_config(cert, key, auth_file)
        cmd = [
            "sushy-emulator",
            "--config",
            str(config_file),
            "--libvirt-uri",
            LIBVIRT_URI,
        ]
        log("INFO", f"Starting sushy-emulator (port {self.vm_config.redfish_port})")
        proc = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.processes.append(proc)

    def _ensure_storage_pool(self) -> None:
        ensure_directory(self._storage_pool_path)
        conn: Optional[libvirt.virConnect] = None
        try:
            conn = libvirt.open(LIBVIRT_URI)
            if conn is None:
                log(
                    "WARN",
                    f"Failed to open libvirt connection at {LIBVIRT_URI}; "
                    "virtual media may be unavailable",
                )
                return
            try:
                pool = conn.storagePoolLookupByName(self._storage_pool_name)
            except libvirt.libvirtError:
                pool = None
            if pool is None:
                pool_xml = textwrap.dedent(
                    f"""
                    <pool type='dir'>
                      <name>{self._storage_pool_name}</name>
                      <target>
                        <path>{self._storage_pool_path}</path>
                      </target>
                    </pool>
                    """
                ).strip()
                pool = conn.storagePoolDefineXML(pool_xml, 0)
                try:
                    pool.build(0)
                except libvirt.libvirtError as exc:
                    log(
                        "WARN",
                        f"Storage pool '{self._storage_pool_name}' build failed: {exc}",
                    )
                else:
                    log(
                        "INFO",
                        f"Created libvirt storage pool "
                        f"'{self._storage_pool_name}' "
                        f"({self._storage_pool_path})",
                    )
            if pool.isActive() == 0:
                pool.create(0)
            if pool.autostart() == 0:
                pool.setAutostart(True)
        except libvirt.libvirtError as exc:
            log(
                "WARN",
                f"Unable to ensure storage pool '{self._storage_pool_name}': {exc}",
            )
        finally:
            if conn is not None:
                conn.close()

    def start_novnc(self) -> None:
        if not self.vm_config.novnc_enabled:
            return
        if self._novnc_started:
            return

        if shutil.which("websockify") is None:
            raise ManagerError(
                "noVNC requested but websockify is missing. Install websockify inside the container image."
            )

        web_root = Path("/usr/share/novnc")
        if not web_root.exists():
            raise ManagerError("noVNC static assets not found at /usr/share/novnc.")

        # Ensure landing on / renders the viewer automatically instead of a directory listing.
        index_path = web_root / "index.html"
        if (web_root / "vnc.html").exists():
            redirect_marker = "<!-- docker-vm-runner novnc redirect -->"
            try:
                existing = index_path.read_text(encoding="utf-8")
            except FileNotFoundError:
                existing = ""
            except OSError:
                existing = None
            if existing is None or redirect_marker not in existing:
                redirect_html = f"""<!DOCTYPE html>
<html>
  <head>
    {redirect_marker}
    <meta charset="utf-8" />
    <title>noVNC</title>
    <script>
      window.location.replace("vnc.html?autoconnect=1&resize=scale");
    </script>
  </head>
  <body>
    <p>
      Redirecting to
      <a href="vnc.html?autoconnect=1&resize=scale">noVNC console</a>…
    </p>
    <noscript>
      <meta http-equiv="refresh" content="1;url=vnc_lite.html">
      <p>
        JavaScript is required for the full noVNC client.
        You will be redirected to the lite client shortly.
      </p>
    </noscript>
  </body>
</html>
"""
                try:
                    index_path.write_text(redirect_html, encoding="utf-8")
                except OSError:
                    log(
                        "WARN",
                        f"Failed to update {index_path} for noVNC redirect; directory listing will remain.",
                    )

        self._ensure_certificates()
        cert = self.cert_dir / "sushy.crt"
        key = self.cert_dir / "sushy.key"

        listen = f"0.0.0.0:{self.vm_config.novnc_port}"
        target = f"127.0.0.1:{self.vm_config.vnc_port}"
        cmd = [
            "websockify",
            "--web",
            str(web_root),
            "--cert",
            str(cert),
            "--key",
            str(key),
            listen,
            target,
        ]
        log(
            "INFO",
            f"Starting noVNC proxy (web:{self.vm_config.novnc_port} -> VNC:{self.vm_config.vnc_port})",
        )
        try:
            proc = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
        except FileNotFoundError as exc:
            raise ManagerError(f"Failed to start noVNC proxy: {exc}") from exc

        self.processes.append(proc)
        self._novnc_started = True
        log(
            "INFO",
            f"noVNC web client available at https://localhost:{self.vm_config.novnc_port}/vnc.html",
        )

    def stop(self) -> None:
        if self._shutdown:
            return
        self._shutdown = True
        for proc in self.processes:
            if proc.poll() is None:
                proc.terminate()
        for proc in self.processes:
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()


class VMManager:
    def __init__(self, vm_config: VMConfig, service_manager: ServiceManager) -> None:
        self.cfg = vm_config
        self.service_manager = service_manager
        self.conn: Optional[libvirt.virConnect] = None
        self.domain: Optional[libvirt.virDomain] = None
        self._kvm_available = kvm_available()
        self._effective_cpu_model = self.cfg.cpu_model
        self._arch_profile = SUPPORTED_ARCHES[self.cfg.arch]
        self._firmware_loader_path: Optional[Path] = None
        self._firmware_vars_path: Optional[Path] = None
        ensure_directory(IMAGES_DIR)
        ensure_directory(BASE_IMAGES_DIR)
        ensure_directory(VM_IMAGES_DIR)
        self.vm_dir = VM_IMAGES_DIR / self.cfg.vm_name
        if self.cfg.persist:
            ensure_directory(self.vm_dir)
        else:
            if self.vm_dir.exists():
                shutil.rmtree(self.vm_dir, ignore_errors=True)
            ensure_directory(self.vm_dir)
        self._external_base_image = False
        if self.cfg.base_image_path:
            self.base_image = Path(self.cfg.base_image_path)
            self._external_base_image = True
        else:
            self.base_image = BASE_IMAGES_DIR / f"{self.cfg.distro}.{self.cfg.image_format}"
        self.work_image = self.vm_dir / f"disk.{self.cfg.image_format}"
        self.boot_iso = Path(self.cfg.boot_iso_path) if self.cfg.boot_iso_path else None
        self.boot_iso_url = self.cfg.boot_iso_url
        self.seed_iso = self.vm_dir / "seed.iso" if self.cfg.cloud_init_enabled else None
        self._disk_reused = False
        self._network_macs: Dict[int, str] = {}
        self._ipxe_rom_path: Optional[Path] = None
        self._boot_iso_cache_path: Optional[Path] = None
        if self.cfg.ipxe_enabled and self.cfg.ipxe_rom_path:
            self._ipxe_rom_path = Path(self.cfg.ipxe_rom_path)

    def connect(self) -> None:
        self.conn = libvirt.open(LIBVIRT_URI)
        if self.conn is None:
            raise ManagerError(f"Failed to open libvirt connection to {LIBVIRT_URI}")

    def close(self) -> None:
        if self.conn is not None:
            self.conn.close()
            self.conn = None

    def prepare(self) -> None:
        if not self._kvm_available:
            log("WARN", "=" * 60)
            log("WARN", "  /dev/kvm not found!")
            log("WARN", "  Running in software emulation mode (TCG).")
            log("WARN", "  Performance will be 10-50x slower.")
            log("WARN", "  Fix: add --device /dev/kvm:/dev/kvm")
            log("WARN", "=" * 60)
            if get_env_bool("REQUIRE_KVM", False):
                raise ManagerError(
                    "REQUIRE_KVM=1 is set but /dev/kvm is not available. "
                    "Add --device /dev/kvm:/dev/kvm or unset REQUIRE_KVM."
                )
            cpu_lower = self.cfg.cpu_model.lower()
            if cpu_lower in {"host", "host-passthrough"}:
                fallback = self._arch_profile.get("tcg_fallback")
                if not fallback:
                    raise ManagerError(
                        f"CPU_MODEL={self.cfg.cpu_model} requires KVM for architecture {self.cfg.arch}."
                    )
                self._effective_cpu_model = fallback
                log(
                    "WARN",
                    f"CPU_MODEL=host is not compatible with TCG on {self.cfg.arch}. Using {fallback} instead.",
                )
        if not self.cfg.blank_work_disk:
            self._ensure_base_image()
        self._prepare_work_image()
        # Smart ISO skip: if disk was reused from a prior install, skip ISO boot
        iso_requested = bool(self.boot_iso or self.boot_iso_url)
        if (
            iso_requested
            and self._disk_reused
            and self._is_installed()
            and not self.cfg.force_iso
        ):
            log("INFO", "Persistent disk with prior install found; skipping ISO boot (set FORCE_ISO=1 to override)")
            self.boot_iso = None
            self.boot_iso_url = None
            if "cdrom" in self.cfg.boot_order:
                self.cfg.boot_order = [d for d in self.cfg.boot_order if d != "cdrom"]
            if "hd" not in self.cfg.boot_order:
                self.cfg.boot_order = ["hd"] + self.cfg.boot_order
        self._prepare_boot_iso()
        if self.boot_iso and not self.boot_iso.exists():
            raise ManagerError(f"Boot ISO not found: {self.boot_iso}")
        self._prepare_firmware()
        self._generate_cloud_init()
        self._define_domain()

    def _ensure_base_image(self) -> None:
        if self._external_base_image:
            if not self.base_image.exists():
                raise ManagerError(f"Base image not found: {self.base_image}")
            log("INFO", f"Using external base image: {self.base_image}")
            return
        if self.base_image.exists() and self.base_image.stat().st_size > 100 * 1024 * 1024:
            log("INFO", f"Using cached image: {self.base_image}")
            return
        if self.base_image.exists():
            size_mb = self.base_image.stat().st_size / (1024 * 1024)
            log("WARN", f"Cached image too small ({size_mb:.1f} MiB < 100 MiB threshold); re-downloading {self.base_image}")
            self.base_image.unlink()

        download_file(self.cfg.image_url, self.base_image, label="Downloading base image")

    def _prepare_work_image(self) -> None:
        self._disk_reused = False
        if self.cfg.persist and self.work_image.exists():
            size = self.work_image.stat().st_size
            if size > 100 * 1024 * 1024:
                log("INFO", f"Reusing persistent disk {self.work_image}")
                self._disk_reused = True
                # Expand disk if DISK_SIZE is larger than current virtual size
                if self.cfg.disk_size and self.cfg.disk_size != "0":
                    info = subprocess.run(
                        ["qemu-img", "info", "--output=json", str(self.work_image)],
                        capture_output=True, text=True,
                    )
                    if info.returncode == 0:
                        current_vsize = json.loads(info.stdout).get("virtual-size", 0)
                        requested = self.cfg.disk_size
                        # Parse requested size to bytes for comparison
                        suffix = requested[-1].upper() if requested[-1].isalpha() else ""
                        num = int(requested[:-1]) if suffix else int(requested)
                        multiplier = {"K": 1024, "M": 1024**2, "G": 1024**3, "T": 1024**4}.get(suffix, 1)
                        requested_bytes = num * multiplier
                        if requested_bytes > current_vsize:
                            log("INFO", f"Expanding disk from {current_vsize // (1024**3)}G to {requested}...")
                            run(["qemu-img", "resize", str(self.work_image), requested])
                            log("SUCCESS", f"Disk expanded to {requested}")
            else:
                size_mb = size / (1024 * 1024)
                log("WARN", f"Existing disk too small ({size_mb:.1f} MiB < 100 MiB threshold); recreating {self.work_image}")
                self.work_image.unlink(missing_ok=True)

        if not self._disk_reused:
            if self.cfg.blank_work_disk:
                log("INFO", f"Creating blank disk {self.work_image} ({self.cfg.disk_size})")
                run(
                    [
                        "qemu-img",
                        "create",
                        "-f",
                        self.cfg.image_format,
                        str(self.work_image),
                        self.cfg.disk_size,
                    ]
                )
            else:
                log("INFO", f"Creating working disk {self.work_image}")
                if self.base_image.suffix.lower() == ".iso":
                    raise ManagerError(
                        f"BASE_IMAGE points to an ISO ({self.base_image}). "
                        f"Try: BOOT_ISO={self.base_image} (and optionally BLANK_DISK=1)"
                    )
                shutil.copy2(self.base_image, self.work_image)
                if self.cfg.disk_size and self.cfg.disk_size != "0":
                    log("INFO", f"Resizing disk to {self.cfg.disk_size}...")
                    run(["qemu-img", "resize", str(self.work_image), self.cfg.disk_size])
        else:
            log("INFO", f"Persistent disk retained at {self.work_image}")

    def _prepare_boot_iso(self) -> None:
        if self.boot_iso:
            return
        if not self.boot_iso_url:
            return

        ensure_directory(BOOT_ISO_CACHE_DIR)
        digest = hashlib.sha256(self.boot_iso_url.encode("utf-8")).hexdigest()
        parsed = urlparse(self.boot_iso_url)
        base_name = Path(parsed.path or "").name or "boot.iso"
        safe_name = re.sub(r"[^A-Za-z0-9._-]", "_", base_name) or "boot.iso"
        if not Path(safe_name).suffix:
            safe_name = f"{safe_name}.iso"
        destination = BOOT_ISO_CACHE_DIR / f"{digest[:12]}-{safe_name}"

        if destination.exists() and destination.stat().st_size > 0:
            log("INFO", f"Using cached BOOT_ISO_URL download: {destination}")
        else:
            download_file(self.boot_iso_url, destination, label="Downloading boot ISO")

        self._boot_iso_cache_path = destination
        self.boot_iso = destination

    def _is_installed(self) -> bool:
        return (self.vm_dir / INSTALLED_MARKER_NAME).exists()

    def _mark_installed(self) -> None:
        marker = self.vm_dir / INSTALLED_MARKER_NAME
        if not marker.exists():
            marker.write_text(f"Installed on {time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}\n")
            log("INFO", f"Marked VM as installed ({marker})")

    def _prepare_firmware(self) -> None:
        firmware_cfg = self._arch_profile.get("firmware")
        if not firmware_cfg:
            return

        loader_path = Path(firmware_cfg["loader"])
        vars_template_path = Path(firmware_cfg["vars_template"])

        if not loader_path.exists():
            raise ManagerError(
                f"Firmware loader not found at {loader_path} for arch {self.cfg.arch}. Install qemu-efi-aarch64."
            )
        if not vars_template_path.exists():
            raise ManagerError(
                f"Firmware variable template not found at {vars_template_path} for arch {self.cfg.arch}. "
                "Install qemu-efi-aarch64."
            )

        firmware_dir = STATE_DIR / "firmware"
        ensure_directory(firmware_dir)
        vars_destination = firmware_dir / f"{self.cfg.vm_name}-vars.fd"
        if not vars_destination.exists():
            shutil.copy2(vars_template_path, vars_destination)

        self._firmware_loader_path = loader_path
        self._firmware_vars_path = vars_destination

    def _generate_cloud_init(self) -> None:
        if not self.cfg.cloud_init_enabled:
            return
        ensure_directory(IMAGES_DIR)
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            passwd_hash = hash_password(self.cfg.password)

            cloud_cfg: Dict[str, object] = {
                "packages": ["qemu-guest-agent"],
                "users": [
                    {
                        "name": self.cfg.login_user,
                        "lock_passwd": False,
                        "sudo": "ALL=(ALL) NOPASSWD:ALL",
                        "shell": "/bin/bash",
                        "passwd": passwd_hash,
                    }
                ],
                "chpasswd": {"expire": False},
                "ssh_pwauth": True,
            }

            if self.cfg.ssh_pubkey:
                cloud_cfg["ssh_authorized_keys"] = [self.cfg.ssh_pubkey]

            runcmd: List[List[str]] = [
                ["systemctl", "enable", "--now", "qemu-guest-agent"],
            ]

            if self.cfg.filesystems:
                mounts = []
                for fs in self.cfg.filesystems:
                    tag = fs.target
                    safe_target = sanitize_mount_target(tag)
                    mount_dir = Path("/mnt") / safe_target
                    mkdir_cmd = ["mkdir", "-p", str(mount_dir)]
                    if mkdir_cmd not in runcmd:
                        runcmd.append(mkdir_cmd)
                    if fs.driver == "virtiofs":
                        fstype = "virtiofs"
                        options = ["defaults", "_netdev"]
                    else:
                        fstype = "9p"
                        options = ["trans=virtio,version=9p2000.L", "_netdev"]
                    if fs.readonly:
                        options.append("ro")
                    mount_entry = [
                        tag,
                        str(mount_dir),
                        fstype,
                        ",".join(options),
                        "0",
                        "0",
                    ]
                    mounts.append(mount_entry)
                if mounts:
                    cloud_cfg["mounts"] = mounts

            if runcmd:
                cloud_cfg["runcmd"] = runcmd

            vendor_user_data = "#cloud-config\n" + yaml.safe_dump(
                cloud_cfg, sort_keys=False, default_flow_style=False
            )

            user_data_payload = vendor_user_data
            override_path = self.cfg.cloud_init_user_data_path
            if override_path:
                override_content = override_path.read_text(encoding="utf-8")
                if override_content.strip():
                    log("INFO", f"Appending user cloud-init data from {override_path}")
                    multipart = MIMEMultipart()
                    vendor_part = MIMEText(
                        vendor_user_data,
                        _subtype="cloud-config",
                        _charset="utf-8",
                    )
                    vendor_part.add_header(
                        "Content-Disposition",
                        "attachment",
                        filename="00-vendor-cloud-config.yaml",
                    )
                    multipart.attach(vendor_part)

                    content_type = detect_cloud_init_content_type(override_content)
                    main_type, _, subtype = content_type.partition("/")
                    if main_type != "text" or not subtype:
                        raise ManagerError(
                            f"Unsupported content type '{content_type}' inferred for CLOUD_INIT_USER_DATA"
                        )
                    user_part = MIMEText(
                        override_content,
                        _subtype=subtype,
                        _charset="utf-8",
                    )
                    user_part.add_header(
                        "Content-Disposition",
                        "attachment",
                        filename=f"99-user-data.{subtype.replace('/', '-')}",
                    )
                    multipart.attach(user_part)
                    user_data_payload = multipart.as_string()
                else:
                    log(
                        "WARN",
                        f"CLOUD_INIT_USER_DATA file {override_path} is empty; only vendor cloud-config will be applied.",
                    )

            (tmp / "user-data").write_text(user_data_payload, encoding="utf-8")
            meta_data = textwrap.dedent(
                f"""
                instance-id: iid-{self.cfg.vm_name}
                local-hostname: {self.cfg.vm_name}
                """
            ).strip() + "\n"
            (tmp / "meta-data").write_text(meta_data, encoding="utf-8")

            cmd = [
                "genisoimage",
                "-output",
                str(self.seed_iso),
                "-volid",
                "cidata",
                "-joliet",
                "-rock",
                str(tmp / "user-data"),
                str(tmp / "meta-data"),
            ]
            run(cmd)

    def _domain_exists(self) -> bool:
        if self.conn is None:
            raise ManagerError("libvirt connection not established")
        try:
            self.conn.lookupByName(self.cfg.vm_name)
            return True
        except libvirt.libvirtError:
            return False

    def _define_domain(self) -> None:
        if self.conn is None:
            raise ManagerError("libvirt connection not established")
        if self._domain_exists():
            log("INFO", f"Domain {self.cfg.vm_name} already defined")
            self.domain = self.conn.lookupByName(self.cfg.vm_name)
            return

        xml = self._render_domain_xml()
        self.domain = self.conn.defineXML(xml)
        if self.domain is None:
            raise ManagerError("Failed to define libvirt domain")
        log("SUCCESS", f"Defined domain {self.cfg.vm_name}")

    def _render_domain_xml(self) -> str:
        qemu_ns = "xmlns:qemu='http://libvirt.org/schemas/domain/qemu/1.0'"
        domain_type = "kvm" if self._kvm_available else "qemu"
        effective_model = self._effective_cpu_model
        host_cpu = self._kvm_available and effective_model.lower() in ("host", "host-passthrough")
        machine_type = self._arch_profile["machine"]

        if host_cpu:
            cpu_xml = "<cpu mode='host-passthrough'/>"
        else:
            cpu_xml = textwrap.dedent(
                f"""
                <cpu mode='custom' match='exact'>
                  <model fallback='allow'>{effective_model}</model>
                </cpu>
                """
            ).strip()

        extra_cmds = ""
        if self.cfg.extra_args:
            extra_cmds = (
                "<qemu:commandline>\n"
                + "\n".join(
                    f"  <qemu:arg value='{arg}'/>"
                    for arg in self.cfg.extra_args.split()
                )
                + "\n</qemu:commandline>"
            )

        boot_order_priority = {dev: idx + 1 for idx, dev in enumerate(self.cfg.boot_order)}

        features = self._arch_profile.get("features", ())
        features_inner = "\n".join(f"            <{feature}/>" for feature in features)
        features_block = f"          <features>\n{features_inner}\n          </features>"

        loader_xml = ""
        if self._arch_profile.get("firmware"):
            if self._firmware_loader_path is None or self._firmware_vars_path is None:
                raise ManagerError("Firmware assets not prepared for this architecture.")
            loader_xml = textwrap.dedent(
                f"""
                <loader readonly='yes' secure='no' type='pflash'>{self._firmware_loader_path}</loader>
                <nvram>{self._firmware_vars_path}</nvram>
                """
            ).strip()
            loader_xml = textwrap.indent(loader_xml, "            ")

        network_order = boot_order_priority.get("network")
        interfaces_xml: List[str] = []
        for idx, nic in enumerate(self.cfg.nics):
            nic_boot_order = network_order if nic.boot else None
            rom_file = None
            if self._ipxe_rom_path is not None:
                rom_file = str(self._ipxe_rom_path)
            ssh_forward = self.cfg.ssh_port if idx == 0 and nic.mode == "user" else None
            pf_list = self.cfg.port_forwards if idx == 0 and nic.mode == "user" else None
            mac_seed = self._network_macs.get(idx)
            nic_xml, resolved_mac = render_network_xml(
                nic,
                ssh_port=ssh_forward,
                mac_address=mac_seed,
                boot_order=nic_boot_order,
                rom_file=rom_file,
                port_forwards=pf_list,
            )
            self._network_macs[idx] = resolved_mac
            interfaces_xml.append(textwrap.indent(nic_xml, "            "))
        interfaces_block = ""
        if interfaces_xml:
            interfaces_block = "\n" + "\n".join(interfaces_xml)

        filesystems_xml: List[str] = []
        for fs in self.cfg.filesystems:
            driver_type = "virtiofs" if fs.driver == "virtiofs" else "path"
            fs_lines = [
                f"<filesystem type='mount' accessmode='{fs.accessmode}'>",
                f"  <driver type='{driver_type}'/>",
                f"  <source dir='{fs.source}'/>",
                f"  <target dir='{fs.target}'/>",
            ]
            if fs.driver == "virtiofs":
                fs_lines.insert(
                    2,
                    "  <binary path='/usr/lib/qemu/virtiofsd'/>",
                )
            if fs.readonly:
                fs_lines.append("  <readonly/>")
            fs_lines.append("</filesystem>")
            filesystems_xml.append(textwrap.indent("\n".join(fs_lines), "            "))
        filesystems_block = ""
        if filesystems_xml:
            filesystems_block = "\n" + "\n".join(filesystems_xml)

        display_xml = ""
        graphics = self.cfg.graphics_type
        if graphics and graphics != "none":
            keymap_attr = f" keymap='{self.cfg.vnc_keymap}'" if self.cfg.vnc_keymap else ""
            if graphics == "vnc":
                display_xml = (
                    f"<graphics type='{graphics}' listen='0.0.0.0' port='{self.cfg.vnc_port}' autoport='no'{keymap_attr}/>"
                )
            else:
                display_xml = (
                    f"<graphics type='{graphics}' listen='0.0.0.0' autoport='yes'{keymap_attr}/>"
                )
            display_xml += "\n" + textwrap.dedent("""\
            <video>
              <model type='virtio' heads='1' primary='yes'>
                <resolution x='1920' y='1080'/>
              </model>
            </video>""")
            display_xml += "\n" + textwrap.dedent("""\
            <channel type='qemu-vdagent'>
              <source>
                <clipboard copypaste='yes'/>
                <mouse mode='client'/>
              </source>
              <target type='virtio' name='com.redhat.spice.0'/>
            </channel>""")

        seed_iso_xml = ""
        if self.seed_iso:
            seed_iso_xml = textwrap.dedent(
                f"""
                <disk type='file' device='cdrom'>
                  <driver name='qemu' type='raw'/>
                  <source file='{self.seed_iso}'/>
                  <target dev='sda' bus='sata'/>
                  <readonly/>
                </disk>
                """
            ).strip()
            seed_iso_xml = textwrap.indent(seed_iso_xml, "            ")

        boot_iso_xml = ""
        if self.boot_iso:
            boot_order_attr = ""
            order = boot_order_priority.get("cdrom")
            if order is not None:
                boot_order_attr = f"\n                  <boot order='{order}'/>"
            boot_iso_xml = textwrap.dedent(
                f"""
                <disk type='file' device='cdrom'>
                  <driver name='qemu' type='raw'/>
                  <source file='{self.boot_iso}'/>
                  <target dev='sdb' bus='sata'/>
                  <readonly/>{boot_order_attr}
                </disk>
                """
            ).strip()
            boot_iso_xml = textwrap.indent(boot_iso_xml, "            ")

        memory_unit = "MiB"
        disk_boot_attr = ""
        order = boot_order_priority.get("hd")
        if order is not None:
            disk_boot_attr = f"\n              <boot order='{order}'/>"

        memory_backing_xml = ""
        if any(fs.driver == "virtiofs" for fs in self.cfg.filesystems):
            memory_backing_xml = textwrap.dedent(
                """
                <memoryBacking>
                  <source type='memfd'/>
                  <access mode='shared'/>
                </memoryBacking>
                """
            ).strip()

        os_lines = [
            f"            <type arch='{self.cfg.arch}' machine='{machine_type}'>hvm</type>",
        ]
        if loader_xml:
            os_lines.append(loader_xml)
        os_block = "\n".join(os_lines)

        xml = f"""
        <domain type='{domain_type}' {qemu_ns}>
          <name>{self.cfg.vm_name}</name>
          <memory unit='{memory_unit}'>{self.cfg.memory_mb}</memory>
          <vcpu placement='static'>{self.cfg.cpus}</vcpu>
          <os>
{os_block}
          </os>
{features_block}
          {memory_backing_xml}
          {cpu_xml}
          <devices>
            <disk type='file' device='disk'>
              <driver name='qemu' type='{self.cfg.image_format}' cache='none'/>
              <source file='{self.work_image}'/>
              <target dev='vda' bus='virtio'/>{disk_boot_attr}
            </disk>
            {seed_iso_xml}
            {boot_iso_xml}
{interfaces_block}
{filesystems_block}
            <channel type='unix'>
              <target type='virtio' name='org.qemu.guest_agent.0'/>
            </channel>
            <serial type='pty'>
              <target port='0'/>
            </serial>
            <console type='pty'>
              <target type='virtio' port='0'/>
            </console>
            {display_xml}
          </devices>
          {extra_cmds}
        </domain>
        """
        return textwrap.dedent(xml).strip()

    def start(self) -> None:
        if self.domain is None:
            raise ManagerError("Domain not defined")
        if self.domain.isActive():  # type: ignore[attr-defined]
            log("INFO", f"Domain {self.cfg.vm_name} already running")
        else:
            try:
                self.domain.create()  # type: ignore[attr-defined]
            except libvirt.libvirtError as exc:
                message = exc.get_error_message() if hasattr(exc, "get_error_message") else str(exc)
                if "cgroup" in message.lower():
                    raise ManagerError(
                        f"libvirt could not access host cgroups: {message}\n"
                        "Run the container with --cgroupns=host to fix this."
                    ) from exc
                raise ManagerError(f"Failed to start domain: {message}") from exc
            log("SUCCESS", f"Domain {self.cfg.vm_name} started")
        if self.cfg.novnc_enabled:
            self.service_manager.start_novnc()

    def wait_for_guest_ready(self, timeout: float = 120.0, interval: float = 3.0) -> bool:
        """Poll QEMU Guest Agent until the guest OS is responsive."""
        log("INFO", "Waiting for guest agent to become ready...")
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                result = subprocess.run(
                    ["virsh", "-c", LIBVIRT_URI, "qemu-agent-command",
                     self.cfg.vm_name, '{"execute":"guest-ping"}'],
                    capture_output=True, text=True, timeout=5,
                )
                if result.returncode == 0:
                    log("SUCCESS", "Guest agent is ready — VM is fully booted")
                    return True
            except subprocess.TimeoutExpired:
                pass
            except Exception:
                pass
            time.sleep(interval)
        log("WARN", f"Guest agent did not respond within {int(timeout)}s (VM may still be booting)")
        return False

    def wait_until_stopped(self) -> None:
        if self.domain is None:
            raise ManagerError("Domain not defined")

        shutdown_requested = False
        _first_sigint_time: Optional[float] = None
        _DOUBLE_PRESS_WINDOW = 3.0  # seconds

        def _do_shutdown():
            nonlocal shutdown_requested
            if shutdown_requested:
                return
            shutdown_requested = True
            log("INFO", "Shutting down VM...")
            try:
                self.domain.shutdown()  # type: ignore[attr-defined]
            except libvirt.libvirtError:
                try:
                    self.domain.destroy()  # type: ignore[attr-defined]
                except libvirt.libvirtError:
                    log("INFO", "libvirt connection lost; VM process will terminate with container")

        def _request_shutdown(signum, frame):
            nonlocal _first_sigint_time
            # SIGTERM always shuts down immediately (Docker stop, orchestrators)
            if signum == signal.SIGTERM:
                sig_name = signal.Signals(signum).name
                log("INFO", f"{sig_name} received, shutting down VM")
                _do_shutdown()
                return
            # SIGINT (Ctrl+C) uses double-press guard
            now = time.time()
            if _first_sigint_time is not None and (now - _first_sigint_time) < _DOUBLE_PRESS_WINDOW:
                log("INFO", "Second Ctrl+C received, shutting down VM")
                _do_shutdown()
            else:
                _first_sigint_time = now
                log("WARN", "Press Ctrl+C again within 3s to shutdown the VM (or Ctrl+] to detach console)")

        prev_sigterm = signal.signal(signal.SIGTERM, _request_shutdown)
        prev_sigint = signal.signal(signal.SIGINT, _request_shutdown)
        try:
            log("INFO", f"Waiting for domain {self.cfg.vm_name} to stop")
            while True:
                try:
                    active = self.domain.isActive()  # type: ignore[attr-defined]
                except libvirt.libvirtError:
                    log("INFO", f"Domain {self.cfg.vm_name} is no longer active")
                    return
                if not active:
                    log("INFO", f"Domain {self.cfg.vm_name} is no longer active")
                    return
                time.sleep(1)
        finally:
            signal.signal(signal.SIGTERM, prev_sigterm)
            signal.signal(signal.SIGINT, prev_sigint)

    def cleanup(self) -> None:
        if self.domain is not None:
            try:
                if self.domain.isActive():  # type: ignore[attr-defined]
                    log("INFO", f"Shutting down domain {self.cfg.vm_name}")
                    self.domain.destroy()  # type: ignore[attr-defined]
            except libvirt.libvirtError:
                log("DEBUG", f"Could not destroy domain {self.cfg.vm_name} (libvirt connection lost)")
            try:
                self.domain.undefine()
            except libvirt.libvirtError:
                log("DEBUG", f"Could not undefine domain {self.cfg.vm_name} (libvirt connection lost)")

        # Safety net: kill any remaining qemu processes to prevent orphans
        self._kill_remaining_qemu()

        if not self.cfg.persist and self.vm_dir.exists():
            try:
                shutil.rmtree(self.vm_dir)
            except OSError:
                log("WARN", f"Failed to remove {self.vm_dir}")

    def _kill_remaining_qemu(self) -> None:
        """Kill any QEMU processes still running inside this container."""
        try:
            result = subprocess.run(
                ["pgrep", "-f", "qemu-system"],
                capture_output=True, text=True, check=False,
            )
            if result.returncode != 0:
                return
            for line in result.stdout.strip().splitlines():
                pid = line.strip()
                if pid:
                    log("WARN", f"Killing orphaned QEMU process (PID {pid})")
                    try:
                        os.kill(int(pid), signal.SIGKILL)
                    except (OSError, ValueError):
                        pass
        except FileNotFoundError:
            pass


def load_distro_config(distro: str, config_path: Path = DEFAULT_CONFIG_PATH) -> Dict[str, str]:
    if not config_path.exists():
        raise ManagerError(f"Distribution config missing: {config_path}")
    data = yaml.safe_load(config_path.read_text())
    distros = data.get("distributions", {})
    if distro not in distros:
        available = sorted(distros.keys())
        available_list = "\n    ".join(available)
        raise ManagerError(
            f"Unknown distro '{distro}'.\n"
            f"  Available distributions:\n"
            f"    {available_list}\n"
            f"  Use --list-distros to see details."
        )
    return distros[distro]


def parse_env() -> VMConfig:
    distro = get_env("DISTRO", "ubuntu-2404")
    distro_info = load_distro_config(distro)

    memory_mb = parse_int_env("MEMORY", "4096")
    cpus = parse_int_env("CPUS", "2")
    disk_size = validate_disk_size(get_env("DISK_SIZE", "20G") or "20G")

    display = (get_env("GRAPHICS") or "none").strip().lower() or "none"
    novnc_enabled = display == "novnc"
    if novnc_enabled:
        graphics_type = "vnc"
    else:
        graphics_type = display

    vnc_port = parse_int_env("VNC_PORT", "5900", min_val=1, max_val=65535)
    vnc_keymap = (get_env("VNC_KEYMAP") or "").strip()
    novnc_port = parse_int_env("NOVNC_PORT", "6080", min_val=1, max_val=65535)
    if novnc_enabled and graphics_type != "vnc":
        raise ManagerError("noVNC requires a VNC graphics backend")

    base_image_override = get_env("BASE_IMAGE")
    if base_image_override is not None:
        base_image_override = base_image_override.strip()
        if not base_image_override:
            base_image_override = None

    blank_disk_explicit = get_env("BLANK_DISK") is not None
    blank_work_disk = get_env_bool("BLANK_DISK", False)
    if base_image_override and base_image_override.lower() == "blank":
        blank_work_disk = True
        base_image_override = None

    boot_iso = get_env("BOOT_ISO")
    if boot_iso is not None:
        boot_iso = boot_iso.strip() or None

    boot_iso_url = get_env("BOOT_ISO_URL")
    if boot_iso_url is not None:
        boot_iso_url = boot_iso_url.strip() or None

    # Auto-detect: if BOOT_ISO looks like a URL, treat it as BOOT_ISO_URL
    if boot_iso and boot_iso.startswith(("http://", "https://")):
        if boot_iso_url:
            raise ManagerError("Set only one of BOOT_ISO or BOOT_ISO_URL, not both.")
        boot_iso_url = boot_iso
        boot_iso = None

    if boot_iso and boot_iso_url:
        raise ManagerError("Set only one of BOOT_ISO or BOOT_ISO_URL, not both.")

    iso_requested = bool(boot_iso or boot_iso_url)

    boot_order_raw = get_env("BOOT_ORDER", "hd")
    boot_order_input = [item.strip().lower() for item in boot_order_raw.split(",") if item.strip()]
    valid_boot_devices = {"hd", "cdrom", "network"}
    boot_order = []
    for dev in boot_order_input:
        if dev not in valid_boot_devices:
            raise ManagerError(f"Unknown BOOT_ORDER device '{dev}'. Supported: hd, cdrom, network")
        boot_order.append(dev)
    if not boot_order:
        boot_order = ["hd"]
    if iso_requested and "cdrom" not in boot_order:
        boot_order = ["cdrom"] + boot_order
    if iso_requested and base_image_override is None and not blank_disk_explicit:
        # Installing from ISO without an explicit base image -> default to a blank disk.
        blank_work_disk = True
    cloud_init_raw = get_env("CLOUD_INIT")
    if cloud_init_raw is not None:
        cloud_init_enabled = cloud_init_raw.lower() in TRUTHY
    elif iso_requested:
        cloud_init_enabled = False
        log("INFO", "BOOT_ISO detected; auto-disabling cloud-init (set CLOUD_INIT=1 to override)")
    else:
        cloud_init_enabled = True
    cloud_init_user_data_env = get_env("CLOUD_INIT_USER_DATA")
    cloud_init_user_data_path: Optional[Path] = None
    if cloud_init_user_data_env:
        candidate = Path(cloud_init_user_data_env)
        if not candidate.exists():
            raise ManagerError(
                f"CLOUD_INIT_USER_DATA file not found: {candidate}\n"
                "Ensure the file is bind-mounted into the container (e.g. -v /host/path:/container/path:ro)"
            )
        if not candidate.is_file():
            raise ManagerError(
                f"CLOUD_INIT_USER_DATA must point to a regular file: {candidate}"
            )
        cloud_init_user_data_path = candidate

    CLOUD_INIT_HEADERS = {"#cloud-config", "#!", "#cloud-boothook", "#include", "#part-handler"}

    if cloud_init_user_data_path:
        try:
            first_line = cloud_init_user_data_path.read_text().split("\n", 1)[0].strip()
        except OSError as exc:
            raise ManagerError(f"Cannot read CLOUD_INIT_USER_DATA: {exc}")
        if not any(first_line.startswith(h) for h in CLOUD_INIT_HEADERS):
            log(
                "WARN",
                f"CLOUD_INIT_USER_DATA does not start with a recognized cloud-init header "
                f"(got: '{first_line[:60]}'). "
                "Expected: #cloud-config, #!/bin/bash, #cloud-boothook, #include, or #part-handler"
            )
        if first_line == "#cloud-config":
            try:
                content = cloud_init_user_data_path.read_text()
                parsed = yaml.safe_load(content)
                if not isinstance(parsed, dict):
                    log("WARN", "CLOUD_INIT_USER_DATA: #cloud-config should contain a YAML mapping, got " + type(parsed).__name__)
            except yaml.YAMLError as exc:
                raise ManagerError(f"CLOUD_INIT_USER_DATA contains invalid YAML: {exc}")

    ipxe_enabled = get_env_bool("IPXE_ENABLE", False)
    ipxe_rom_override = (get_env("IPXE_ROM_PATH") or "").strip() or None

    distro_arch_raw = distro_info.get("arch")
    arch_env = get_env("ARCH")
    if arch_env is not None:
        arch_candidate = arch_env.strip() or "x86_64"
    elif distro_arch_raw:
        arch_candidate = str(distro_arch_raw).strip() or "x86_64"
    else:
        arch_candidate = "x86_64"

    arch_lower = arch_candidate.lower()
    arch_key = ARCH_ALIASES.get(arch_lower, arch_lower)
    if arch_key not in SUPPORTED_ARCHES:
        supported = ", ".join(sorted(SUPPORTED_ARCHES.keys()))
        raise ManagerError(f"Unsupported ARCH '{arch_candidate}'. Supported: {supported}")

    if distro_arch_raw:
        distro_arch_lower = str(distro_arch_raw).strip().lower()
        distro_arch_key = ARCH_ALIASES.get(distro_arch_lower, distro_arch_lower)
        if distro_arch_key not in SUPPORTED_ARCHES:
            raise ManagerError(
                f"Distribution '{distro}' declares unsupported arch '{distro_arch_raw}'."
            )
        if arch_env is not None and distro_arch_key != arch_key:
            raise ManagerError(
                f"ARCH='{arch_candidate}' does not match distribution '{distro}' arch '{distro_arch_raw}'."
            )
        arch = distro_arch_key
    else:
        arch = arch_key
    cpu_model = get_env("CPU_MODEL", "host")
    extra_args = get_env("EXTRA_ARGS", "")

    guest_password = get_env("GUEST_PASSWORD", "password")
    ssh_port = parse_int_env("SSH_PORT", "2222", min_val=1, max_val=65535)

    vm_name = derive_vm_name(distro, iso_mode=iso_requested)

    network_mode_map = {
        "nat": "user",
        "bridge": "bridge",
        "direct": "direct",
    }

    def get_env_indexed(name: str, index: int) -> Optional[str]:
        """Get indexed env var. E.g. get_env_indexed("NETWORK_MODE", 2) -> NETWORK2_MODE."""
        if index == 1:
            return get_env(name)
        prefix, rest = name.split("_", 1)
        return get_env(f"{prefix}{index}_{rest}")

    def build_nic(index: int) -> Optional[NicConfig]:
        mode_raw = get_env_indexed("NETWORK_MODE", index)
        if mode_raw is None or not mode_raw.strip():
            if index == 1:
                mode_raw = "nat"
            else:
                return None
        mode_key = network_mode_map.get(mode_raw.strip().lower())
        if mode_key is None:
            suffix = "" if index == 1 else str(index)
            raise ManagerError(
                f"Unsupported NETWORK{suffix}_MODE '{mode_raw}'. Expected one of nat, bridge, direct."
            )

        bridge_name = None
        direct_device = None
        if mode_key == "bridge":
            bridge_name = get_env_indexed("NETWORK_BRIDGE", index)
            if not bridge_name:
                suffix = "" if index == 1 else str(index)
                raise ManagerError(
                    f"NETWORK{suffix}_BRIDGE is required when NETWORK{suffix}_MODE=bridge"
                )
            bridge_name = bridge_name.strip()
        elif mode_key == "direct":
            direct_device = get_env_indexed("NETWORK_DIRECT_DEV", index)
            if not direct_device:
                suffix = "" if index == 1 else str(index)
                raise ManagerError(
                    f"NETWORK{suffix}_DIRECT_DEV is required when NETWORK{suffix}_MODE=direct"
                )
            direct_device = direct_device.strip()

        mac_raw = get_env_indexed("NETWORK_MAC", index)
        mac_address = mac_raw.strip().lower() if mac_raw else None
        if mac_address and not MAC_ADDRESS_RE.match(mac_address):
            suffix = "" if index == 1 else str(index)
            raise ManagerError(
                f"Invalid NETWORK{suffix}_MAC '{mac_raw}'. Use format aa:bb:cc:dd:ee:ff"
            )
        if not mac_address:
            mac_address = deterministic_mac(f"{vm_name}:{index}")

        model_raw = get_env_indexed("NETWORK_MODEL", index)
        model = (model_raw.strip().lower() if model_raw else "virtio")
        if model not in SUPPORTED_NETWORK_MODELS:
            supported_models = ", ".join(sorted(SUPPORTED_NETWORK_MODELS))
            suffix = "" if index == 1 else str(index)
            raise ManagerError(
                f"Unsupported NETWORK{suffix}_MODEL '{model_raw}'. Supported: {supported_models}"
            )

        nic = NicConfig(
            mode=mode_key,
            bridge_name=bridge_name,
            direct_device=direct_device,
            mac_address=mac_address,
            model=model,
            boot=False,
        )

        boot_override = get_env_indexed("NETWORK_BOOT", index)
        if boot_override is not None:
            nic.boot = boot_override.strip().lower() in TRUTHY
        return nic

    def build_filesystem(index: int) -> Optional[FilesystemConfig]:
        source_raw = get_env_indexed("FILESYSTEM_SOURCE", index)
        target_raw = get_env_indexed("FILESYSTEM_TARGET", index)
        driver_raw = get_env_indexed("FILESYSTEM_DRIVER", index)
        accessmode_raw = get_env_indexed("FILESYSTEM_ACCESSMODE", index)
        readonly_raw = get_env_indexed("FILESYSTEM_READONLY", index)

        trigger_values = [source_raw, target_raw, driver_raw, accessmode_raw]
        has_value = any(
            value is not None and value.strip() for value in trigger_values if isinstance(value, str)
        )
        if not has_value and readonly_raw is not None:
            if readonly_raw.strip().lower() in TRUTHY:
                has_value = True
        if not has_value:
            return None

        suffix = "" if index == 1 else str(index)

        if source_raw is None or not source_raw.strip():
            raise ManagerError(f"FILESYSTEM{suffix}_SOURCE is required when configuring a filesystem share")
        # Auto-derive target from last segment of source path
        if target_raw is None or not target_raw.strip():
            derived = Path(source_raw.strip()).name
            if not derived or derived in (".", "/"):
                raise ManagerError(
                    f"FILESYSTEM{suffix}_TARGET is required (could not auto-derive from source '{source_raw}')"
                )
            target_raw = derived
            log("INFO", f"FILESYSTEM{suffix}_TARGET auto-derived as '{derived}' from source path")

        readonly = False
        if readonly_raw is not None:
            readonly = readonly_raw.strip().lower() in TRUTHY

        source_path = Path(source_raw).expanduser()
        if source_path.exists():
            if not source_path.is_dir():
                raise ManagerError(f"FILESYSTEM{suffix}_SOURCE {source_path} must point to a directory")
        else:
            if readonly:
                raise ManagerError(
                    f"FILESYSTEM{suffix}_SOURCE {source_path} does not exist and cannot be created while readonly"
                )
            ensure_directory(source_path)
        if not source_path.is_dir():
            raise ManagerError(f"FILESYSTEM{suffix}_SOURCE {source_path} must point to a directory")

        target = target_raw.strip()
        if "/" in target:
            raise ManagerError(f"FILESYSTEM{suffix}_TARGET '{target}' must be a simple tag without '/' characters")

        driver = (driver_raw or "virtiofs").strip().lower()
        if driver not in ("virtiofs", "9p"):
            raise ManagerError(
                f"Unsupported FILESYSTEM{suffix}_DRIVER '{driver}'. Supported: virtiofs, 9p"
            )

        accessmode = (accessmode_raw or "passthrough").strip().lower()
        if accessmode not in {"passthrough", "mapped", "squash"}:
            raise ManagerError(
                f"Unsupported FILESYSTEM{suffix}_ACCESSMODE '{accessmode}'. "
                "Supported values: passthrough, mapped, squash."
            )
        if driver == "virtiofs" and accessmode != "passthrough":
            raise ManagerError(
                f"FILESYSTEM{suffix}_ACCESSMODE='{accessmode}' is not supported with virtiofs. "
                "virtiofs only supports 'passthrough'. Use FILESYSTEM_DRIVER=9p for 'mapped' or 'squash'."
            )

        return FilesystemConfig(
            source=source_path,
            target=target,
            driver=driver,
            accessmode=accessmode,
            readonly=readonly,
        )

    nics: List[NicConfig] = []
    primary_nic = build_nic(1)
    if primary_nic is None:
        raise ManagerError("Failed to configure primary network interface")
    nics.append(primary_nic)

    idx = 2
    while True:
        nic = build_nic(idx)
        if nic is None:
            break
        nics.append(nic)
        idx += 1

    filesystems: List[FilesystemConfig] = []
    fs_index = 1
    while True:
        filesystem = build_filesystem(fs_index)
        if filesystem is None:
            break
        filesystems.append(filesystem)
        fs_index += 1

    ipxe_rom_path: Optional[str] = None
    if ipxe_enabled:
        if "network" in boot_order:
            boot_order = ["network"] + [dev for dev in boot_order if dev != "network"]
        else:
            boot_order = ["network"] + boot_order
        primary_nic.boot = True
        default_roms = IPXE_DEFAULT_ROMS.get(arch, {})
        if ipxe_rom_override:
            ipxe_rom_path = ipxe_rom_override
        else:
            default_rom = default_roms.get(primary_nic.model)
            if default_rom:
                ipxe_rom_path = str(default_rom)
        if not ipxe_rom_path:
            raise ManagerError(
                "IPXE_ENABLE=1 requires IPXE_ROM_PATH when a default ROM is not available for "
                f"ARCH='{arch}' with NETWORK_MODEL='{primary_nic.model}'."
            )
        rom_candidate = Path(ipxe_rom_path)
        if not rom_candidate.exists():
            raise ManagerError(
                f"iPXE ROM not found at {rom_candidate}. Install ipxe-qemu inside the image or override IPXE_ROM_PATH."
            )
        if primary_nic.mode == "user":
            log(
                "WARN",
                "IPXE_ENABLE=1 with NETWORK_MODE=nat relies on the built-in user-mode DHCP/TFTP. "
                "For real PXE environments prefer bridge or direct networking.",
            )

    persist_default = bool(_DATA_DIR)
    persist = get_env_bool("PERSIST", persist_default)
    if _DATA_DIR and os.environ.get("PERSIST") is None:
        log("INFO", "Data volume detected; defaulting PERSIST=1 (override with PERSIST=0)")
    force_iso = get_env_bool("FORCE_ISO", False)
    ssh_pubkey = get_env("SSH_PUBKEY")
    redfish_user = get_env("REDFISH_USERNAME", "admin")
    redfish_password = get_env("REDFISH_PASSWORD", "password")
    redfish_port = parse_int_env("REDFISH_PORT", "8443", min_val=1, max_val=65535)
    redfish_system_id = get_env("REDFISH_SYSTEM_ID", vm_name)
    redfish_enabled = get_env_bool("REDFISH_ENABLE", False)

    # --- PORT_FWD parsing ---
    port_forwards: List[PortForward] = []
    port_fwd_raw = get_env("PORT_FWD")
    if port_fwd_raw:
        for entry in port_fwd_raw.split(","):
            entry = entry.strip()
            if not entry:
                continue
            parts = entry.split(":")
            if len(parts) != 2:
                raise ManagerError(
                    f"Invalid PORT_FWD entry '{entry}': expected format host_port:guest_port"
                )
            try:
                host_port = int(parts[0])
                guest_port = int(parts[1])
            except ValueError:
                raise ManagerError(
                    f"Invalid PORT_FWD entry '{entry}': ports must be integers"
                )
            if not (1 <= host_port <= 65535):
                raise ManagerError(
                    f"Invalid PORT_FWD entry '{entry}': host port {host_port} out of range (1-65535)"
                )
            if not (1 <= guest_port <= 65535):
                raise ManagerError(
                    f"Invalid PORT_FWD entry '{entry}': guest port {guest_port} out of range (1-65535)"
                )
            port_forwards.append(PortForward(host_port=host_port, guest_port=guest_port))

    active_ports = {"SSH_PORT": ssh_port}
    if graphics_type == "vnc" or novnc_enabled:
        active_ports["VNC_PORT"] = vnc_port
    if novnc_enabled:
        active_ports["NOVNC_PORT"] = novnc_port
    if redfish_enabled:
        active_ports["REDFISH_PORT"] = redfish_port
    for pf in port_forwards:
        active_ports[f"PORT_FWD({pf.host_port}:{pf.guest_port})"] = pf.host_port

    seen: Dict[int, str] = {}
    for label, port in active_ports.items():
        if port in seen:
            raise ManagerError(
                f"Port conflict: {label}={port} collides with {seen[port]}={port}. "
                "Each service needs a unique port."
            )
        seen[port] = label

    return VMConfig(
        distro=distro,
        image_url=distro_info["url"],
        login_user=distro_info["user"],
        image_format=distro_info.get("format", "qcow2"),
        distro_name=distro_info["name"],
        memory_mb=memory_mb,
        cpus=cpus,
        disk_size=disk_size,
        display=display,
        graphics_type=graphics_type,
        arch=arch,
        cpu_model=cpu_model,
        extra_args=extra_args,
        novnc_enabled=novnc_enabled,
        vnc_port=vnc_port,
        vnc_keymap=vnc_keymap,
        novnc_port=novnc_port,
        base_image_path=base_image_override,
        blank_work_disk=blank_work_disk,
        boot_iso_path=boot_iso,
        boot_iso_url=boot_iso_url,
        boot_order=boot_order,
        cloud_init_enabled=cloud_init_enabled,
        cloud_init_user_data_path=cloud_init_user_data_path,
        password=guest_password,
        ssh_port=ssh_port,
        vm_name=vm_name,
        persist=persist,
        force_iso=force_iso,
        ssh_pubkey=ssh_pubkey,
        redfish_user=redfish_user,
        redfish_password=redfish_password,
        redfish_port=redfish_port,
        redfish_system_id=redfish_system_id,
        redfish_enabled=redfish_enabled,
        nics=nics,
        ipxe_enabled=ipxe_enabled,
        ipxe_rom_path=ipxe_rom_path,
        filesystems=filesystems,
        port_forwards=port_forwards,
    )


def run_console(vm_name: str) -> int:
    """Attach to the guest console via virsh."""
    cmd = ["virsh", "-c", LIBVIRT_URI, "console", vm_name]
    log("INFO", "Attaching to VM console (Ctrl+] to exit)")
    proc = subprocess.Popen(cmd)

    def _terminate_console(signum, frame):
        proc.terminate()

    prev_sigterm = signal.signal(signal.SIGTERM, _terminate_console)
    try:
        return proc.wait()
    except KeyboardInterrupt:
        proc.send_signal(signal.SIGINT)
        return proc.wait()
    finally:
        signal.signal(signal.SIGTERM, prev_sigterm)


def list_distros(config_path: Path = DEFAULT_CONFIG_PATH, arch_filter: Optional[str] = None) -> None:
    """Print available distributions and exit, optionally filtered by arch."""
    if not config_path.exists():
        log("ERROR", f"Distribution config missing: {config_path}")
        return
    data = yaml.safe_load(config_path.read_text())
    distros = data.get("distributions", {})
    if not distros:
        log("WARN", "No distributions found")
        return

    if arch_filter:
        arch_filter_norm = ARCH_ALIASES.get(arch_filter.lower(), arch_filter.lower())
        distros = {
            k: v for k, v in distros.items()
            if ARCH_ALIASES.get(v.get("arch", "x86_64").lower(), v.get("arch", "x86_64").lower()) == arch_filter_norm
        }
        if not distros:
            log("WARN", f"No distributions found for arch '{arch_filter}'")
            return
        log("INFO", f"Showing distributions for arch: {arch_filter_norm}")

    max_key = max(len(k) for k in distros)
    for key in sorted(distros):
        info = distros[key]
        name = info.get("name", key)
        arch = info.get("arch", "x86_64")
        user = info.get("user", "?")
        print(f"  {key:<{max_key}}  {name}  (arch={arch}, user={user})")


_SENSITIVE_FIELDS = {"password", "redfish_password"}


def show_config(cfg: VMConfig) -> None:
    """Print the resolved VM configuration and exit."""
    import dataclasses

    for field in dataclasses.fields(cfg):
        value = getattr(cfg, field.name)
        if field.name in _SENSITIVE_FIELDS:
            print(f"  {field.name}: ********")
        elif isinstance(value, list) and value and hasattr(value[0], "__dataclass_fields__"):
            print(f"  {field.name}:")
            for i, item in enumerate(value):
                print(f"    [{i}]:")
                for sub_field in dataclasses.fields(item):
                    sub_value = getattr(item, sub_field.name)
                    print(f"      {sub_field.name}: {sub_value}")
        else:
            print(f"  {field.name}: {value}")


def print_startup_banner(cfg: VMConfig) -> None:
    """Print a visually distinct access-info banner after VM starts."""
    lines: List[str] = []
    lines.append(f"  VM: {cfg.vm_name} ({cfg.distro_name})")
    lines.append(f"  Arch: {cfg.arch} | Memory: {cfg.memory_mb} MiB | CPUs: {cfg.cpus}")

    has_user_nic = any(nic.mode == "user" for nic in cfg.nics)
    ports_to_publish: List[str] = []

    if has_user_nic and cfg.ssh_port:
        if cfg.cloud_init_enabled:
            lines.append(f"  SSH:  ssh -p {cfg.ssh_port} {cfg.login_user}@localhost")
        else:
            lines.append(f"  SSH:  port {cfg.ssh_port} -> guest:22")
        ports_to_publish.append(f"-p {cfg.ssh_port}:{cfg.ssh_port}")
    if cfg.cloud_init_enabled:
        lines.append(f"  User: {cfg.login_user}  Pass: {cfg.password}")
    if cfg.novnc_enabled:
        lines.append(f"  VNC:  https://localhost:{cfg.novnc_port}/vnc.html")
        ports_to_publish.append(f"-p {cfg.novnc_port}:{cfg.novnc_port}")
    elif cfg.graphics_type == "vnc":
        lines.append(f"  VNC:  localhost:{cfg.vnc_port}")
        ports_to_publish.append(f"-p {cfg.vnc_port}:{cfg.vnc_port}")
    if cfg.redfish_enabled:
        lines.append(f"  Redfish: https://localhost:{cfg.redfish_port}/")
        ports_to_publish.append(f"-p {cfg.redfish_port}:{cfg.redfish_port}")
    if cfg.port_forwards and has_user_nic:
        fwd_strs = [f"{pf.host_port}->{pf.guest_port}" for pf in cfg.port_forwards]
        lines.append(f"  Ports: {', '.join(fwd_strs)}")
        for pf in cfg.port_forwards:
            ports_to_publish.append(f"-p {pf.host_port}:{pf.host_port}")

    if ports_to_publish:
        lines.append("")
        lines.append(f"  Ensure docker ports are published:")
        lines.append(f"    {' '.join(ports_to_publish)}")

    max_len = max(len(line) for line in lines)
    border_len = max_len + 2
    banner_colour = "\033[0;36m"
    reset = "\033[0m"
    print(f"{banner_colour}{'=' * border_len}{reset}", flush=True)
    for line in lines:
        print(f"{banner_colour}{line}{reset}", flush=True)
    print(f"{banner_colour}{'=' * border_len}{reset}", flush=True)


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description="Docker-VM-Runner libvirt manager")
    parser.add_argument("--no-console", action="store_true", help="Do not attach to console")
    parser.add_argument("--list-distros", nargs="?", const="", default=None, metavar="ARCH",
                        help="List available distributions and exit (optionally filter by arch: x86_64, aarch64, arm64, amd64)")
    parser.add_argument("--show-config", action="store_true", help="Show resolved VM configuration and exit")
    parser.add_argument("--dry-run", action="store_true", help="Validate config and environment, then exit")
    args = parser.parse_args(argv)

    if args.list_distros is not None:
        arch_filter = args.list_distros if args.list_distros else None
        list_distros(arch_filter=arch_filter)
        return 0

    no_console_explicit = get_env("NO_CONSOLE")
    no_console = args.no_console or get_env_bool("NO_CONSOLE", False)
    # Auto-infer NO_CONSOLE when GRAPHICS=novnc (serial console is rarely useful alongside GUI)
    graphics_env = get_env("GRAPHICS", "")
    if not no_console and no_console_explicit is None and graphics_env.strip().lower() == "novnc":
        no_console = True
        log("INFO", "GRAPHICS=novnc detected; auto-disabling serial console (set NO_CONSOLE=0 to override)")
    console_requested = not no_console
    if console_requested and not has_controlling_tty():
        log(
            "INFO",
            "No TTY detected; running in headless mode. The serial console will not be attached.",
        )
        console_requested = False

    try:
        cfg = parse_env()
    except ManagerError as exc:
        log("ERROR", str(exc))
        return 1

    if args.show_config:
        show_config(cfg)
        return 0

    if args.dry_run:
        log("INFO", "=== Configuration ===")
        show_config(cfg)
        log("INFO", "=== Environment Checks ===")
        # KVM check
        if kvm_available():
            log("SUCCESS", "KVM:         available (/dev/kvm)")
        else:
            if get_env_bool("REQUIRE_KVM", False):
                log("ERROR", "KVM:         NOT available (REQUIRE_KVM=1 is set — will fail)")
            else:
                log("WARN", "KVM:         NOT available (will use TCG — 10-50x slower)")
        # Boot order
        log("INFO", f"Boot order:  {', '.join(cfg.boot_order)}")
        # Persist
        if cfg.persist:
            log("INFO", f"Persistence: enabled (data dir: {IMAGES_DIR})")
        else:
            log("INFO", "Persistence: disabled (ephemeral)")
        # Cloud-init
        if cfg.cloud_init_enabled:
            log("INFO", f"Cloud-init:  enabled (user={cfg.login_user})")
        else:
            log("INFO", "Cloud-init:  disabled")
        # ISO
        if cfg.boot_iso_path:
            iso_path = Path(cfg.boot_iso_path)
            if iso_path.exists():
                log("SUCCESS", f"Boot ISO:    {iso_path} (found)")
            else:
                log("ERROR", f"Boot ISO:    {iso_path} (NOT FOUND)")
        elif cfg.boot_iso_url:
            log("INFO", f"Boot ISO:    {cfg.boot_iso_url} (will download)")
        # Network
        for idx, nic in enumerate(cfg.nics, start=1):
            label = f"NIC #{idx}"
            log("INFO", f"{label}:       mode={nic.mode}, model={nic.model}, mac={nic.mac_address or 'auto'}")
        log("INFO", "=== Dry-run complete (no VM started) ===")
        print_startup_banner(cfg)
        return 0

    # Compact startup log
    iso_boot = bool(cfg.boot_iso_path or cfg.boot_iso_url)
    if iso_boot:
        iso_display = cfg.boot_iso_path or cfg.boot_iso_url
        log("INFO", f"Boot source: ISO ({iso_display})")
    else:
        log("INFO", f"Distribution: {cfg.distro} ({cfg.distro_name})")
    log("INFO", f"VM: {cfg.vm_name} | Memory: {cfg.memory_mb} MiB | CPUs: {cfg.cpus} | Disk: {cfg.disk_size}")

    has_user_nic = any(nic.mode == "user" for nic in cfg.nics)
    for idx, nic in enumerate(cfg.nics, start=1):
        label = "Primary NIC" if idx == 1 else f"NIC #{idx}"
        log("INFO", f"{label}: mode={nic.mode}, model={nic.model}")
    if not has_user_nic and cfg.ssh_port:
        log("WARN", f"SSH_PORT={cfg.ssh_port} is set but no user-mode NIC; SSH port forwarding not active")
    if cfg.port_forwards and not has_user_nic:
        log("WARN", "PORT_FWD is set but no user-mode NIC; port forwarding not active")
    if cfg.ipxe_enabled:
        log("INFO", f"iPXE enabled (ROM: {cfg.ipxe_rom_path or '<default>'})")
    if cfg.filesystems:
        for idx, fs in enumerate(cfg.filesystems, start=1):
            mode = "ro" if fs.readonly else "rw"
            log("INFO", f"Filesystem #{idx}: {fs.source} -> /mnt/{sanitize_mount_target(fs.target)} ({fs.driver}, {mode})")
    log("INFO", f"Boot order: {', '.join(cfg.boot_order)}")

    ensure_directory(STATE_DIR)

    service_manager = ServiceManager(cfg)
    service_manager.start()

    vm_mgr = VMManager(cfg, service_manager)
    vm_mgr.connect()
    vm_started = False
    try:
        vm_mgr.prepare()
        vm_mgr.start()
        vm_started = True
        print_startup_banner(cfg)
        # Background guest-agent readiness check (non-blocking for console mode)
        if not console_requested:
            vm_mgr.wait_for_guest_ready(timeout=120)
        retcode = 0
        if not console_requested:
            vm_mgr.wait_until_stopped()
        else:
            retcode = run_console(cfg.vm_name)
            if retcode != 0:
                log("WARN", f"Console exited with status {retcode}")
        return retcode
    except ManagerError as exc:
        log("ERROR", str(exc))
        return 1
    except Exception as exc:
        log("ERROR", f"Unexpected error: {exc}")
        log("ERROR", "This is likely a bug. Please report it at https://github.com/munenick/docker-vm-runner/issues")
        import traceback
        traceback.print_exc()
        return 1
    finally:
        if vm_started and cfg.persist:
            vm_mgr._mark_installed()
        vm_mgr.cleanup()
        vm_mgr.close()
        service_manager.stop()


if __name__ == "__main__":
    sys.exit(main())
