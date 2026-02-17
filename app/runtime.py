"""Container runtime detection for Docker-VM-Runner."""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

from app.utils import log


@dataclass
class RuntimeInfo:
    engine: str  # "docker", "podman", "kubernetes", "unknown"
    rootless: bool
    privileged: bool


def _detect_engine() -> str:
    """Detect which container runtime is in use."""
    if Path("/var/run/secrets/kubernetes.io").exists():
        return "kubernetes"
    if Path("/run/.containerenv").exists():
        return "podman"
    if Path("/.dockerenv").exists():
        return "docker"
    return "unknown"


def _is_rootless() -> bool:
    """Check if running in a rootless container (UID mapping active)."""
    try:
        with open("/proc/self/uid_map") as f:
            for line in f:
                parts = line.split()
                if len(parts) >= 3:
                    # In rootless, UID 0 inside maps to a non-zero UID outside
                    if parts[0] == "0" and parts[1] != "0":
                        return True
        return False
    except OSError:
        return False


def _is_privileged() -> bool:
    """Check if the container has full capabilities (privileged mode)."""
    try:
        with open("/proc/self/status") as f:
            for line in f:
                if line.startswith("CapBnd:"):
                    # Full capability bitmask (privileged) is all bits set
                    cap_hex = line.split(":", 1)[1].strip()
                    cap_val = int(cap_hex, 16)
                    # 0x3fffffffff (38 bits) or higher indicates privileged
                    return cap_val >= 0x3FFFFFFFFF
        return False
    except (OSError, ValueError):
        return False


def detect_runtime() -> RuntimeInfo:
    """Detect container runtime, rootless status, and privilege level."""
    engine = _detect_engine()
    rootless = _is_rootless()
    privileged = _is_privileged()

    if rootless:
        log("WARN", f"Rootless {engine} detected — some operations may require workarounds")
    if not privileged:
        log("DEBUG", "Container is running without full privileges")

    return RuntimeInfo(engine=engine, rootless=rootless, privileged=privileged)
