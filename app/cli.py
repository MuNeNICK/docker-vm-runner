"""CLI entry points for Docker-VM-Runner."""

from __future__ import annotations

import argparse
import signal
import subprocess
from pathlib import Path
from typing import List, Optional

try:
    import yaml  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit("PyYAML is required but not installed") from exc

from app.config import parse_env
from app.constants import (
    _SENSITIVE_FIELDS,
    ARCH_ALIASES,
    DEFAULT_CONFIG_PATH,
    IMAGES_DIR,
    LIBVIRT_URI,
    STATE_DIR,
)
from app.exceptions import ManagerError
from app.models import VMConfig
from app.services import ServiceManager
from app.utils import (
    ensure_directory,
    get_available_disk_space,
    get_env,
    get_env_bool,
    get_host_info,
    has_controlling_tty,
    kvm_available,
    log,
    sanitize_mount_target,
)
from app.vm import VMManager


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


def list_distros(config_path: Optional[Path] = None, arch_filter: Optional[str] = None) -> None:
    """Print available distributions and exit, optionally filtered by arch."""
    if config_path is None:
        config_path = DEFAULT_CONFIG_PATH
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
            k: v
            for k, v in distros.items()
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
    boot_info = f"  Arch: {cfg.arch} | Memory: {cfg.memory_mb} MiB | CPUs: {cfg.cpus}"
    if cfg.boot_mode != "legacy":
        boot_info += f" | Boot: {cfg.boot_mode.upper()}"
    if cfg.tpm_enabled:
        boot_info += " | TPM"
    lines.append(boot_info)

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
        lines.append("  Ensure docker ports are published:")
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
    parser.add_argument(
        "--list-distros",
        nargs="?",
        const="",
        default=None,
        metavar="ARCH",
        help="List available distributions and exit (optionally filter by arch: x86_64, aarch64, arm64, amd64)",
    )
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

    # Display host system info
    host_info = get_host_info()
    log("INFO", f"Host: {host_info.get('cpu_model', 'unknown')} ({host_info.get('cpu_count', '?')} cores)")
    mem_total_gb = host_info.get("mem_total", 0) / (1024**3)
    mem_avail_gb = host_info.get("mem_available", 0) / (1024**3)
    log("INFO", f"Memory: {mem_avail_gb:.1f}G available / {mem_total_gb:.1f}G total")
    disk_avail = get_available_disk_space(IMAGES_DIR if IMAGES_DIR.exists() else Path("/"))
    disk_avail_gb = disk_avail / (1024**3)
    log("INFO", f"Storage: {disk_avail_gb:.1f}G available at {IMAGES_DIR}")
    if kvm_available():
        log("INFO", f"KVM: available | Kernel: {host_info.get('kernel', 'unknown')}")
    else:
        log("WARN", f"KVM: NOT available (TCG mode) | Kernel: {host_info.get('kernel', 'unknown')}")

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
            mount = sanitize_mount_target(fs.target)
            log("INFO", f"Filesystem #{idx}: {fs.source} -> /mnt/{mount} ({fs.driver}, {mode})")
    if cfg.boot_mode != "legacy":
        log("INFO", f"Boot mode: {cfg.boot_mode}")
    if cfg.tpm_enabled:
        log("INFO", "TPM: enabled")
    if cfg.extra_disks:
        for ed in cfg.extra_disks:
            log("INFO", f"Extra disk {ed.index}: {ed.size}")
    if cfg.block_devices:
        for bd in cfg.block_devices:
            log("INFO", f"Block device {bd.index}: {bd.path}")
    if cfg.disk_controller != "virtio":
        log("INFO", f"Disk controller: {cfg.disk_controller}")
    if cfg.hyperv_enabled:
        log("INFO", "Hyper-V enlightenments: enabled")
    if cfg.gpu_passthrough != "off":
        log("INFO", f"GPU passthrough: {cfg.gpu_passthrough}")
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
