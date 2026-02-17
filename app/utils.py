"""Utility functions for Docker-VM-Runner."""

from __future__ import annotations

import hashlib
import os
import random
import re
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import List, Optional
from urllib.parse import urlparse
from urllib.request import urlopen, Request
from urllib.error import URLError, HTTPError

try:
    import bcrypt  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit("bcrypt is required but not installed") from exc

from app.constants import (
    _CONTAINER_ID_RE,
    _LOG_VERBOSE,
    DISK_SIZE_RE,
    TRUTHY,
)
from app.exceptions import ManagerError


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


def run(cmd: List[str], check: bool = True, **kwargs) -> subprocess.CompletedProcess:
    """Run command with logging."""
    log("DEBUG", f"Running: {' '.join(cmd)}")
    result = subprocess.run(cmd, check=check, text=True, **kwargs)
    return result
