"""Network XML generation for Docker-VM-Runner."""

from __future__ import annotations

from typing import List, Optional, Tuple
from xml.etree.ElementTree import Element, SubElement, tostring

from app.exceptions import ManagerError
from app.models import GuestAddress, NicConfig, PortForward
from app.utils import random_mac

_DEFAULT_IPV4 = GuestAddress("10.0.2.15", 24)
_DEFAULT_IPV6 = GuestAddress("fec0::2", 64)


def _element_to_str(root: Element) -> str:
    """Serialize an ElementTree element to a pretty-printed XML string without declaration."""
    from xml.dom.minidom import parseString

    raw = tostring(root, encoding="unicode")
    return parseString(raw).documentElement.toprettyxml(indent="  ").strip()


def _add_mtu(iface: Element, mtu: Optional[int]) -> None:
    """Add MTU element to interface if non-default."""
    if mtu is not None and mtu != 1500:
        SubElement(iface, "mtu", size=str(mtu))


def render_network_xml(
    config: NicConfig,
    ssh_port: Optional[int] = None,
    mac_address: Optional[str] = None,
    boot_order: Optional[int] = None,
    rom_file: Optional[str] = None,
    port_forwards: Optional[List[PortForward]] = None,
    ipv6_enabled: bool = False,
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
        ipv4_addrs = config.guest_ips or [_DEFAULT_IPV4]
        for addr in ipv4_addrs:
            SubElement(iface, "ip", family="ipv4", address=addr.address, prefix=str(addr.prefix))
        if config.guest_ip6s:
            for addr in config.guest_ip6s:
                SubElement(iface, "ip", family="ipv6", address=addr.address, prefix=str(addr.prefix))
        elif ipv6_enabled:
            SubElement(iface, "ip", family="ipv6", address=_DEFAULT_IPV6.address, prefix=str(_DEFAULT_IPV6.prefix))
        SubElement(iface, "model", type=model)
        _add_mtu(iface, config.mtu)
        if rom_file:
            SubElement(iface, "rom", file=rom_file)
        if ssh_port is not None:
            pf_el = SubElement(iface, "portForward", proto="tcp")
            SubElement(pf_el, "range", start=str(ssh_port), to="22")
        for pf in port_forwards or []:
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
        _add_mtu(iface, config.mtu)
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
        _add_mtu(iface, config.mtu)
        if rom_file:
            SubElement(iface, "rom", file=rom_file)
        SubElement(iface, "source", dev=config.direct_device, mode="bridge")
        return _element_to_str(iface), mac

    raise ManagerError(f"Unsupported network mode: {config.mode}")
