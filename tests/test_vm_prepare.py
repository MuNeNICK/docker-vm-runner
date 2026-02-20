"""Prepare/image/firmware tests for app.vm."""

from __future__ import annotations

import io
import json
import subprocess
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from app.exceptions import ManagerError
from app.models import DiskConfig, FilesystemConfig
from app.vm import VMManager


def _make_mgr(default_vm_config, tmp_path):
    with patch.object(VMManager, "__init__", lambda self, *a, **kw: None):
        mgr = VMManager.__new__(VMManager)
    cfg = default_vm_config
    cfg.vm_name = "test-vm"
    mgr.cfg = cfg
    mgr.service_manager = MagicMock()
    mgr.conn = MagicMock()
    mgr.domain = MagicMock()
    mgr._kvm_available = True
    mgr._effective_cpu_model = cfg.cpu_model
    mgr._arch_profile = {"machine": "q35", "features": (), "tcg_fallback": "qemu64"}
    mgr._firmware_loader_path = None
    mgr._firmware_vars_path = None
    mgr.vm_dir = tmp_path / "vm"
    mgr.vm_dir.mkdir(parents=True, exist_ok=True)
    mgr.base_image = tmp_path / "base.qcow2"
    mgr.work_image = tmp_path / "disk.qcow2"
    mgr.boot_iso = None
    mgr.seed_iso = tmp_path / "seed.iso"
    mgr._disk_reused = False
    mgr._network_macs = {}
    mgr._ipxe_rom_path = None
    mgr._tpm_process = None
    return mgr


class TestPrepareFlow:
    def test_prepare_requires_kvm_raises(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr._kvm_available = False
        with patch("app.vm.get_env_bool", return_value=True):
            with pytest.raises(ManagerError, match="REQUIRE_KVM=1"):
                mgr.prepare()

    def test_prepare_no_kvm_uses_fallback_and_calls_pipeline(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr._kvm_available = False
        mgr.cfg.cpu_model = "host"
        with (
            patch("app.vm.get_env_bool", return_value=False),
            patch.object(mgr, "_resolve_boot_from"),
            patch.object(mgr, "_ensure_base_image"),
            patch.object(mgr, "_prepare_work_image"),
            patch.object(mgr, "_extract_qemu_binary"),
            patch.object(mgr, "_prepare_firmware"),
            patch.object(mgr, "_start_tpm"),
            patch.object(mgr, "_generate_cloud_init"),
            patch.object(mgr, "_define_domain"),
        ):
            mgr.prepare()
        assert mgr._effective_cpu_model == "qemu64"

    def test_prepare_skips_iso_when_disk_already_installed(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.boot_iso = tmp_path / "installer.iso"
        mgr.boot_iso.write_bytes(b"iso")
        mgr._disk_reused = True
        mgr.cfg.persist = True
        mgr.cfg.boot_order = ["cdrom"]
        with (
            patch.object(mgr, "_resolve_boot_from"),
            patch.object(mgr, "_ensure_base_image"),
            patch.object(mgr, "_prepare_work_image"),
            patch.object(mgr, "_is_installed", return_value=True),
            patch.object(mgr, "_extract_qemu_binary"),
            patch.object(mgr, "_prepare_firmware"),
            patch.object(mgr, "_start_tpm"),
            patch.object(mgr, "_generate_cloud_init"),
            patch.object(mgr, "_define_domain"),
        ):
            mgr.prepare()
        assert mgr.boot_iso is None
        assert "cdrom" not in mgr.cfg.boot_order
        assert mgr.cfg.boot_order[0] == "hd"

    def test_prepare_raises_when_boot_iso_missing(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.boot_iso = tmp_path / "missing.iso"
        with (
            patch.object(mgr, "_resolve_boot_from"),
            patch.object(mgr, "_ensure_base_image"),
            patch.object(mgr, "_prepare_work_image"),
        ):
            with pytest.raises(ManagerError, match="Boot ISO not found"):
                mgr.prepare()


class TestBaseImageAndWorkDisk:
    def test_ensure_base_image_uses_cache_when_large(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.base_image.parent.mkdir(parents=True, exist_ok=True)
        with open(mgr.base_image, "wb") as f:
            f.truncate(101 * 1024 * 1024)
        with patch("app.vm.download_file_with_retry") as mock_download:
            mgr._ensure_base_image()
        mock_download.assert_not_called()

    def test_ensure_base_image_redownloads_small_file(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.image_url = "https://example.com/disk.tar.xz"
        mgr.base_image.parent.mkdir(parents=True, exist_ok=True)
        mgr.base_image.write_bytes(b"small")
        with (
            patch("app.vm.download_file_with_retry") as mock_download,
            patch.object(mgr, "_post_process_image") as mock_post,
        ):
            mgr._ensure_base_image()
        assert mock_download.call_count == 1
        download_path = mock_download.call_args.args[1]
        assert str(download_path).endswith("disk.tar.xz")
        mock_post.assert_called_once_with(download_path)

    def test_post_process_converts_raw_and_updates_paths(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        source = tmp_path / "image.raw"
        source.write_bytes(b"rawdata")

        def _convert(_src: Path, dest: Path):
            dest.write_bytes(b"converted")

        with (
            patch("app.vm.detect_image_format", return_value="raw"),
            patch("app.vm.convert_disk_image", side_effect=_convert),
        ):
            mgr._post_process_image(source)
        assert mgr.base_image.exists()
        assert mgr.base_image.read_bytes() == b"converted"
        assert mgr.cfg.image_format == "qcow2"

    def test_prepare_work_image_blank_disk_path(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.blank_work_disk = True
        mgr.cfg.disk_preallocate = True
        with (
            patch("app.vm.check_filesystem_compatibility"),
            patch("app.vm.check_disk_space"),
            patch("app.vm.run") as mock_run,
            patch.object(mgr, "_prepare_extra_disks"),
        ):
            mgr._prepare_work_image()
        assert mock_run.call_args.args[0][:3] == ["qemu-img", "create", "-f"]

    def test_prepare_work_image_iso_base_raises(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.blank_work_disk = False
        mgr.base_image = tmp_path / "installer.iso"
        with (
            patch("app.vm.check_filesystem_compatibility"),
            patch("app.vm.check_disk_space"),
        ):
            with pytest.raises(ManagerError, match="Base image is an ISO"):
                mgr._prepare_work_image()

    def test_prepare_work_image_resizes_persistent_disk(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.persist = True
        mgr.cfg.disk_size = "20G"
        with open(mgr.work_image, "wb") as f:
            f.truncate(120 * 1024 * 1024)
        info = subprocess.CompletedProcess(
            args=["qemu-img"],
            returncode=0,
            stdout=json.dumps({"virtual-size": 1024**3}),
            stderr="",
        )
        with (
            patch("app.vm.check_filesystem_compatibility"),
            patch("app.vm.check_disk_space"),
            patch("app.vm.subprocess.run", return_value=info),
            patch("app.vm.run") as mock_run,
            patch.object(mgr, "_prepare_extra_disks"),
        ):
            mgr._prepare_work_image()
        assert any(call.args[0][1] == "resize" for call in mock_run.mock_calls)

    def test_prepare_extra_disks_creates_new(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.extra_disks = [DiskConfig(size="5G", index=2)]
        with patch("app.vm.run") as mock_run:
            mgr._prepare_extra_disks()
        mock_run.assert_called_once()

    def test_prepare_extra_disks_reuses_existing_when_persist(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.persist = True
        mgr.cfg.extra_disks = [DiskConfig(size="5G", index=2)]
        existing = mgr.vm_dir / f"disk2.{mgr.cfg.image_format}"
        existing.write_bytes(b"existing")
        with patch("app.vm.run") as mock_run:
            mgr._prepare_extra_disks()
        mock_run.assert_not_called()


class TestFirmwareTpmAndCloudInit:
    def test_prepare_firmware_x86_legacy_noop(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.arch = "x86_64"
        mgr.cfg.boot_mode = "legacy"
        mgr._arch_profile = {"firmware": {}}
        mgr._prepare_firmware()
        assert mgr._firmware_loader_path is None

    def test_prepare_firmware_x86_missing_loader_raises(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.arch = "x86_64"
        mgr.cfg.boot_mode = "uefi"
        mgr._arch_profile = {
            "firmware": {
                "uefi": {
                    "loader": str(tmp_path / "missing-loader.fd"),
                    "vars_template": str(tmp_path / "missing-vars.fd"),
                }
            }
        }
        with pytest.raises(ManagerError, match="OVMF firmware not found"):
            mgr._prepare_firmware()

    def test_prepare_firmware_x86_success(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.arch = "x86_64"
        mgr.cfg.boot_mode = "uefi"
        loader = tmp_path / "OVMF_CODE.fd"
        vars_template = tmp_path / "OVMF_VARS.fd"
        loader.write_bytes(b"loader")
        vars_template.write_bytes(b"vars")
        mgr._arch_profile = {
            "firmware": {
                "uefi": {
                    "loader": str(loader),
                    "vars_template": str(vars_template),
                }
            }
        }
        with patch("app.vm.STATE_DIR", tmp_path / "state"):
            mgr._prepare_firmware()
        assert mgr._firmware_loader_path == loader
        assert mgr._firmware_vars_path is not None
        assert mgr._firmware_vars_path.exists()

    def test_start_tpm_failures_and_success(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.tpm_enabled = True

        with (
            patch("app.vm.STATE_DIR", tmp_path / "state"),
            patch("app.vm.subprocess.Popen", side_effect=FileNotFoundError),
        ):
            with pytest.raises(ManagerError, match="swtpm not found"):
                mgr._start_tpm()

        failing_proc = MagicMock()
        failing_proc.poll.return_value = 1
        failing_proc.stderr = io.BytesIO(b"failed")
        with (
            patch("app.vm.STATE_DIR", tmp_path / "state"),
            patch("app.vm.subprocess.Popen", return_value=failing_proc),
            patch("app.vm.time.sleep"),
        ):
            with pytest.raises(ManagerError, match="swtpm failed to start"):
                mgr._start_tpm()

        ok_proc = MagicMock()
        ok_proc.poll.return_value = None
        ok_proc.stderr = io.BytesIO(b"")
        with (
            patch("app.vm.subprocess.Popen", return_value=ok_proc),
            patch("app.vm.STATE_DIR", tmp_path / "state"),
            patch("app.vm.time.sleep"),
        ):
            mgr._start_tpm()
        assert mgr._tpm_process is ok_proc

    def test_generate_cloud_init_writes_expected_content(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.cfg.cloud_init_enabled = True
        mgr.cfg.ssh_pubkey = "ssh-rsa AAAATEST"
        mgr.cfg.filesystems = [FilesystemConfig(source=tmp_path, target="data", driver="virtiofs")]
        user_data = tmp_path / "user-data.yaml"
        user_data.write_text("#cloud-config\nhostname: demo\n", encoding="utf-8")
        mgr.cfg.cloud_init_user_data_path = user_data

        captured = {}

        def _run_geniso(cmd):
            captured["cmd"] = cmd
            captured["meta"] = Path(cmd[-3]).read_text(encoding="utf-8")
            captured["user"] = Path(cmd[-2]).read_text(encoding="utf-8")
            captured["vendor"] = Path(cmd[-1]).read_text(encoding="utf-8")

        with (
            patch("app.vm.IMAGES_DIR", tmp_path / "images"),
            patch("app.vm.run", side_effect=_run_geniso),
            patch("app.vm.hash_password", return_value="HASH"),
        ):
            mgr._generate_cloud_init()

        assert captured["cmd"][0] == "genisoimage"
        assert "iid-test-vm" in captured["meta"]
        assert "hostname: demo" in captured["user"]
        assert "ssh_authorized_keys" in captured["vendor"]
        assert "virtiofs" in captured["vendor"]


class TestDomainHelpers:
    def test_domain_exists_without_conn_raises(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)
        mgr.conn = None
        with pytest.raises(ManagerError, match="connection not established"):
            mgr._domain_exists()

    def test_define_domain_paths(self, default_vm_config, tmp_path):
        mgr = _make_mgr(default_vm_config, tmp_path)

        with patch.object(mgr, "_domain_exists", return_value=True):
            mgr.conn.lookupByName.return_value = "domain-object"
            mgr._define_domain()
        assert mgr.domain == "domain-object"

        mgr2 = _make_mgr(default_vm_config, tmp_path)
        with (
            patch.object(mgr2, "_domain_exists", return_value=False),
            patch.object(mgr2, "_render_domain_xml", return_value="<domain/>"),
        ):
            mgr2.conn.defineXML.return_value = None
            with pytest.raises(ManagerError, match="Failed to define libvirt domain"):
                mgr2._define_domain()
