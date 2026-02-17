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
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

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
        raise ManagerError(f"Invalid DISK_SIZE '{raw}'. Use a number with optional suffix: K, M, G, T (e.g. '20G')")
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
                        end="",
                        flush=True,
                    )
                else:
                    print(
                        f"\r  {downloaded_mb:.1f} MiB downloaded ({speed / (1024 * 1024):.1f} MiB/s)",
                        end="",
                        flush=True,
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


def download_file_with_retry(url: str, destination: Path, label: str = "Downloading", retries: int = 3) -> None:
    """Download a file with retry logic."""
    delays = [5, 10, 20]
    for attempt in range(1, retries + 1):
        try:
            download_file(url, destination, label=label)
            return
        except ManagerError:
            if attempt >= retries:
                raise
            delay = delays[min(attempt - 1, len(delays) - 1)]
            log("WARN", f"Download failed (attempt {attempt}/{retries}), retrying in {delay}s...")
            time.sleep(delay)


def get_host_info() -> dict:
    """Return host system information."""
    info: dict = {}
    # CPU model
    try:
        with open("/proc/cpuinfo") as f:
            for line in f:
                if line.startswith("model name"):
                    info["cpu_model"] = line.split(":", 1)[1].strip()
                    break
    except OSError:
        info["cpu_model"] = "unknown"

    info["cpu_count"] = os.cpu_count() or 1

    # Memory
    try:
        with open("/proc/meminfo") as f:
            for line in f:
                if line.startswith("MemTotal:"):
                    info["mem_total"] = int(line.split()[1]) * 1024  # KB to bytes
                elif line.startswith("MemAvailable:"):
                    info["mem_available"] = int(line.split()[1]) * 1024
    except OSError:
        info["mem_total"] = 0
        info["mem_available"] = 0

    # Kernel
    try:
        info["kernel"] = os.uname().release
    except AttributeError:
        info["kernel"] = "unknown"

    return info


def get_available_disk_space(path: Path) -> int:
    """Return available disk space in bytes at the given path."""
    try:
        stat = os.statvfs(str(path))
        return stat.f_bavail * stat.f_frsize
    except OSError:
        return 0


def get_available_memory() -> int:
    """Return available memory in bytes."""
    try:
        with open("/proc/meminfo") as f:
            for line in f:
                if line.startswith("MemAvailable:"):
                    return int(line.split()[1]) * 1024
    except OSError:
        pass
    return 0


def get_cpu_count() -> int:
    """Return number of host CPUs."""
    return os.cpu_count() or 1


def get_cpu_vendor() -> str:
    """Return CPU vendor: 'amd', 'intel', or 'unknown'."""
    try:
        with open("/proc/cpuinfo") as f:
            for line in f:
                if line.startswith("vendor_id"):
                    vendor = line.split(":", 1)[1].strip().lower()
                    if "amd" in vendor:
                        return "amd"
                    if "intel" in vendor:
                        return "intel"
                    return vendor
    except OSError:
        pass
    return "unknown"


def get_cpu_flags() -> set:
    """Return the set of CPU flags from /proc/cpuinfo."""
    try:
        with open("/proc/cpuinfo") as f:
            for line in f:
                if line.startswith("flags"):
                    return set(line.split(":", 1)[1].strip().split())
    except OSError:
        pass
    return set()


def detect_host_mtu() -> int:
    """Detect the MTU of the default network interface."""
    try:
        result = subprocess.run(
            ["ip", "-o", "route", "show", "default"],
            capture_output=True,
            text=True,
            check=True,
        )
        # Parse default route to find interface: "default via X.X.X.X dev ethN ..."
        tokens = result.stdout.split()
        for i, token in enumerate(tokens):
            if token == "dev" and i + 1 < len(tokens):
                iface = tokens[i + 1]
                mtu_path = Path(f"/sys/class/net/{iface}/mtu")
                if mtu_path.exists():
                    return int(mtu_path.read_text().strip())
    except (subprocess.CalledProcessError, FileNotFoundError, ValueError, OSError):
        pass
    return 1500


def has_ipv6() -> bool:
    """Check if IPv6 is available on the host."""
    return Path("/proc/net/if_inet6").exists()


def detect_filesystem(path: Path) -> str:
    """Detect filesystem type at the given path."""
    try:
        result = subprocess.run(
            ["stat", "-f", "-c", "%T", str(path)],
            capture_output=True,
            text=True,
            check=True,
        )
        return result.stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return "unknown"


def convert_disk_image(source: Path, dest: Path, target_format: str = "qcow2") -> None:
    """Convert VHD/VMDK/VDI/VHDX to qcow2 using qemu-img convert."""
    log("INFO", f"Converting {source.name} to {target_format}...")
    run(["qemu-img", "convert", "-p", "-O", target_format, str(source), str(dest)])
    log("SUCCESS", f"Converted to {dest}")


def extract_compressed(source: Path, dest_dir: Path) -> Path:
    """Extract .gz/.xz/.7z/.zip/.bz2 files. Returns path to extracted file."""
    suffix = source.suffix.lower()
    stem = source.stem

    if suffix == ".gz":
        import gzip

        out = dest_dir / stem
        with gzip.open(source, "rb") as f_in, open(out, "wb") as f_out:
            while True:
                chunk = f_in.read(1024 * 256)
                if not chunk:
                    break
                f_out.write(chunk)
        return out

    if suffix == ".xz":
        import lzma

        out = dest_dir / stem
        with lzma.open(source, "rb") as f_in, open(out, "wb") as f_out:
            while True:
                chunk = f_in.read(1024 * 256)
                if not chunk:
                    break
                f_out.write(chunk)
        return out

    if suffix == ".bz2":
        import bz2

        out = dest_dir / stem
        with bz2.open(source, "rb") as f_in, open(out, "wb") as f_out:
            while True:
                chunk = f_in.read(1024 * 256)
                if not chunk:
                    break
                f_out.write(chunk)
        return out

    if suffix == ".zip":
        import zipfile

        with zipfile.ZipFile(source, "r") as zf:
            names = zf.namelist()
            if not names:
                raise ManagerError(f"Empty zip archive: {source}")
            # Extract the largest file (likely the disk image)
            largest = max(names, key=lambda n: zf.getinfo(n).file_size)
            zf.extract(largest, dest_dir)
            return dest_dir / largest

    if suffix == ".7z":
        run(["7z", "x", "-y", f"-o{dest_dir}", str(source)])
        # Find extracted files
        extracted = [f for f in dest_dir.iterdir() if f != source and f.is_file()]
        if not extracted:
            raise ManagerError(f"No files extracted from {source}")
        return max(extracted, key=lambda f: f.stat().st_size)

    if suffix == ".rar":
        run(["7z", "x", "-y", f"-o{dest_dir}", str(source)])
        extracted = [f for f in dest_dir.iterdir() if f != source and f.is_file()]
        if not extracted:
            raise ManagerError(f"No files extracted from {source}")
        return max(extracted, key=lambda f: f.stat().st_size)

    raise ManagerError(f"Unsupported compressed format: {suffix}")


def check_filesystem_compatibility(path: Path) -> None:
    """Warn about BTRFS COW, OverlayFS, FUSE, ecryptfs issues."""
    fs_type = detect_filesystem(path)
    fs_lower = fs_type.lower()

    if "btrfs" in fs_lower:
        log("WARN", f"Storage is on BTRFS ({path}). Consider disabling COW with 'chattr +C' on the directory")
    elif "overlay" in fs_lower:
        log("WARN", f"Storage is on OverlayFS ({path}). Disk I/O performance may be reduced")
    elif "fuse" in fs_lower:
        log("WARN", f"Storage is on a FUSE filesystem ({path}). Performance may be reduced")
    elif "ecryptfs" in fs_lower:
        log("WARN", f"Storage is on ecryptfs ({path}). Encrypted filesystems may impact VM disk performance")


def check_disk_space(path: Path, required_bytes: int) -> None:
    """Warn if insufficient disk space."""
    available = get_available_disk_space(path)
    if available == 0:
        return
    if available < required_bytes:
        avail_gb = available / (1024**3)
        req_gb = required_bytes / (1024**3)
        log("ERROR", f"Insufficient disk space: {avail_gb:.1f}G available, {req_gb:.1f}G required at {path}")
    elif available < required_bytes * 2:
        avail_gb = available / (1024**3)
        req_gb = required_bytes / (1024**3)
        log("WARN", f"Low disk space: {avail_gb:.1f}G available for {req_gb:.1f}G disk at {path}")


def parse_resource_size(raw: str, resource_type: str) -> int:
    """Parse 'max', 'half', or integer for MEMORY/CPUS/DISK_SIZE.

    resource_type: 'memory' (returns MiB), 'cpus' (returns count), 'disk' (returns bytes)
    """
    value = raw.strip().lower()
    if value == "max":
        if resource_type == "memory":
            mem = get_available_memory()
            # Reserve 512 MiB for the host
            return max(512, (mem // (1024 * 1024)) - 512)
        elif resource_type == "cpus":
            return get_cpu_count()
        elif resource_type == "disk":
            return 0  # handled at call site with path info
    elif value == "half":
        if resource_type == "memory":
            mem = get_available_memory()
            return max(512, mem // (1024 * 1024) // 2)
        elif resource_type == "cpus":
            return max(1, get_cpu_count() // 2)
        elif resource_type == "disk":
            return 0  # handled at call site with path info
    # Fall through to integer parsing
    raise ManagerError(f"Invalid {resource_type} value '{raw}'. Use an integer, 'max', or 'half'")
