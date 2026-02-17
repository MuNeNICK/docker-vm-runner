"""Tests for app.runtime module."""

from __future__ import annotations

from unittest.mock import mock_open, patch

from app.runtime import RuntimeInfo, _detect_engine, _is_privileged, _is_rootless


class TestDetectEngine:
    @patch("app.runtime.Path")
    def test_kubernetes(self, mock_path):
        mock_path.return_value.exists.side_effect = lambda: True
        # First call checks kubernetes path
        with patch("app.runtime.Path") as mp:
            mp.return_value.exists.side_effect = [True]
            result = _detect_engine()
            assert result == "kubernetes"

    @patch("app.runtime.Path")
    def test_podman(self, mock_path):
        calls = []

        def exists_side_effect():
            calls.append(1)
            if len(calls) == 1:
                return False  # k8s
            if len(calls) == 2:
                return True  # podman
            return False

        mock_path.return_value.exists = exists_side_effect
        result = _detect_engine()
        assert result == "podman"

    @patch("app.runtime.Path")
    def test_docker(self, mock_path):
        calls = []

        def exists_side_effect():
            calls.append(1)
            if len(calls) == 1:
                return False  # k8s
            if len(calls) == 2:
                return False  # podman
            if len(calls) == 3:
                return True  # docker
            return False

        mock_path.return_value.exists = exists_side_effect
        result = _detect_engine()
        assert result == "docker"


class TestIsRootless:
    def test_rootful(self):
        uid_map = "         0          0 4294967295\n"
        with patch("builtins.open", mock_open(read_data=uid_map)):
            assert _is_rootless() is False

    def test_rootless(self):
        uid_map = "         0       1000          1\n"
        with patch("builtins.open", mock_open(read_data=uid_map)):
            assert _is_rootless() is True

    def test_file_not_found(self):
        with patch("builtins.open", side_effect=OSError):
            assert _is_rootless() is False


class TestIsPrivileged:
    def test_privileged(self):
        status = "Name:\tpython\nCapBnd:\t000001ffffffffff\n"
        with patch("builtins.open", mock_open(read_data=status)):
            assert _is_privileged() is True

    def test_unprivileged(self):
        status = "Name:\tpython\nCapBnd:\t00000000a80425fb\n"
        with patch("builtins.open", mock_open(read_data=status)):
            assert _is_privileged() is False

    def test_file_not_found(self):
        with patch("builtins.open", side_effect=OSError):
            assert _is_privileged() is False


class TestRuntimeInfo:
    def test_dataclass(self):
        info = RuntimeInfo(engine="docker", rootless=False, privileged=True)
        assert info.engine == "docker"
        assert info.rootless is False
        assert info.privileged is True
