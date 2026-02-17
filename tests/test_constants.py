"""Tests for app.constants module."""

from pathlib import Path

from app.constants import (
    ARCH_ALIASES,
    DEFAULT_CONFIG_PATH,
    DISK_SIZE_RE,
    IPXE_DEFAULT_ROMS,
    MAC_ADDRESS_RE,
    SUPPORTED_ARCHES,
    SUPPORTED_NETWORK_MODELS,
    TRUTHY,
    _CONTAINER_ID_RE,
    _SENSITIVE_FIELDS,
)


class TestConstants:
    def test_default_config_path_is_pathlib(self):
        assert isinstance(DEFAULT_CONFIG_PATH, Path)

    def test_truthy_values(self):
        assert "1" in TRUTHY
        assert "true" in TRUTHY
        assert "yes" in TRUTHY
        assert "on" in TRUTHY
        assert "false" not in TRUTHY

    def test_mac_address_regex(self):
        assert MAC_ADDRESS_RE.match("52:54:00:aa:bb:cc")
        assert MAC_ADDRESS_RE.match("00:11:22:33:44:55")
        assert not MAC_ADDRESS_RE.match("invalid")
        assert not MAC_ADDRESS_RE.match("52:54:00:aa:bb")  # too short
        assert not MAC_ADDRESS_RE.match("52:54:00:aa:bb:cc:dd")  # too long
        assert not MAC_ADDRESS_RE.match("52:54:00:GG:bb:cc")  # invalid hex

    def test_disk_size_regex(self):
        assert DISK_SIZE_RE.match("20G")
        assert DISK_SIZE_RE.match("500M")
        assert DISK_SIZE_RE.match("1T")
        assert DISK_SIZE_RE.match("1024K")
        assert DISK_SIZE_RE.match("100")
        assert DISK_SIZE_RE.match("10g")
        assert not DISK_SIZE_RE.match("abc")
        assert not DISK_SIZE_RE.match("")
        assert not DISK_SIZE_RE.match("-1G")

    def test_supported_arches_all_have_machine(self):
        for arch, profile in SUPPORTED_ARCHES.items():
            assert "machine" in profile, f"{arch} missing 'machine'"
            assert "features" in profile, f"{arch} missing 'features'"
            assert "tcg_fallback" in profile, f"{arch} missing 'tcg_fallback'"

    def test_supported_arches_contains_expected(self):
        expected = {"x86_64", "aarch64", "ppc64", "s390x", "riscv64"}
        assert set(SUPPORTED_ARCHES.keys()) == expected

    def test_arch_aliases_map_to_valid_arches(self):
        for alias, target in ARCH_ALIASES.items():
            assert target in SUPPORTED_ARCHES, f"Alias '{alias}' maps to unknown arch '{target}'"

    def test_arch_aliases_contains_common(self):
        assert ARCH_ALIASES["amd64"] == "x86_64"
        assert ARCH_ALIASES["arm64"] == "aarch64"
        assert ARCH_ALIASES["ppc64le"] == "ppc64"
        assert ARCH_ALIASES["riscv"] == "riscv64"

    def test_network_models(self):
        assert "virtio" in SUPPORTED_NETWORK_MODELS
        assert "e1000" in SUPPORTED_NETWORK_MODELS

    def test_ipxe_roms_only_for_supported_arches(self):
        for arch in IPXE_DEFAULT_ROMS:
            assert arch in SUPPORTED_ARCHES

    def test_container_id_regex(self):
        assert _CONTAINER_ID_RE.match("a" * 12)
        assert _CONTAINER_ID_RE.match("0123456789ab")
        assert _CONTAINER_ID_RE.match("0" * 64)
        assert not _CONTAINER_ID_RE.match("short")
        assert not _CONTAINER_ID_RE.match("my-hostname")

    def test_sensitive_fields(self):
        assert "password" in _SENSITIVE_FIELDS
        assert "redfish_password" in _SENSITIVE_FIELDS

    def test_aarch64_has_firmware(self):
        firmware = SUPPORTED_ARCHES["aarch64"].get("firmware")
        assert firmware is not None
        assert "loader" in firmware
        assert "vars_template" in firmware
