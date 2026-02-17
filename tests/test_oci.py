"""Tests for OCI containerDisk support (is_oci_reference, pull_oci_disk, _resolve_boot_from OCI branch)."""

from __future__ import annotations

import json
import subprocess
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from app.exceptions import ManagerError
from app.utils import is_oci_reference, pull_oci_disk


class TestIsOciReference:
    @pytest.mark.parametrize(
        "ref",
        [
            "docker.io/kubevirt/fedora-cloud-container-disk-demo:latest",
            "ghcr.io/munenick/my-vm-disk:v1",
            "registry.example.com/images/vm:1.0",
            "quay.io/libvirt/alpine:edge",
            "localhost:5000/myimage:latest",
        ],
    )
    def test_valid_oci_references(self, ref):
        assert is_oci_reference(ref) is True

    @pytest.mark.parametrize(
        "ref",
        [
            "https://example.com/image.qcow2",
            "http://example.com/image.qcow2",
            "/local/path/to/image.qcow2",
            "/dev/sda",
            "ubuntu",
            "my-image",
            "",
        ],
    )
    def test_non_oci_references(self, ref):
        assert is_oci_reference(ref) is False

    def test_bare_library_image_without_dot_or_colon(self):
        # "library/ubuntu" — first component has no dot or colon → False
        assert is_oci_reference("library/ubuntu") is False

    def test_first_component_with_port(self):
        assert is_oci_reference("localhost:5000/image") is True

    def test_first_component_with_dot(self):
        assert is_oci_reference("registry.local/image") is True


class TestPullOciDisk:
    def test_skopeo_not_found_raises(self, tmp_path):
        with patch("app.utils.subprocess.run", side_effect=FileNotFoundError):
            with pytest.raises(ManagerError, match="skopeo is not installed"):
                pull_oci_disk("docker.io/test/image:latest", tmp_path)

    def test_skopeo_inspect_failure_raises(self, tmp_path):
        exc = subprocess.CalledProcessError(1, "skopeo", stderr="auth required")
        with patch("app.utils.subprocess.run", side_effect=exc):
            with pytest.raises(ManagerError, match="skopeo inspect failed"):
                pull_oci_disk("docker.io/test/image:latest", tmp_path)

    def test_cache_hit_returns_existing(self, tmp_path):
        """When a sentinel and disk file exist, return immediately without calling skopeo copy."""
        digest = "sha256:abcdef1234567890"
        digest_key = digest.replace(":", "-")[:19]  # "sha256-abcdef123456"
        safe_name = "image_latest"
        sentinel = tmp_path / f"{digest_key}-{safe_name}.done"
        disk_dir = tmp_path / f"{digest_key}-{safe_name}"
        disk_dir.mkdir()
        disk_file = disk_dir / "disk.qcow2"
        disk_file.write_bytes(b"\x00" * 1024)
        sentinel.write_text(digest)

        inspect_result = MagicMock()
        inspect_result.stdout = digest + "\n"
        inspect_result.returncode = 0

        with patch("app.utils.subprocess.run", return_value=inspect_result) as mock_run:
            result = pull_oci_disk("docker.io/test/image:latest", tmp_path)
            assert result == disk_file
            # Only the inspect call should happen — no copy
            assert mock_run.call_count == 1

    def test_no_disk_found_raises(self, tmp_path):
        """If the OCI image contains no extractable disk, raise ManagerError."""
        digest = "sha256:deadbeef12345678"

        inspect_result = MagicMock()
        inspect_result.stdout = digest + "\n"
        inspect_result.returncode = 0

        def _fake_run(cmd, **kwargs):
            if "inspect" in cmd:
                return inspect_result
            if "copy" in cmd:
                # Create a minimal OCI layout with an empty layer
                oci_dir = None
                for arg in cmd:
                    if arg.startswith("oci:"):
                        oci_dir = Path(arg.split(":", 1)[1].split(":")[0])
                        break
                if oci_dir:
                    oci_dir.mkdir(parents=True, exist_ok=True)
                    blobs = oci_dir / "blobs" / "sha256"
                    blobs.mkdir(parents=True)

                    # Empty layer (not a valid tar)
                    layer_hash = "aaa111"
                    (blobs / layer_hash).write_bytes(b"not a tar")

                    manifest = {
                        "layers": [
                            {"digest": f"sha256:{layer_hash}", "mediaType": "application/vnd.oci.image.layer.v1.tar"}
                        ]
                    }
                    manifest_json = json.dumps(manifest).encode()
                    import hashlib

                    m_hash = hashlib.sha256(manifest_json).hexdigest()
                    (blobs / m_hash).write_bytes(manifest_json)

                    index = {"manifests": [{"digest": f"sha256:{m_hash}"}]}
                    (oci_dir / "index.json").write_text(json.dumps(index))

                return MagicMock(returncode=0)
            return MagicMock(returncode=0)

        with patch("app.utils.subprocess.run", side_effect=_fake_run):
            with pytest.raises(ManagerError, match="No disk image found"):
                pull_oci_disk("docker.io/test/empty:latest", tmp_path)


class TestResolveBootFromOci:
    """Test _resolve_boot_from() OCI branch integration."""

    def test_oci_reference_calls_pull_oci_disk(self, default_vm_config, tmp_path):
        from app.vm import VMManager

        default_vm_config.boot_from = "docker.io/kubevirt/fedora-cloud:latest"

        fake_disk = tmp_path / "disk.qcow2"
        fake_disk.write_bytes(b"\x00" * 1024)

        with patch.object(VMManager, "__init__", lambda self, *a, **kw: None):
            mgr = VMManager.__new__(VMManager)
            mgr.cfg = default_vm_config
            mgr.base_image = tmp_path / "base.qcow2"
            mgr.work_image = tmp_path / "disk.qcow2"
            mgr.vm_dir = tmp_path
            mgr.boot_iso = None

            with (
                patch("app.vm.pull_oci_disk", return_value=fake_disk) as mock_pull,
                patch("app.vm.ensure_directory"),
                patch.object(mgr, "_post_process_image"),
            ):
                mgr._resolve_boot_from()

                mock_pull.assert_called_once_with(
                    "docker.io/kubevirt/fedora-cloud:latest",
                    Path("/var/lib/docker-vm-runner/oci-disks"),
                )
                # Should set base_image to the returned disk path
                assert mgr.base_image == fake_disk

    def test_url_still_works(self, default_vm_config, tmp_path):
        """Ensure HTTP URLs are still handled by the existing code path."""
        from app.vm import VMManager

        default_vm_config.boot_from = "https://example.com/image.qcow2"

        with patch.object(VMManager, "__init__", lambda self, *a, **kw: None):
            mgr = VMManager.__new__(VMManager)
            mgr.cfg = default_vm_config
            mgr.base_image = tmp_path / "base.qcow2"
            mgr.work_image = tmp_path / "disk.qcow2"
            mgr.vm_dir = tmp_path
            mgr.boot_iso = None

            with (
                patch("app.vm.ensure_directory"),
                patch("app.vm.download_file_with_retry"),
                patch("app.vm.is_oci_reference", return_value=False),
                patch.object(mgr, "_post_process_image"),
            ):
                mgr._resolve_boot_from()
                # Should not set boot_iso for non-ISO URLs
                assert mgr.boot_iso is None

    def test_local_path_still_works(self, default_vm_config, tmp_path):
        """Ensure local paths are still handled by the existing code path."""
        from app.vm import VMManager

        local_disk = tmp_path / "local.qcow2"
        local_disk.write_bytes(b"\x00" * 1024)
        default_vm_config.boot_from = str(local_disk)

        with patch.object(VMManager, "__init__", lambda self, *a, **kw: None):
            mgr = VMManager.__new__(VMManager)
            mgr.cfg = default_vm_config
            mgr.base_image = tmp_path / "base.qcow2"
            mgr.work_image = tmp_path / "disk.qcow2"
            mgr.vm_dir = tmp_path
            mgr.boot_iso = None

            with patch.object(mgr, "_post_process_image"):
                mgr._resolve_boot_from()
                assert mgr.base_image == local_disk
