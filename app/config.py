"""Configuration loading and environment variable parsing for Docker-VM-Runner."""

from __future__ import annotations

import os
from pathlib import Path
from typing import Dict, List, Optional

try:
    import yaml  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit("PyYAML is required but not installed") from exc

from app.constants import (
    _DATA_DIR,
    ARCH_ALIASES,
    DEFAULT_CONFIG_PATH,
    IPXE_DEFAULT_ROMS,
    MAC_ADDRESS_RE,
    SUPPORTED_ARCHES,
    SUPPORTED_NETWORK_MODELS,
    TRUTHY,
)
from app.exceptions import ManagerError
from app.models import (
    FilesystemConfig,
    NicConfig,
    PortForward,
    VMConfig,
)
from app.utils import (
    derive_vm_name,
    deterministic_mac,
    ensure_directory,
    generate_password,
    get_env,
    get_env_bool,
    log,
    parse_int_env,
    validate_disk_size,
)


def load_distro_config(distro: str, config_path: Optional[Path] = None) -> Dict[str, str]:
    if config_path is None:
        config_path = DEFAULT_CONFIG_PATH
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
            raise ManagerError(f"CLOUD_INIT_USER_DATA must point to a regular file: {candidate}")
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
                "Expected: #cloud-config, #!/bin/bash, #cloud-boothook, #include, or #part-handler",
            )
        if first_line == "#cloud-config":
            try:
                content = cloud_init_user_data_path.read_text()
                parsed = yaml.safe_load(content)
                if not isinstance(parsed, dict):
                    log(
                        "WARN",
                        "CLOUD_INIT_USER_DATA: #cloud-config should contain a YAML mapping, got "
                        + type(parsed).__name__,
                    )
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
            raise ManagerError(f"Distribution '{distro}' declares unsupported arch '{distro_arch_raw}'.")
        if arch_env is not None and distro_arch_key != arch_key:
            raise ManagerError(
                f"ARCH='{arch_candidate}' does not match distribution '{distro}' arch '{distro_arch_raw}'."
            )
        arch = distro_arch_key
    else:
        arch = arch_key
    cpu_model = get_env("CPU_MODEL", "host")
    extra_args = get_env("EXTRA_ARGS", "")

    guest_password_env = get_env("GUEST_PASSWORD")
    if guest_password_env is not None:
        guest_password = guest_password_env
    else:
        guest_password = generate_password()
        log("INFO", f"No GUEST_PASSWORD set; generated random password: {guest_password}")
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
            raise ManagerError(f"Unsupported NETWORK{suffix}_MODE '{mode_raw}'. Expected one of nat, bridge, direct.")

        bridge_name = None
        direct_device = None
        if mode_key == "bridge":
            bridge_name = get_env_indexed("NETWORK_BRIDGE", index)
            if not bridge_name:
                suffix = "" if index == 1 else str(index)
                raise ManagerError(f"NETWORK{suffix}_BRIDGE is required when NETWORK{suffix}_MODE=bridge")
            bridge_name = bridge_name.strip()
        elif mode_key == "direct":
            direct_device = get_env_indexed("NETWORK_DIRECT_DEV", index)
            if not direct_device:
                suffix = "" if index == 1 else str(index)
                raise ManagerError(f"NETWORK{suffix}_DIRECT_DEV is required when NETWORK{suffix}_MODE=direct")
            direct_device = direct_device.strip()

        mac_raw = get_env_indexed("NETWORK_MAC", index)
        mac_address = mac_raw.strip().lower() if mac_raw else None
        if mac_address and not MAC_ADDRESS_RE.match(mac_address):
            suffix = "" if index == 1 else str(index)
            raise ManagerError(f"Invalid NETWORK{suffix}_MAC '{mac_raw}'. Use format aa:bb:cc:dd:ee:ff")
        if not mac_address:
            mac_address = deterministic_mac(f"{vm_name}:{index}")

        model_raw = get_env_indexed("NETWORK_MODEL", index)
        model = model_raw.strip().lower() if model_raw else "virtio"
        if model not in SUPPORTED_NETWORK_MODELS:
            supported_models = ", ".join(sorted(SUPPORTED_NETWORK_MODELS))
            suffix = "" if index == 1 else str(index)
            raise ManagerError(f"Unsupported NETWORK{suffix}_MODEL '{model_raw}'. Supported: {supported_models}")

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
        has_value = any(value is not None and value.strip() for value in trigger_values if isinstance(value, str))
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
            raise ManagerError(f"Unsupported FILESYSTEM{suffix}_DRIVER '{driver}'. Supported: virtiofs, 9p")

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
                f"iPXE ROM not found at {rom_candidate}. "
                "Override with IPXE_ROM_PATH or ensure QEMU packages include the ROMs."
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
    redfish_password_env = get_env("REDFISH_PASSWORD")
    if redfish_password_env is not None:
        redfish_password = redfish_password_env
    else:
        redfish_password = generate_password()
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
                raise ManagerError(f"Invalid PORT_FWD entry '{entry}': expected format host_port:guest_port")
            try:
                host_port = int(parts[0])
                guest_port = int(parts[1])
            except ValueError:
                raise ManagerError(f"Invalid PORT_FWD entry '{entry}': ports must be integers")
            if not (1 <= host_port <= 65535):
                raise ManagerError(f"Invalid PORT_FWD entry '{entry}': host port {host_port} out of range (1-65535)")
            if not (1 <= guest_port <= 65535):
                raise ManagerError(f"Invalid PORT_FWD entry '{entry}': guest port {guest_port} out of range (1-65535)")
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
                f"Port conflict: {label}={port} collides with {seen[port]}={port}. Each service needs a unique port."
            )
        seen[port] = label

    return VMConfig(
        distro=distro,
        image_url=distro_info["url"],
        login_user=distro_info["user"],
        image_format=distro_info.get("format", "qcow2"),
        distro_name="Custom ISO" if iso_requested else distro_info["name"],
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
