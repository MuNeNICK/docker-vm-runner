#!/usr/bin/env python3
"""
Docker-QEMU manager rewritten around libvirt + sushy.
Maintains the existing UX (single `docker run` attaches directly to the guest
console) while provisioning the VM via libvirt and exposing Redfish control
through sushy-emulator.
"""

from __future__ import annotations

import argparse
import crypt
import bcrypt
import os
import random
import shutil
import signal
import string
import subprocess
import sys
import tempfile
import textwrap
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, List, Optional

import yaml

try:
    import libvirt  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit(f"libvirt python bindings not available: {exc}")


DEFAULT_CONFIG_PATH = Path("/config/distros.yaml")
IMAGES_DIR = Path("/images")
BASE_IMAGES_DIR = IMAGES_DIR / "base"
VM_IMAGES_DIR = IMAGES_DIR / "vms"
STATE_DIR = Path("/var/lib/docker-qemu")
LIBVIRT_URI = os.environ.get("LIBVIRT_URI", "qemu:///system")


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
    """Generate a salted SHA512 crypt hash for cloud-init."""
    salt_charset = string.ascii_letters + string.digits
    salt = "".join(random.choice(salt_charset) for _ in range(16))
    return crypt.crypt(password, f"$6${salt}")


def run(cmd: List[str], check: bool = True, **kwargs) -> subprocess.CompletedProcess:
    """Run command with logging."""
    log("INFO", f"Running: {' '.join(cmd)}")
    result = subprocess.run(cmd, check=check, text=True, **kwargs)
    return result


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
    password: str
    ssh_port: int
    vm_name: str
    persist: bool
    ssh_pubkey: Optional[str]
    redfish_user: str
    redfish_password: str
    redfish_port: int
    redfish_system_id: str


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
        self._start_sushy()

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
            if sock.exists():
                sock.unlink()

        virtlogd = subprocess.Popen(
            ["/usr/sbin/virtlogd", "-f", "/etc/libvirt/virtlogd.conf"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.processes.append(virtlogd)

        libvirtd = subprocess.Popen(
            ["/usr/sbin/libvirtd", "-f", "/etc/libvirt/libvirtd.conf"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.processes.append(libvirtd)
        log("INFO", "libvirt services spawned")
        self._assert_running(virtlogd, "virtlogd")
        self._assert_running(libvirtd, "libvirtd")

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
                "/CN=docker-qemu/O=docker-qemu",
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
            redirect_marker = "<!-- docker-qemu novnc redirect -->"
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
    <meta http-equiv="refresh" content="0;url=vnc.html?autoconnect=1&resize=scale" />
    <title>noVNC</title>
  </head>
  <body>
    <p>Redirecting to <a href="vnc.html?autoconnect=1&resize=scale">noVNC console</a>…</p>
    <script>window.location.replace("vnc.html?autoconnect=1&resize=scale");</script>
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
        self.base_image = BASE_IMAGES_DIR / f"{self.cfg.distro}.{self.cfg.image_format}"
        self.work_image = self.vm_dir / f"disk.{self.cfg.image_format}"
        self.seed_iso = self.vm_dir / "seed.iso"
        self._disk_reused = False

    def connect(self) -> None:
        self.conn = libvirt.open(LIBVIRT_URI)
        if self.conn is None:
            raise ManagerError(f"Failed to open libvirt connection to {LIBVIRT_URI}")

    def close(self) -> None:
        if self.conn is not None:
            self.conn.close()
            self.conn = None

    def prepare(self) -> None:
        self._ensure_base_image()
        self._prepare_work_image()
        self._generate_cloud_init()
        self._define_domain()

    def _ensure_base_image(self) -> None:
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
            log("INFO", f"Creating working disk {self.work_image}")
            shutil.copy2(self.base_image, self.work_image)
            if self.cfg.disk_size and self.cfg.disk_size != "0":
                log("INFO", f"Resizing disk to {self.cfg.disk_size}...")
                run(["qemu-img", "resize", str(self.work_image), self.cfg.disk_size])
        else:
            log("INFO", f"Persistent disk retained at {self.work_image}")

    def _generate_cloud_init(self) -> None:
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
        host_cpu = self.cfg.cpu_model.lower() in ("host", "host-passthrough")

        if host_cpu:
            cpu_xml = "<cpu mode='host-passthrough'/>"
        else:
            cpu_xml = textwrap.dedent(
                f"""
                <cpu mode='custom' match='exact'>
                  <model fallback='allow'>{self.cfg.cpu_model}</model>
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

        interface_mac = random_mac()
        ssh_port = self.cfg.ssh_port
        network_xml = textwrap.dedent(
            f"""
            <interface type='user'>
              <mac address='{interface_mac}'/>
              <model type='virtio'/>
              <protocol type='tcp'>
                <source mode='bind' address='0.0.0.0' service='{ssh_port}'/>
                <destination mode='connect' service='22'/>
              </protocol>
            </interface>
            """
        ).strip()

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

        memory_unit = "MiB"
        xml = f"""
        <domain type='kvm' {qemu_ns}>
          <name>{self.cfg.vm_name}</name>
          <memory unit='{memory_unit}'>{self.cfg.memory_mb}</memory>
          <vcpu placement='static'>{self.cfg.cpus}</vcpu>
          <os>
            <type arch='{self.cfg.arch}' machine='pc'>hvm</type>
            <boot dev='hd'/>
          </os>
          <features>
            <acpi/>
            <apic/>
            <pae/>
          </features>
          {cpu_xml}
          <devices>
            <disk type='file' device='disk'>
              <driver name='qemu' type='{self.cfg.image_format}' cache='none'/>
              <source file='{self.work_image}'/>
              <target dev='vda' bus='virtio'/>
            </disk>
            <disk type='file' device='cdrom'>
              <driver name='qemu' type='raw'/>
              <source file='{self.seed_iso}'/>
              <target dev='sda' bus='sata'/>
              <readonly/>
            </disk>
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
    if not config_path.exists():
        raise ManagerError(f"Distribution config missing: {config_path}")
    data = yaml.safe_load(config_path.read_text())
    distros = data.get("distributions", {})
    if distro not in distros:
        available = ", ".join(sorted(distros.keys()))
        raise ManagerError(f"Unknown distro '{distro}'. Available: {available}")
    return distros[distro]


def parse_env() -> VMConfig:
    distro = os.environ.get("DISTRO", "ubuntu-2404")
    distro_info = load_distro_config(distro)
    memory_mb = int(os.environ.get("VM_MEMORY", "4096"))
    cpus = int(os.environ.get("VM_CPUS", "2"))
    disk_size = os.environ.get("VM_DISK_SIZE", "20G")
    display_raw = os.environ.get("VM_DISPLAY", "none")
    display = display_raw.strip().lower()
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

    vnc_port = int(os.environ.get("VM_VNC_PORT", "5900"))
    novnc_port = int(os.environ.get("VM_NOVNC_PORT", "6080"))
    if novnc_enabled and graphics_type != "vnc":
        raise ManagerError("noVNC requires a VNC graphics backend")
    arch = os.environ.get("VM_ARCH", "x86_64")
    cpu_model = os.environ.get("VM_CPU_MODEL", "host")
    extra_args = os.environ.get("EXTRA_ARGS", "")
    password = os.environ.get("VM_PASSWORD", "password")
    ssh_port = int(os.environ.get("VM_SSH_PORT", "2222"))
    hostname = os.environ.get("HOSTNAME", distro)
    vm_name = os.environ.get("VM_NAME", hostname)
    persist = os.environ.get("VM_PERSIST", "0") in ("1", "true", "yes", "on")
    ssh_pubkey = os.environ.get("VM_SSH_PUBKEY")
    redfish_user = os.environ.get("REDFISH_USERNAME", "admin")
    redfish_password = os.environ.get("REDFISH_PASSWORD", "password")
    redfish_port = int(os.environ.get("REDFISH_PORT", "8443"))
    redfish_system_id = os.environ.get("REDFISH_SYSTEM_ID", vm_name)

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
        password=password,
        ssh_port=ssh_port,
        vm_name=vm_name,
        persist=persist,
        ssh_pubkey=ssh_pubkey,
        redfish_user=redfish_user,
        redfish_password=redfish_password,
        redfish_port=redfish_port,
        redfish_system_id=redfish_system_id,
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
    parser = argparse.ArgumentParser(description="Docker-QEMU libvirt manager")
    parser.add_argument("--no-console", action="store_true", help="Do not attach to console")
    args = parser.parse_args(argv)

    cfg = parse_env()
    log("INFO", f"Distribution: {cfg.distro} ({cfg.distro_name})")
    log("INFO", f"VM Name: {cfg.vm_name}")
    log("INFO", f"Memory: {cfg.memory_mb} MiB, CPUs: {cfg.cpus}")
    log("INFO", f"SSH forward: localhost:{cfg.ssh_port} -> guest:22")
    log("INFO", f"Display mode: {cfg.display}")
    if cfg.graphics_type != "none":
        log("INFO", f"Graphics backend: {cfg.graphics_type}")
    if cfg.novnc_enabled:
        log("INFO", f"noVNC web: https://<host>:{cfg.novnc_port}/vnc.html")
    elif cfg.graphics_type == "vnc":
        log("INFO", f"VNC server: <host>:{cfg.vnc_port}")

    ensure_directory(STATE_DIR)

    service_manager = ServiceManager(cfg)
    service_manager.start()

    vm_mgr = VMManager(cfg, service_manager)
    vm_mgr.connect()
    try:
        vm_mgr.prepare()
        vm_mgr.start()
        retcode = 0
        if args.no_console:
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
