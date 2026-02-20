"""Runtime/control-path tests for app.vm."""

from __future__ import annotations

import subprocess
from unittest.mock import MagicMock, patch

import pytest

from app.exceptions import ManagerError
from app.runtime import RuntimeInfo
from app.vm import VMManager


def _make_mgr(default_vm_config, tmp_path):
    with patch.object(VMManager, "__init__", lambda self, *a, **kw: None):
        mgr = VMManager.__new__(VMManager)
        mgr.cfg = default_vm_config
        mgr.cfg.vm_name = "test-vm"
        mgr.conn = MagicMock()
        mgr.domain = MagicMock()
        mgr.service_manager = MagicMock()
        mgr.service_manager.runtime = RuntimeInfo(engine="docker", rootless=False, privileged=True)
        mgr._firmware_vars_path = None
        mgr._tpm_process = None
        mgr.vm_dir = tmp_path / "vm-dir"
        mgr.vm_dir.mkdir(parents=True, exist_ok=True)
        return mgr


class TestNetworkFallback:
    def test_try_network_fallback_success(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.domain.XMLDesc.return_value = '<domain><backend type="passt"/></domain>'
        replacement = MagicMock()
        mgr.conn.defineXML.return_value = replacement

        ok = mgr._try_network_fallback()

        assert ok is True
        mgr.conn.defineXML.assert_called_once()
        replacement.create.assert_called_once()

    def test_try_network_fallback_returns_false_without_passt(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.domain.XMLDesc.return_value = "<domain/>"
        assert mgr._try_network_fallback() is False


class TestGuestExec:
    def test_guest_exec_success(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        exec_result = subprocess.CompletedProcess(
            args=["virsh"],
            returncode=0,
            stdout='{"return":{"pid":7}}',
            stderr="",
        )
        status_result = subprocess.CompletedProcess(
            args=["virsh"],
            returncode=0,
            stdout='{"return":{"exited":true,"exitcode":0,"out-data":"aGVsbG8K"}}',
            stderr="",
        )
        with patch("app.vm.subprocess.run", side_effect=[exec_result, status_result]), patch("app.vm.time.sleep"):
            ret = mgr._guest_exec("echo", ["hello"])
        assert ret == (0, "hello\n")

    def test_guest_exec_initial_failure_returns_none(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        failure = subprocess.CompletedProcess(args=["virsh"], returncode=1, stdout="", stderr="error")
        with patch("app.vm.subprocess.run", return_value=failure):
            assert mgr._guest_exec("echo", ["hello"]) is None


class TestGuestWaiters:
    def test_wait_for_guest_agent_success(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        first = subprocess.CompletedProcess(args=["virsh"], returncode=1, stdout="", stderr="")
        second = subprocess.CompletedProcess(args=["virsh"], returncode=0, stdout="{}", stderr="")
        with patch("app.vm.subprocess.run", side_effect=[first, second]), patch("app.vm.time.sleep"):
            assert mgr.wait_for_guest_agent(timeout=5, interval=0) is True

    def test_wait_for_guest_ready_done(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.cloud_init_enabled = True
        with (
            patch.object(mgr, "wait_for_guest_agent", return_value=True),
            patch.object(mgr, "_guest_exec", side_effect=[(0, "status: running"), (0, "status: done")]),
            patch("app.vm.time.sleep"),
        ):
            assert mgr.wait_for_guest_ready(timeout=5, interval=0) is True

    def test_wait_for_guest_ready_many_failures_still_returns_true(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.cloud_init_enabled = True
        with (
            patch.object(mgr, "wait_for_guest_agent", return_value=True),
            patch.object(mgr, "_guest_exec", side_effect=[None] * 30) as mock_exec,
            patch("app.vm.time.sleep"),
        ):
            assert mgr.wait_for_guest_ready(timeout=5, interval=0) is True
        assert mock_exec.call_count == 30


class TestStartAndCleanup:
    def test_start_uses_network_fallback_on_passt_error(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.novnc_enabled = True
        passt_error = __import__("app.vm", fromlist=["libvirt"]).libvirt.libvirtError("passt backend failed")
        mgr.domain.isActive.return_value = False
        mgr.domain.create.side_effect = passt_error
        with patch.object(mgr, "_try_network_fallback", return_value=True):
            mgr.start()
        mgr.service_manager.start_novnc.assert_called_once()

    def test_start_cgroup_error_raises(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        cgroup_error = __import__("app.vm", fromlist=["libvirt"]).libvirt.libvirtError("cgroup permission denied")
        mgr.domain.isActive.return_value = False
        mgr.domain.create.side_effect = cgroup_error
        with pytest.raises(ManagerError, match="--cgroupns=host"):
            mgr.start()

    def test_cleanup_removes_ephemeral_vm_dir(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.persist = False
        mgr.domain.isActive.return_value = True
        with patch.object(mgr, "_kill_remaining_qemu") as mock_kill:
            mgr.cleanup()
        assert not mgr.vm_dir.exists()
        mgr.domain.destroy.assert_called_once()
        mgr.domain.undefine.assert_called_once()
        mock_kill.assert_called_once()

    def test_kill_remaining_qemu_processes(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        pgrep_out = subprocess.CompletedProcess(args=["pgrep"], returncode=0, stdout="123\n456\n", stderr="")
        with patch("app.vm.subprocess.run", return_value=pgrep_out), patch("app.vm.os.kill") as mock_kill:
            mgr._kill_remaining_qemu()
        assert mock_kill.call_count == 2
