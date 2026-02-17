"""Network XML generation for Docker-VM-Runner."""

from __future__ import annotations

from typing import List, Optional, Tuple

from app.exceptions import ManagerError
from app.models import NicConfig, PortForward
from app.utils import random_mac


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
