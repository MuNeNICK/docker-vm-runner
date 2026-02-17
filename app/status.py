"""Boot status broadcasting for Docker-VM-Runner."""

from __future__ import annotations

from pathlib import Path

from app.utils import log

# Status file served by websockify's --web directory
STATUS_FILE = Path("/usr/share/novnc/status.txt")


class StatusBroadcaster:
    """Write boot progress to a file served over HTTP by websockify."""

    def __init__(self) -> None:
        self._ready = False

    def update(self, msg: str) -> None:
        """Append a status message to the status file."""
        if self._ready:
            return
        try:
            STATUS_FILE.parent.mkdir(parents=True, exist_ok=True)
            with open(STATUS_FILE, "a") as f:
                f.write(msg + "\n")
                f.flush()
        except OSError:
            pass
        log("DEBUG", f"Status: {msg}")

    def ready(self) -> None:
        """Signal that the VM is ready — remove status file so polling detects completion."""
        self._ready = True
        try:
            STATUS_FILE.unlink(missing_ok=True)
        except OSError:
            pass
        log("DEBUG", "Status: VM ready")
