"""Tests for app.network module."""

from __future__ import annotations

import pytest

from app.exceptions import ManagerError
from app.models import NicConfig, PortForward
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
