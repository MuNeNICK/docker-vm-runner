"""Service management (libvirt, sushy, noVNC) for Docker-VM-Runner."""

from __future__ import annotations

import errno
import os
import shutil
import socket
import subprocess
import textwrap
import time
from pathlib import Path
from typing import List, Optional

_NOVNC_ROOT = Path("/usr/share/novnc")


try:
    import bcrypt  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit("bcrypt is required but not installed") from exc

try:
    import libvirt  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit(f"libvirt python bindings not available: {exc}")

from app.constants import LIBVIRT_URI, STATE_DIR
from app.exceptions import ManagerError
from app.models import VMConfig
from app.runtime import RuntimeInfo, detect_runtime
from app.utils import ensure_directory, log, run, wait_for_path


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
        self._storage_pool_path = Path(os.environ.get("REDFISH_STORAGE_PATH", "/var/lib/libvirt/images"))
        self.runtime: RuntimeInfo = detect_runtime()

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
            msg = (
                "libvirt socket did not appear.\n"
                "  Possible fixes:\n"
                "    - Run with --privileged\n"
                "    - Or add --cgroupns=host --device /dev/kvm:/dev/kvm\n"
                "    - Ensure the container has sufficient capabilities (SYS_ADMIN, NET_ADMIN)"
            )
            if self.runtime.rootless:
                log("WARN", msg)
                return
            raise ManagerError(msg)
        if not any(wait_for_path(path, timeout=15) for path in virtlogd_paths):
            msg = (
                "virtlogd socket did not appear.\n"
                "  Possible fixes:\n"
                "    - Run with --privileged\n"
                "    - Or add --cgroupns=host\n"
                "    - Check container logs for virtlogd errors"
            )
            if self.runtime.rootless:
                log("WARN", msg)
                return
            raise ManagerError(msg)

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
        hashed = bcrypt.hashpw(self.vm_config.redfish_password.encode("utf-8"), bcrypt.gensalt()).decode("utf-8")
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
                    f"Failed to open libvirt connection at {LIBVIRT_URI}; virtual media may be unavailable",
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
                        f"Created libvirt storage pool '{self._storage_pool_name}' ({self._storage_pool_path})",
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

        if not _NOVNC_ROOT.exists():
            raise ManagerError(f"noVNC static assets not found at {_NOVNC_ROOT}.")

        self._ensure_certificates()
        cert = self.cert_dir / "sushy.crt"
        key = self.cert_dir / "sushy.key"

        listen = f"0.0.0.0:{self.vm_config.novnc_port}"
        target = f"127.0.0.1:{self.vm_config.vnc_port}"
        cmd = [
            "websockify",
            "--web",
            str(_NOVNC_ROOT),
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
            f"noVNC console at https://localhost:{self.vm_config.novnc_port}/vnc.html?autoconnect=1&resize=scale",
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
