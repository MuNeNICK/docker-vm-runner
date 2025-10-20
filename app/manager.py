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
import os
import random
import re
import shutil
import signal
import string
import socket
import subprocess
import sys
import tempfile
import textwrap
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, List, Optional, Tuple

try:
    import yaml  # type: ignore
except ImportError:  # pragma: no cover
    yaml = None

try:
    import bcrypt  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit("bcrypt is required but not installed") from exc

try:
    import libvirt  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit(f"libvirt python bindings not available: {exc}")


DEFAULT_CONFIG_PATH = Path("/config/distros.yaml")
IMAGES_DIR = Path("/images")
BASE_IMAGES_DIR = IMAGES_DIR / "base"
VM_IMAGES_DIR = IMAGES_DIR / "vms"
STATE_DIR = Path("/var/lib/docker-vm-runner")
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
    "x86_64": "x86_64",
    "amd64": "x86_64",
    "arm64": "aarch64",
    "aarch64": "aarch64",
}

NETWORK_MODEL_ALIASES = {
    "virtio": "virtio",
    "virtio-net": "virtio",
    "virtio_net": "virtio",
    "virtio-net-pci": "virtio",
    "e1000": "e1000",
    "e1000e": "e1000e",
    "rtl8139": "rtl8139",
    "ne2k": "ne2k_pci",
    "ne2k_pci": "ne2k_pci",
    "pcnet": "pcnet",
    "pcnet32": "pcnet",
    "vmxnet3": "vmxnet3",
}

SUPPORTED_NETWORK_MODELS = set(NETWORK_MODEL_ALIASES.values())

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


def log(level: str, message: str) -> None:
    """Lightweight structured logging compatible with existing colour expectation."""
    colours = {
        "INFO": "\033[0;34m",
        "WARN": "\033[1;33m",
        "ERROR": "\033[0;31m",
        "SUCCESS": "\033[0;32m",
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


def detect_container_id() -> Optional[str]:
    """Try to extract the container ID from cgroup membership."""
    cgroup_path = Path("/proc/self/cgroup")
    try:
        data = cgroup_path.read_text(encoding="utf-8")
    except (OSError, UnicodeError):  # pragma: no cover - unlikely but safe
        return None

    for line in data.splitlines():
        parts = line.strip().split(":", 2)
        if len(parts) != 3:
            continue
        path = parts[2]
        for segment in reversed(path.split("/")):
            seg = segment.strip().lower()
            if 12 <= len(seg) <= 64 and all(ch in string.hexdigits for ch in seg):
                return seg
    return None


def derive_vm_name(distro: str) -> str:
    explicit = get_env("GUEST_NAME")
    if explicit:
        return explicit.strip()

    container_name = os.environ.get("CONTAINER_NAME")
    if container_name:
        candidate = container_name.strip()
        if candidate:
            return candidate

    container_id = detect_container_id()
    if container_id:
        return container_id[:12]

    hostname_env = os.environ.get("HOSTNAME")
    if hostname_env:
        candidate = hostname_env.strip()
        if candidate:
            return candidate

    return distro


def deterministic_mac(seed: str) -> str:
    digest = hashlib.sha256(seed.encode("utf-8")).digest()
    octets = [0x52, 0x54, 0x00, digest[0], digest[1], digest[2]]
    octets[3] = octets[3] | 0x02  # ensure locally administered bit
    octets[3] = octets[3] & 0xFE  # clear multicast bit
    return ":".join(f"{octet:02x}" for octet in octets)


def render_network_xml(
    config: NetworkConfig,
    ssh_port: int,
    mac_address: Optional[str] = None,
    boot_order: Optional[int] = None,
    rom_file: Optional[str] = None,
) -> Tuple[str, str]:
    """Render a libvirt interface definition based on the requested network mode."""
    mac = (mac_address or config.mac_address or random_mac()).lower()
    model = config.model

    if config.mode == "user":
        body = ["<interface type='user'>"]
        if boot_order is not None:
            body.append(f"  <boot order='{boot_order}'/>")
        body.append(f"  <mac address='{mac}'/>")
        body.append(f"  <model type='{model}'/>")
        if rom_file:
            body.append(f"  <rom file='{rom_file}'/>")
        body.extend(
            [
                "  <protocol type='tcp'>",
                f"    <source mode='bind' address='0.0.0.0' service='{ssh_port}'/>",
                "    <destination mode='connect' service='22'/>",
                "  </protocol>",
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
    log("INFO", f"Running: {' '.join(cmd)}")
    result = subprocess.run(cmd, check=check, text=True, **kwargs)
    return result


@dataclass
class NetworkConfig:
    mode: str
    bridge_name: Optional[str] = None
    direct_device: Optional[str] = None
    mac_address: Optional[str] = None
    model: str = "virtio"


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
    novnc_port: int
    base_image_path: Optional[str]
    blank_work_disk: bool
    boot_iso_path: Optional[str]
    boot_order: List[str]
    cloud_init_enabled: bool
    password: str
    ssh_port: int
    vm_name: str
    persist: bool
    ssh_pubkey: Optional[str]
    redfish_user: str
    redfish_password: str
    redfish_port: int
    redfish_system_id: str
    redfish_enabled: bool
    network: NetworkConfig
    ipxe_enabled: bool
    ipxe_rom_path: Optional[str]


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

    def start(self) -> None:
        self._start_libvirt()
        self._wait_for_libvirt()
        if self.vm_config.redfish_enabled:
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
            raise ManagerError("libvirt socket did not appear; check container privileges")
        if not any(wait_for_path(path, timeout=15) for path in virtlogd_paths):
            raise ManagerError("virtlogd socket did not appear; check container privileges")

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
            f"noVNC web client available at https://<host>:{self.vm_config.novnc_port}/vnc.html",
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
        self.seed_iso = self.vm_dir / "seed.iso" if self.cfg.cloud_init_enabled else None
        self._disk_reused = False
        self._network_mac: Optional[str] = self.cfg.network.mac_address
        self._ipxe_rom_path: Optional[Path] = None
        if self.cfg.ipxe_enabled:
            if not self.cfg.ipxe_rom_path:
                raise ManagerError("IPXE_ENABLE=1 requires an iPXE ROM path.")
            rom_candidate = Path(self.cfg.ipxe_rom_path)
            if not rom_candidate.exists():
                raise ManagerError(f"iPXE ROM not found at {rom_candidate}")
            self._ipxe_rom_path = rom_candidate

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
            log(
                "WARN",
                "KVM device not available; falling back to software emulation (TCG). Performance will be degraded.",
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
            log("WARN", f"Cached image too small; removing {self.base_image}")
            self.base_image.unlink()

        log("INFO", f"Downloading image: {self.cfg.image_url}")
        with tempfile.NamedTemporaryFile(delete=False, dir=BASE_IMAGES_DIR) as tmp:
            tmp_path = Path(tmp.name)
        try:
            run(
                [
                    "wget",
                    "--progress=bar:force:noscroll",
                    "-O",
                    str(tmp_path),
                    self.cfg.image_url,
                ],
                check=True,
            )
            tmp_path.replace(self.base_image)
        except subprocess.CalledProcessError as exc:
            if tmp_path.exists():
                tmp_path.unlink()
            raise ManagerError(f"Failed to download {self.cfg.image_url}: {exc}") from exc

    def _prepare_work_image(self) -> None:
        self._disk_reused = False
        if self.cfg.persist and self.work_image.exists():
            size = self.work_image.stat().st_size
            if size > 100 * 1024 * 1024:
                log("INFO", f"Reusing persistent disk {self.work_image}")
                self._disk_reused = True
            else:
                log("WARN", f"Existing disk too small; recreating {self.work_image}")
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
                        "Base image points to an ISO. Use BOOT_ISO for installation media and keep BASE_IMAGE for disk images."
                    )
                shutil.copy2(self.base_image, self.work_image)
                if self.cfg.disk_size and self.cfg.disk_size != "0":
                    log("INFO", f"Resizing disk to {self.cfg.disk_size}...")
                    run(["qemu-img", "resize", str(self.work_image), self.cfg.disk_size])
        else:
            log("INFO", f"Persistent disk retained at {self.work_image}")

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
            user_data = textwrap.dedent(
                f"""
                #cloud-config
                users:
                  - name: {self.cfg.login_user}
                    lock_passwd: false
                    sudo: ALL=(ALL) NOPASSWD:ALL
                    shell: /bin/bash
                    passwd: '{passwd_hash}'
                chpasswd:
                  expire: False
                ssh_pwauth: true
                """
            ).strip() + "\n"
            if self.cfg.ssh_pubkey:
                user_data += "ssh_authorized_keys:\n"
                user_data += f"  - {self.cfg.ssh_pubkey}\n"
            (tmp / "user-data").write_text(user_data)
            meta_data = textwrap.dedent(
                f"""
                instance-id: iid-{self.cfg.vm_name}
                local-hostname: {self.cfg.vm_name}
                """
            ).strip() + "\n"
            (tmp / "meta-data").write_text(meta_data)

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
        rom_file = str(self._ipxe_rom_path) if self._ipxe_rom_path else None
        network_xml, resolved_mac = render_network_xml(
            self.cfg.network,
            self.cfg.ssh_port,
            mac_address=self._network_mac,
            boot_order=network_order,
            rom_file=rom_file,
        )
        self._network_mac = resolved_mac

        display_xml = ""
        graphics = self.cfg.graphics_type
        if graphics and graphics != "none":
            if graphics == "vnc":
                display_xml = (
                    f"<graphics type='{graphics}' listen='0.0.0.0' port='{self.cfg.vnc_port}' autoport='no'/>"
                )
            else:
                display_xml = (
                    f"<graphics type='{graphics}' listen='0.0.0.0' autoport='yes'/>"
                )

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
          {cpu_xml}
          <devices>
            <disk type='file' device='disk'>
              <driver name='qemu' type='{self.cfg.image_format}' cache='none'/>
              <source file='{self.work_image}'/>
              <target dev='vda' bus='virtio'/>{disk_boot_attr}
            </disk>
            {seed_iso_xml}
            {boot_iso_xml}
            {network_xml}
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
                    log(
                        "ERROR",
                        "libvirt could not access host cgroups. Run the container with --cgroupns=host.",
                    )
                raise
            log("SUCCESS", f"Domain {self.cfg.vm_name} started")
        if self.cfg.novnc_enabled:
            self.service_manager.start_novnc()

    def wait_until_stopped(self) -> None:
        if self.domain is None:
            raise ManagerError("Domain not defined")
        log("INFO", f"Waiting for domain {self.cfg.vm_name} to stop (Ctrl+C to terminate)")
        try:
            while True:
                try:
                    active = self.domain.isActive()  # type: ignore[attr-defined]
                except libvirt.libvirtError as exc:
                    raise ManagerError(f"Failed to query domain state: {exc}") from exc
                if not active:
                    log("INFO", f"Domain {self.cfg.vm_name} is no longer active")
                    return
                time.sleep(1)
        except KeyboardInterrupt:
            log("INFO", "Interrupt received, attempting graceful shutdown")
            try:
                self.domain.shutdown()  # type: ignore[attr-defined]
            except libvirt.libvirtError:
                log("WARN", "Graceful shutdown failed; forcing destroy")
                try:
                    self.domain.destroy()  # type: ignore[attr-defined]
                except libvirt.libvirtError:
                    log("WARN", "Failed to force destroy domain")
    def cleanup(self) -> None:
        if self.domain is not None:
            try:
                if self.domain.isActive():  # type: ignore[attr-defined]
                    log("INFO", f"Shutting down domain {self.cfg.vm_name}")
                    self.domain.destroy()  # type: ignore[attr-defined]
            except libvirt.libvirtError:
                log("WARN", f"Failed to destroy domain {self.cfg.vm_name}")
            try:
                log("INFO", f"Undefining domain {self.cfg.vm_name}")
                self.domain.undefine()
            except libvirt.libvirtError:
                log("WARN", f"Failed to undefine domain {self.cfg.vm_name}")
        if not self.cfg.persist and self.vm_dir.exists():
            try:
                shutil.rmtree(self.vm_dir)
            except OSError:
                log("WARN", f"Failed to remove {self.vm_dir}")


def load_distro_config(distro: str, config_path: Path = DEFAULT_CONFIG_PATH) -> Dict[str, str]:
    if yaml is None:
        raise ManagerError("PyYAML is required to load distribution configuration")
    if not config_path.exists():
        raise ManagerError(f"Distribution config missing: {config_path}")
    data = yaml.safe_load(config_path.read_text())
    distros = data.get("distributions", {})
    if distro not in distros:
        available = ", ".join(sorted(distros.keys()))
        raise ManagerError(f"Unknown distro '{distro}'. Available: {available}")
    return distros[distro]


def parse_env() -> VMConfig:
    distro = get_env("DISTRO", "ubuntu-2404")
    distro_info = load_distro_config(distro)

    memory_mb = int(get_env("MEMORY", "4096"))
    cpus = int(get_env("CPUS", "2"))
    disk_size = get_env("DISK_SIZE", "20G")

    graphics_raw = get_env("GRAPHICS", None)
    if graphics_raw is None:
        display = "none"
    else:
        display = graphics_raw.strip().lower() or "none"
    graphics_type = display
    novnc_enabled = False
    if display in ("", "none"):
        graphics_type = "none"
    elif display == "novnc":
        graphics_type = "vnc"
        novnc_enabled = True
    elif display == "vnc":
        graphics_type = "vnc"
    else:
        graphics_type = display

    vnc_port = int(get_env("VNC_PORT", "5900"))
    novnc_port = int(get_env("NOVNC_PORT", "6080"))
    if novnc_enabled and graphics_type != "vnc":
        raise ManagerError("noVNC requires a VNC graphics backend")

    base_image_override = get_env("BASE_IMAGE")
    if base_image_override is not None:
        base_image_override = base_image_override.strip()
        if not base_image_override:
            base_image_override = None

    blank_disk_raw = get_env("BLANK_DISK")
    blank_work_disk = get_env_bool("BLANK_DISK", False)
    if base_image_override and base_image_override.lower() == "blank":
        blank_work_disk = True
        base_image_override = None

    boot_iso = get_env("BOOT_ISO")
    if boot_iso is not None:
        boot_iso = boot_iso.strip() or None

    boot_order_raw = get_env("BOOT_ORDER", "hd")
    boot_order_input = [item.strip().lower() for item in boot_order_raw.split(",") if item.strip()]
    boot_aliases = {
        "hd": "hd",
        "disk": "hd",
        "harddisk": "hd",
        "cd": "cdrom",
        "cdrom": "cdrom",
        "dvd": "cdrom",
        "network": "network",
        "net": "network",
        "pxe": "network",
    }
    boot_order = [boot_aliases.get(dev, dev) for dev in boot_order_input]
    if not boot_order:
        boot_order = ["hd"]
    if boot_iso and "cdrom" not in boot_order:
        boot_order = ["cdrom"] + boot_order
    if boot_iso and base_image_override is None and blank_disk_raw is None:
        # Installing from ISO without an explicit base image -> default to a blank disk.
        blank_work_disk = True
    if blank_work_disk and not boot_iso and boot_order == ["hd"]:
        boot_order = ["hd"]

    cloud_init_enabled = get_env_bool("CLOUD_INIT", True)
    ipxe_enabled = get_env_bool("IPXE_ENABLE", False)
    ipxe_rom_raw = get_env("IPXE_ROM_PATH")
    ipxe_rom_override = ipxe_rom_raw.strip() if ipxe_rom_raw else None

    distro_arch_raw = distro_info.get("arch")
    arch_env = get_env("ARCH")
    if arch_env is not None:
        arch_candidate = arch_env.strip()
    elif distro_arch_raw:
        arch_candidate = str(distro_arch_raw).strip()
    else:
        arch_candidate = "x86_64"
    if not arch_candidate:
        arch_candidate = "x86_64"

    arch_key = ARCH_ALIASES.get(arch_candidate.lower())
    if arch_key is None:
        supported = ", ".join(sorted(SUPPORTED_ARCHES.keys()))
        raise ManagerError(f"Unsupported ARCH '{arch_candidate}'. Supported: {supported}")
    if arch_key not in SUPPORTED_ARCHES:
        supported = ", ".join(sorted(SUPPORTED_ARCHES.keys()))
        raise ManagerError(f"Unsupported ARCH '{arch_candidate}'. Supported: {supported}")

    if distro_arch_raw:
        distro_arch_key = ARCH_ALIASES.get(str(distro_arch_raw).strip().lower())
        if distro_arch_key is None or distro_arch_key not in SUPPORTED_ARCHES:
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
    ssh_port = int(get_env("SSH_PORT", "2222"))

    network_mode_raw = get_env("NETWORK_MODE", "nat")
    network_mode_normalized = (network_mode_raw or "nat").strip().lower()
    network_mode_aliases = {
        "nat": "user",
        "user": "user",
        "usernat": "user",
        "bridge": "bridge",
        "bridged": "bridge",
        "host-bridge": "bridge",
        "direct": "direct",
        "macvtap": "direct",
    }
    network_mode = network_mode_aliases.get(network_mode_normalized)
    if network_mode is None:
        raise ManagerError(
            f"Unsupported NETWORK_MODE '{network_mode_raw}'. Expected one of nat, bridge, direct."
        )

    network_model_raw = get_env("NETWORK_MODEL", "virtio")
    if network_model_raw is None:
        network_model_raw = "virtio"
    network_model_candidate = network_model_raw.strip().lower()
    if not network_model_candidate:
        network_model_candidate = "virtio"
    network_model = NETWORK_MODEL_ALIASES.get(network_model_candidate)
    if network_model is None:
        supported_models = ", ".join(sorted(SUPPORTED_NETWORK_MODELS))
        raise ManagerError(
            f"Unsupported NETWORK_MODEL '{network_model_raw}'. Supported models: {supported_models}"
        )

    bridge_name = None
    direct_device = None
    if network_mode == "bridge":
        bridge_name = get_env("NETWORK_BRIDGE")
        if not bridge_name:
            raise ManagerError("NETWORK_BRIDGE is required when NETWORK_MODE=bridge")
        bridge_name = bridge_name.strip()
    elif network_mode == "direct":
        direct_device = get_env("NETWORK_DIRECT_DEV")
        if not direct_device:
            raise ManagerError("NETWORK_DIRECT_DEV is required when NETWORK_MODE=direct")
        direct_device = direct_device.strip()

    vm_name = derive_vm_name(distro)

    mac_raw = get_env("NETWORK_MAC")
    mac_address = mac_raw.strip().lower() if mac_raw else None
    if mac_address and not MAC_ADDRESS_RE.match(mac_address):
        raise ManagerError(f"Invalid NETWORK_MAC '{mac_raw}'. Use format aa:bb:cc:dd:ee:ff")
    if not mac_address:
        mac_address = deterministic_mac(vm_name)

    network_cfg = NetworkConfig(
        mode=network_mode,
        bridge_name=bridge_name,
        direct_device=direct_device,
        mac_address=mac_address,
        model=network_model,
    )
    ipxe_rom_path: Optional[str] = None
    if ipxe_enabled:
        if "network" in boot_order:
            boot_order = ["network"] + [dev for dev in boot_order if dev != "network"]
        else:
            boot_order = ["network"] + boot_order
        default_roms = IPXE_DEFAULT_ROMS.get(arch, {})
        if ipxe_rom_override:
            ipxe_rom_path = ipxe_rom_override
        else:
            default_rom = default_roms.get(network_model)
            if default_rom:
                ipxe_rom_path = str(default_rom)
        if not ipxe_rom_path:
            raise ManagerError(
                "IPXE_ENABLE=1 requires IPXE_ROM_PATH when a default ROM is not available for "
                f"ARCH='{arch}' with NETWORK_MODEL='{network_model}'."
            )
        rom_candidate = Path(ipxe_rom_path)
        if not rom_candidate.exists():
            raise ManagerError(
                f"iPXE ROM not found at {rom_candidate}. Install ipxe-qemu inside the image or override IPXE_ROM_PATH."
            )
        if network_mode == "user":
            log(
                "WARN",
                "IPXE_ENABLE=1 with NETWORK_MODE=nat relies on the built-in user-mode DHCP/TFTP. "
                "For real PXE environments prefer bridge or direct networking.",
            )

    persist = get_env_bool("PERSIST", False)
    ssh_pubkey = get_env("SSH_PUBKEY")
    redfish_user = get_env("REDFISH_USERNAME", "admin")
    redfish_password = get_env("REDFISH_PASSWORD", "password")
    redfish_port = int(get_env("REDFISH_PORT", "8443"))
    redfish_system_id = get_env("REDFISH_SYSTEM_ID", vm_name)
    redfish_enabled = get_env_bool("REDFISH_ENABLE", False)

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
        novnc_port=novnc_port,
        base_image_path=base_image_override,
        blank_work_disk=blank_work_disk,
        boot_iso_path=boot_iso,
        boot_order=boot_order,
        cloud_init_enabled=cloud_init_enabled,
        password=guest_password,
        ssh_port=ssh_port,
        vm_name=vm_name,
        persist=persist,
        ssh_pubkey=ssh_pubkey,
        redfish_user=redfish_user,
        redfish_password=redfish_password,
        redfish_port=redfish_port,
        redfish_system_id=redfish_system_id,
        redfish_enabled=redfish_enabled,
        network=network_cfg,
        ipxe_enabled=ipxe_enabled,
        ipxe_rom_path=ipxe_rom_path,
    )


def run_console(vm_name: str) -> int:
    """Attach to the guest console via virsh."""
    cmd = ["virsh", "-c", LIBVIRT_URI, "console", vm_name]
    log("INFO", "Attaching to VM console (Ctrl+] to exit)")
    proc = subprocess.Popen(cmd)
    try:
        return proc.wait()
    except KeyboardInterrupt:
        proc.send_signal(signal.SIGINT)
        return proc.wait()


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description="Docker-VM-Runner libvirt manager")
    parser.add_argument("--no-console", action="store_true", help="Do not attach to console")
    args = parser.parse_args(argv)

    console_requested = not args.no_console
    if console_requested and not has_controlling_tty():
        log(
            "WARN",
            "No controlling TTY detected; skipping interactive console. "
            "Set NO_CONSOLE=1 to hide this warning.",
        )
        console_requested = False

    cfg = parse_env()
    log("INFO", f"Distribution: {cfg.distro} ({cfg.distro_name})")
    log("INFO", f"VM Name: {cfg.vm_name}")
    log("INFO", f"Memory: {cfg.memory_mb} MiB, CPUs: {cfg.cpus}")
    if cfg.network.mode == "user":
        log("INFO", f"SSH forward: localhost:{cfg.ssh_port} -> guest:22")
    elif cfg.network.mode == "bridge":
        bridge = cfg.network.bridge_name or "<unspecified>"
        log("INFO", f"Bridge networking via {bridge} (guest obtains IP from upstream)")
    elif cfg.network.mode == "direct":
        dev = cfg.network.direct_device or "<unspecified>"
        log("INFO", f"Direct/macvtap networking via host device {dev}")
    log("INFO", f"NIC model: {cfg.network.model}")
    if cfg.ipxe_enabled:
        rom_display = cfg.ipxe_rom_path or "<unspecified>"
        log("INFO", f"iPXE enabled (ROM: {rom_display})")
    log("INFO", f"Display mode: {cfg.display}")
    if cfg.graphics_type != "none":
        log("INFO", f"Graphics backend: {cfg.graphics_type}")
    if cfg.novnc_enabled:
        log("INFO", f"noVNC web: https://<host>:{cfg.novnc_port}/vnc.html")
    elif cfg.graphics_type == "vnc":
        log("INFO", f"VNC server: <host>:{cfg.vnc_port}")
    if cfg.redfish_enabled:
        log("INFO", f"Redfish: https://<host>:{cfg.redfish_port}/")
    else:
        log("INFO", "Redfish disabled (set REDFISH_ENABLE=1 to enable)")

    ensure_directory(STATE_DIR)

    service_manager = ServiceManager(cfg)
    service_manager.start()

    vm_mgr = VMManager(cfg, service_manager)
    vm_mgr.connect()
    try:
        vm_mgr.prepare()
        vm_mgr.start()
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
    finally:
        vm_mgr.cleanup()
        vm_mgr.close()
        service_manager.stop()


if __name__ == "__main__":
    sys.exit(main())
