"""Tests for app.services module."""

from __future__ import annotations

import errno
import subprocess
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from app.exceptions import ManagerError
from app.runtime import RuntimeInfo
from app.services import ServiceManager


@pytest.fixture
def service_manager(default_vm_config, tmp_path, monkeypatch):
    monkeypatch.setattr("app.services.STATE_DIR", tmp_path / "state")
    monkeypatch.setattr(
        "app.services.detect_runtime",
        lambda: RuntimeInfo(engine="docker", rootless=False, privileged=True),
    )
    manager = ServiceManager(default_vm_config)
    manager._storage_pool_path = tmp_path / "storage-pool"
    return manager


class TestServiceStart:
    def test_redfish_disabled(self, service_manager):
        service_manager.vm_config.redfish_enabled = False
        with (
            patch.object(service_manager, "_start_libvirt") as mock_start_libvirt,
            patch.object(service_manager, "_wait_for_libvirt") as mock_wait_libvirt,
            patch.object(service_manager, "_ensure_storage_pool") as mock_pool,
            patch.object(service_manager, "_start_sushy") as mock_sushy,
        ):
            service_manager.start()
        mock_start_libvirt.assert_called_once()
        mock_wait_libvirt.assert_called_once()
        mock_pool.assert_not_called()
        mock_sushy.assert_not_called()

    def test_redfish_enabled(self, service_manager):
        service_manager.vm_config.redfish_enabled = True
        with (
            patch.object(service_manager, "_start_libvirt"),
            patch.object(service_manager, "_wait_for_libvirt"),
            patch.object(service_manager, "_ensure_storage_pool") as mock_pool,
            patch.object(service_manager, "_start_sushy") as mock_sushy,
        ):
            service_manager.start()
        mock_pool.assert_called_once()
        mock_sushy.assert_called_once()


class TestWaitForLibvirt:
    def test_rootless_missing_socket_warns(self, service_manager):
        service_manager.runtime = RuntimeInfo(engine="docker", rootless=True, privileged=False)
        with patch("app.services.wait_for_path", return_value=False):
            service_manager._wait_for_libvirt()

    def test_non_rootless_missing_socket_raises(self, service_manager):
        service_manager.runtime = RuntimeInfo(engine="docker", rootless=False, privileged=True)
        with patch("app.services.wait_for_path", return_value=False):
            with pytest.raises(ManagerError, match="libvirt socket did not appear"):
                service_manager._wait_for_libvirt()


class TestSocketCleanup:
    def test_active_socket_is_kept(self, service_manager):
        path = MagicMock(spec=Path)
        path.exists.return_value = True
        path.is_socket.return_value = True

        client = MagicMock()
        sock_ctx = MagicMock()
        sock_ctx.__enter__.return_value = client
        with patch("app.services.socket.socket", return_value=sock_ctx):
            service_manager._cleanup_socket(path)
        path.unlink.assert_not_called()

    def test_stale_socket_is_removed(self, service_manager):
        path = MagicMock(spec=Path)
        path.exists.return_value = True
        path.is_socket.return_value = True

        client = MagicMock()
        client.connect.side_effect = OSError(errno.ECONNREFUSED, "connection refused")
        sock_ctx = MagicMock()
        sock_ctx.__enter__.return_value = client
        with patch("app.services.socket.socket", return_value=sock_ctx):
            service_manager._cleanup_socket(path)
        path.unlink.assert_called_once()


class TestHelpers:
    def test_assert_running_raises_when_exited(self, service_manager):
        proc = MagicMock()
        proc.poll.return_value = 1
        proc.communicate.return_value = ("out", "err")
        proc.returncode = 1
        with pytest.raises(ManagerError, match="exited prematurely"):
            service_manager._assert_running(proc, "libvirtd")

    def test_write_auth_file(self, service_manager):
        with (
            patch("app.services.bcrypt.gensalt", return_value=b"salt"),
            patch("app.services.bcrypt.hashpw", return_value=b"$2b$hash"),
        ):
            auth = service_manager._write_auth_file()
        text = auth.read_text()
        assert text == "admin:$2b$hash\n"

    def test_write_config(self, service_manager, tmp_path):
        cert = tmp_path / "cert.crt"
        key = tmp_path / "key.key"
        auth = tmp_path / "auth"
        config = service_manager._write_config(cert, key, auth)
        text = config.read_text()
        assert "SUSHY_EMULATOR_LIBVIRT_URI" in text
        assert 'SUSHY_EMULATOR_LISTEN_IP = "0.0.0.0"' in text
        assert f"SUSHY_EMULATOR_SSL_CERT = '{cert}'" in text
        assert f"SUSHY_EMULATOR_SSL_KEY = '{key}'" in text
        assert f"SUSHY_EMULATOR_AUTH_FILE = '{auth}'" in text


class TestStoragePool:
    def test_open_connection_none_returns(self, service_manager):
        with patch("app.services.libvirt.open", return_value=None):
            service_manager._ensure_storage_pool()

    def test_creates_and_activates_pool(self, service_manager):
        pool = MagicMock()
        pool.isActive.return_value = 0
        pool.autostart.return_value = 0

        conn = MagicMock()
        # Lookup misses, then define pool
        from app import services as services_mod

        conn.storagePoolLookupByName.side_effect = services_mod.libvirt.libvirtError("missing")
        conn.storagePoolDefineXML.return_value = pool

        with patch("app.services.libvirt.open", return_value=conn):
            service_manager._ensure_storage_pool()

        conn.storagePoolDefineXML.assert_called_once()
        pool.create.assert_called_once_with(0)
        pool.setAutostart.assert_called_once_with(True)
        conn.close.assert_called_once()


class TestNoVNC:
    def test_disabled_is_noop(self, service_manager):
        service_manager.vm_config.novnc_enabled = False
        with patch("app.services.subprocess.Popen") as mock_popen:
            service_manager.start_novnc()
        mock_popen.assert_not_called()

    def test_missing_websockify_raises(self, service_manager):
        service_manager.vm_config.novnc_enabled = True
        with patch("app.services.shutil.which", return_value=None):
            with pytest.raises(ManagerError, match="websockify is missing"):
                service_manager.start_novnc()

    def test_missing_novnc_assets_raises(self, service_manager, tmp_path):
        service_manager.vm_config.novnc_enabled = True
        missing_assets = tmp_path / "novnc-missing"
        with (
            patch("app.services.shutil.which", return_value="/usr/bin/websockify"),
            patch("app.services._NOVNC_ROOT", missing_assets),
        ):
            with pytest.raises(ManagerError, match="static assets not found"):
                service_manager.start_novnc()

    def test_start_success(self, service_manager, tmp_path):
        service_manager.vm_config.novnc_enabled = True
        assets = tmp_path / "novnc-assets"
        assets.mkdir()
        proc = MagicMock()
        with (
            patch("app.services.shutil.which", return_value="/usr/bin/websockify"),
            patch("app.services._NOVNC_ROOT", assets),
            patch.object(service_manager, "_ensure_certificates") as mock_ensure_certificates,
            patch("app.services.subprocess.Popen", return_value=proc) as mock_popen,
        ):
            service_manager.start_novnc()
        mock_ensure_certificates.assert_called_once()
        mock_popen.assert_called_once()
        assert service_manager._novnc_started is True
        assert proc in service_manager.processes


class TestStop:
    def test_terminates_and_kills_on_timeout(self, service_manager):
        running = MagicMock()
        running.poll.return_value = None
        running.wait.side_effect = subprocess.TimeoutExpired(cmd="x", timeout=5)
        exited = MagicMock()
        exited.poll.return_value = 0
        exited.wait.return_value = 0
        service_manager.processes = [running, exited]

        service_manager.stop()
        service_manager.stop()

        running.terminate.assert_called_once()
        running.kill.assert_called_once()
        exited.terminate.assert_not_called()
