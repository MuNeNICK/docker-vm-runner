"""Tests for app.utils module."""

from __future__ import annotations

import re

import pytest

from app.exceptions import ManagerError
from app.utils import (
    derive_vm_name,
    deterministic_mac,
    get_env,
    get_env_bool,
    hash_password,
    log,
    parse_int_env,
    parse_size_to_bytes,
    random_mac,
    sanitize_mount_target,
    validate_disk_size,
)


class TestLog:
    def test_info_level(self, capsys):
        log("INFO", "test message")
        captured = capsys.readouterr()
        assert "[INFO]" in captured.out
        assert "test message" in captured.out

    def test_debug_suppressed_by_default(self, capsys):
        log("DEBUG", "should not appear")
        captured = capsys.readouterr()
        assert captured.out == ""


class TestGetEnv:
    def test_returns_value(self, monkeypatch):
        monkeypatch.setenv("TEST_VAR", "hello")
        assert get_env("TEST_VAR") == "hello"

    def test_returns_default(self, monkeypatch):
        monkeypatch.delenv("TEST_VAR", raising=False)
        assert get_env("TEST_VAR", "fallback") == "fallback"

    def test_returns_none_without_default(self, monkeypatch):
        monkeypatch.delenv("TEST_VAR", raising=False)
        assert get_env("TEST_VAR") is None


class TestGetEnvBool:
    @pytest.mark.parametrize("value", ["1", "true", "yes", "on", "TRUE", "Yes"])
    def test_truthy_values(self, monkeypatch, value):
        monkeypatch.setenv("TEST_BOOL", value)
        assert get_env_bool("TEST_BOOL") is True

    @pytest.mark.parametrize("value", ["0", "false", "no", "off", "random"])
    def test_falsy_values(self, monkeypatch, value):
        monkeypatch.setenv("TEST_BOOL", value)
        assert get_env_bool("TEST_BOOL") is False

    def test_default_when_unset(self, monkeypatch):
        monkeypatch.delenv("TEST_BOOL", raising=False)
        assert get_env_bool("TEST_BOOL", True) is True
        assert get_env_bool("TEST_BOOL", False) is False


class TestParseIntEnv:
    def test_valid_value(self, monkeypatch):
        monkeypatch.setenv("MY_INT", "42")
        assert parse_int_env("MY_INT", "10") == 42

    def test_default_value(self, monkeypatch):
        monkeypatch.delenv("MY_INT", raising=False)
        assert parse_int_env("MY_INT", "10") == 10

    def test_non_integer_raises(self, monkeypatch):
        monkeypatch.setenv("MY_INT", "abc")
        with pytest.raises(ManagerError, match="must be an integer"):
            parse_int_env("MY_INT", "10")

    def test_below_min_raises(self, monkeypatch):
        monkeypatch.setenv("MY_INT", "0")
        with pytest.raises(ManagerError, match="must be >= 1"):
            parse_int_env("MY_INT", "10", min_val=1)

    def test_above_max_raises(self, monkeypatch):
        monkeypatch.setenv("MY_INT", "70000")
        with pytest.raises(ManagerError, match="must be <= 65535"):
            parse_int_env("MY_INT", "10", max_val=65535)

    def test_within_range(self, monkeypatch):
        monkeypatch.setenv("MY_INT", "100")
        assert parse_int_env("MY_INT", "10", min_val=1, max_val=1000) == 100


class TestValidateDiskSize:
    @pytest.mark.parametrize("size", ["10G", "500M", "1T", "1024K", "100", "20g"])
    def test_valid_sizes(self, size):
        assert validate_disk_size(size) == size

    @pytest.mark.parametrize("size", ["abc", "", "-1G", "10X"])
    def test_invalid_sizes(self, size):
        with pytest.raises(ManagerError, match="Invalid DISK_SIZE"):
            validate_disk_size(size)


class TestRandomMac:
    def test_format(self):
        mac = random_mac()
        assert re.match(r"^52:54:00:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}$", mac)

    def test_qemu_prefix(self):
        mac = random_mac()
        assert mac.startswith("52:54:00:")


class TestDeterministicMac:
    def test_same_seed_same_mac(self):
        mac1 = deterministic_mac("test-seed")
        mac2 = deterministic_mac("test-seed")
        assert mac1 == mac2

    def test_different_seed_different_mac(self):
        mac1 = deterministic_mac("seed-a")
        mac2 = deterministic_mac("seed-b")
        assert mac1 != mac2

    def test_format(self):
        mac = deterministic_mac("test")
        assert re.match(r"^52:54:00:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}$", mac)

    def test_locally_administered_bit(self):
        mac = deterministic_mac("test")
        octets = mac.split(":")
        third_octet = int(octets[3], 16)
        assert third_octet & 0x02 == 0x02  # locally administered bit set
        assert third_octet & 0x01 == 0x00  # multicast bit clear


class TestDeriveVmName:
    def test_explicit_guest_name(self, monkeypatch):
        monkeypatch.setenv("GUEST_NAME", "my-vm")
        monkeypatch.delenv("HOSTNAME", raising=False)
        assert derive_vm_name("ubuntu") == "my-vm"

    def test_hostname_used(self, monkeypatch):
        monkeypatch.delenv("GUEST_NAME", raising=False)
        monkeypatch.setenv("HOSTNAME", "my-host")
        assert derive_vm_name("ubuntu") == "my-host"

    def test_container_id_hostname_ignored(self, monkeypatch):
        monkeypatch.delenv("GUEST_NAME", raising=False)
        monkeypatch.setenv("HOSTNAME", "a" * 12)
        assert derive_vm_name("ubuntu") == "ubuntu"

    def test_distro_fallback(self, monkeypatch):
        monkeypatch.delenv("GUEST_NAME", raising=False)
        monkeypatch.delenv("HOSTNAME", raising=False)
        assert derive_vm_name("ubuntu") == "ubuntu"

    def test_iso_mode_fallback(self, monkeypatch):
        monkeypatch.delenv("GUEST_NAME", raising=False)
        monkeypatch.delenv("HOSTNAME", raising=False)
        assert derive_vm_name("ubuntu", iso_mode=True) == "custom-vm"


class TestSanitizeMountTarget:
    def test_simple_name(self):
        assert sanitize_mount_target("myshare") == "myshare"

    def test_special_characters(self):
        assert sanitize_mount_target("my/share") == "my-share"

    def test_spaces(self):
        assert sanitize_mount_target("my share") == "my-share"

    def test_empty_string(self):
        assert sanitize_mount_target("") == "share"

    def test_all_special(self):
        assert sanitize_mount_target("///") == "share"

    def test_dots_and_hyphens_preserved(self):
        assert sanitize_mount_target("my.share-name") == "my.share-name"


class TestParseSizeToBytes:
    @pytest.mark.parametrize(
        "raw,expected",
        [
            ("20G", 20 * 1024**3),
            ("500M", 500 * 1024**2),
            ("1T", 1 * 1024**4),
            ("1024K", 1024 * 1024),
            ("100", 100),
            ("20g", 20 * 1024**3),
        ],
    )
    def test_valid_sizes(self, raw, expected):
        assert parse_size_to_bytes(raw) == expected

    def test_zero(self):
        assert parse_size_to_bytes("0") == 0


class TestHashPassword:
    def test_returns_string(self):
        result = hash_password("password")
        assert isinstance(result, str)

    def test_bcrypt_format(self):
        result = hash_password("password")
        assert result.startswith("$2")

    def test_different_calls_different_hashes(self):
        h1 = hash_password("password")
        h2 = hash_password("password")
        assert h1 != h2  # different salts
