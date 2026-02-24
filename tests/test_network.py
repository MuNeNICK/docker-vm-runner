"""Tests for app.network module."""

from __future__ import annotations

import pytest

from app.exceptions import ManagerError
from app.models import GuestAddress, NicConfig, PortForward
from app.network import render_network_xml


class TestRenderNetworkXmlUser:
    def test_basic_user_mode(self):
        nic = NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")
        xml, mac = render_network_xml(nic)
        assert mac == "52:54:00:aa:bb:cc"
        assert '<interface type="user">' in xml
        assert '<mac address="52:54:00:aa:bb:cc"/>' in xml
        assert '<model type="virtio"/>' in xml
        assert '<backend type="passt"/>' in xml
        assert '<ip family="ipv4" address="10.0.2.15" prefix="24"/>' in xml
        assert "</interface>" in xml

    def test_user_mode_ssh_port(self):
        nic = NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")
        xml, _ = render_network_xml(nic, ssh_port=2222)
        assert '<portForward proto="tcp">' in xml
        assert '<range start="2222" to="22"/>' in xml

    def test_user_mode_port_forwards(self):
        nic = NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")
        pf = [PortForward(8080, 80), PortForward(8443, 443)]
        xml, _ = render_network_xml(nic, port_forwards=pf)
        assert '<range start="8080" to="80"/>' in xml
        assert '<range start="8443" to="443"/>' in xml

    def test_user_mode_boot_order(self):
        nic = NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")
        xml, _ = render_network_xml(nic, boot_order=1)
        assert '<boot order="1"/>' in xml

    def test_user_mode_rom_file(self):
        nic = NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")
        xml, _ = render_network_xml(nic, rom_file="/usr/share/qemu/pxe-virtio.rom")
        assert '<rom file="/usr/share/qemu/pxe-virtio.rom"/>' in xml

    def test_user_mode_generates_mac_if_missing(self):
        nic = NicConfig(mode="user")
        xml, mac = render_network_xml(nic)
        assert mac.startswith("52:54:00:")
        assert f'<mac address="{mac}"/>' in xml

    def test_custom_guest_ip(self):
        nic = NicConfig(
            mode="user", mac_address="52:54:00:aa:bb:cc",
            guest_ips=[GuestAddress("192.168.1.100", 24)],
        )
        xml, _ = render_network_xml(nic)
        assert '<ip family="ipv4" address="192.168.1.100" prefix="24"/>' in xml
        assert "10.0.2.15" not in xml

    def test_custom_guest_ip_with_prefix(self):
        nic = NicConfig(
            mode="user", mac_address="52:54:00:aa:bb:cc",
            guest_ips=[GuestAddress("172.16.0.10", 12)],
        )
        xml, _ = render_network_xml(nic)
        assert '<ip family="ipv4" address="172.16.0.10" prefix="12"/>' in xml

    def test_multiple_guest_ips(self):
        nic = NicConfig(
            mode="user", mac_address="52:54:00:aa:bb:cc",
            guest_ips=[
                GuestAddress("10.0.2.15", 24),
                GuestAddress("192.168.1.100", 16),
            ],
        )
        xml, _ = render_network_xml(nic)
        assert '<ip family="ipv4" address="10.0.2.15" prefix="24"/>' in xml
        assert '<ip family="ipv4" address="192.168.1.100" prefix="16"/>' in xml

    def test_custom_guest_ip6(self):
        nic = NicConfig(
            mode="user", mac_address="52:54:00:aa:bb:cc",
            guest_ip6s=[GuestAddress("fd00::1", 64)],
        )
        xml, _ = render_network_xml(nic)
        assert '<ip family="ipv6" address="fd00::1" prefix="64"/>' in xml
        assert "fec0::2" not in xml

    def test_multiple_guest_ip6s(self):
        nic = NicConfig(
            mode="user", mac_address="52:54:00:aa:bb:cc",
            guest_ip6s=[
                GuestAddress("fec0::2", 64),
                GuestAddress("fd00::1", 128),
            ],
        )
        xml, _ = render_network_xml(nic)
        assert '<ip family="ipv6" address="fec0::2" prefix="64"/>' in xml
        assert '<ip family="ipv6" address="fd00::1" prefix="128"/>' in xml

    def test_ipv6_enabled_uses_default(self):
        nic = NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")
        xml, _ = render_network_xml(nic, ipv6_enabled=True)
        assert '<ip family="ipv6" address="fec0::2" prefix="64"/>' in xml

    def test_ipv6_disabled_no_ipv6(self):
        nic = NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")
        xml, _ = render_network_xml(nic, ipv6_enabled=False)
        assert "ipv6" not in xml

    def test_explicit_ip6_overrides_ipv6_enabled(self):
        nic = NicConfig(
            mode="user", mac_address="52:54:00:aa:bb:cc",
            guest_ip6s=[GuestAddress("fd00::5", 48)],
        )
        xml, _ = render_network_xml(nic, ipv6_enabled=True)
        assert '<ip family="ipv6" address="fd00::5" prefix="48"/>' in xml
        assert "fec0::2" not in xml

    def test_mixed_ipv4_and_ipv6(self):
        nic = NicConfig(
            mode="user", mac_address="52:54:00:aa:bb:cc",
            guest_ips=[GuestAddress("192.168.0.10", 24)],
            guest_ip6s=[GuestAddress("fd00::10", 64)],
        )
        xml, _ = render_network_xml(nic)
        assert '<ip family="ipv4" address="192.168.0.10" prefix="24"/>' in xml
        assert '<ip family="ipv6" address="fd00::10" prefix="64"/>' in xml


class TestRenderNetworkXmlBridge:
    def test_basic_bridge_mode(self):
        nic = NicConfig(mode="bridge", bridge_name="br0", mac_address="52:54:00:11:22:33")
        xml, mac = render_network_xml(nic)
        assert '<interface type="bridge">' in xml
        assert '<source bridge="br0"/>' in xml
        assert '<driver name="vhost"/>' in xml  # virtio default

    def test_bridge_without_name_raises(self):
        nic = NicConfig(mode="bridge")
        with pytest.raises(ManagerError, match="NETWORK_BRIDGE must be set"):
            render_network_xml(nic)

    def test_bridge_non_virtio_no_vhost(self):
        nic = NicConfig(mode="bridge", bridge_name="br0", model="e1000", mac_address="52:54:00:11:22:33")
        xml, _ = render_network_xml(nic)
        assert '<driver name="vhost"/>' not in xml
        assert '<model type="e1000"/>' in xml


class TestRenderNetworkXmlDirect:
    def test_basic_direct_mode(self):
        nic = NicConfig(mode="direct", direct_device="eth0", mac_address="52:54:00:11:22:33")
        xml, mac = render_network_xml(nic)
        assert '<interface type="direct">' in xml
        assert '<source dev="eth0" mode="bridge"/>' in xml

    def test_direct_without_device_raises(self):
        nic = NicConfig(mode="direct")
        with pytest.raises(ManagerError, match="NETWORK_DIRECT_DEV must be set"):
            render_network_xml(nic)


class TestRenderNetworkXmlUnsupported:
    def test_unsupported_mode_raises(self):
        nic = NicConfig(mode="invalid")
        with pytest.raises(ManagerError, match="Unsupported network mode"):
            render_network_xml(nic)


class TestRenderNetworkXmlMacOverride:
    def test_mac_address_parameter_overrides_config(self):
        nic = NicConfig(mode="user", mac_address="52:54:00:aa:bb:cc")
        _, mac = render_network_xml(nic, mac_address="52:54:00:11:22:33")
        assert mac == "52:54:00:11:22:33"
