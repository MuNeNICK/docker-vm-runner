"""Network XML generation for Docker-VM-Runner."""

from __future__ import annotations

from typing import List, Optional, Tuple
from xml.etree.ElementTree import Element, SubElement, tostring

from app.exceptions import ManagerError
from app.models import NicConfig, PortForward
from app.utils import random_mac


def _element_to_str(root: Element) -> str:
    """Serialize an ElementTree element to a pretty-printed XML string without declaration."""
    from xml.dom.minidom import parseString

    raw = tostring(root, encoding="unicode")
    return parseString(raw).documentElement.toprettyxml(indent="  ").strip()


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
        iface = Element("interface", type="user")
        if boot_order is not None:
            SubElement(iface, "boot", order=str(boot_order))
        SubElement(iface, "mac", address=mac)
        SubElement(iface, "backend", type="passt")
        SubElement(iface, "ip", family="ipv4", address="10.0.2.15", prefix="24")
        SubElement(iface, "model", type=model)
        if rom_file:
            SubElement(iface, "rom", file=rom_file)
        if ssh_port is not None:
            pf_el = SubElement(iface, "portForward", proto="tcp")
            SubElement(pf_el, "range", start=str(ssh_port), to="22")
        for pf in (port_forwards or []):
            pf_el = SubElement(iface, "portForward", proto="tcp")
            SubElement(pf_el, "range", start=str(pf.host_port), to=str(pf.guest_port))
        return _element_to_str(iface), mac

    if config.mode == "bridge":
        if not config.bridge_name:
            raise ManagerError("NETWORK_BRIDGE must be set when NETWORK_MODE=bridge")
        iface = Element("interface", type="bridge")
        if boot_order is not None:
            SubElement(iface, "boot", order=str(boot_order))
        SubElement(iface, "mac", address=mac)
        if model == "virtio":
            SubElement(iface, "driver", name="vhost")
        SubElement(iface, "model", type=model)
        if rom_file:
            SubElement(iface, "rom", file=rom_file)
        SubElement(iface, "source", bridge=config.bridge_name)
        return _element_to_str(iface), mac

    if config.mode == "direct":
        if not config.direct_device:
            raise ManagerError("NETWORK_DIRECT_DEV must be set when NETWORK_MODE=direct")
        iface = Element("interface", type="direct")
        if boot_order is not None:
            SubElement(iface, "boot", order=str(boot_order))
        SubElement(iface, "mac", address=mac)
        if model == "virtio":
            SubElement(iface, "driver", name="vhost")
        SubElement(iface, "model", type=model)
        if rom_file:
            SubElement(iface, "rom", file=rom_file)
        SubElement(iface, "source", dev=config.direct_device, mode="bridge")
        return _element_to_str(iface), mac

    raise ManagerError(f"Unsupported network mode: {config.mode}")
