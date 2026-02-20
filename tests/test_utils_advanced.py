"""Advanced tests for app.utils."""

from __future__ import annotations

import bz2
import gzip
import io
import lzma
import subprocess
import tarfile
import zipfile
from types import SimpleNamespace
from unittest.mock import MagicMock, patch
from urllib.error import HTTPError, URLError

import pytest

from app.exceptions import ManagerError
from app.utils import (
    check_disk_space,
    check_filesystem_compatibility,
    detect_filesystem,
    detect_host_mtu,
    detect_image_format,
    disable_cow,
    download_file,
    download_file_with_retry,
    extract_compressed,
    get_available_disk_space,
    get_available_memory,
    get_cpu_flags,
    get_cpu_vendor,
    get_host_info,
    has_controlling_tty,
    kvm_available,
    parse_resource_size,
    run,
    wait_for_path,
)


class _FakeResponse:
    def __init__(self, payload: bytes, content_length: bool = True):
        self._buf = io.BytesIO(payload)
        self.headers = {"Content-Length": str(len(payload))} if content_length else {}

    def read(self, n: int) -> bytes:
        return self._buf.read(n)


class TestDownload:
    def test_download_file_success(self, tmp_path):
        destination = tmp_path / "image.qcow2"
        payload = b"hello-world" * 1024
        with patch("app.utils.urlopen", return_value=_FakeResponse(payload)):
            download_file("https://example.com/img.qcow2", destination)
        assert destination.read_bytes() == payload

    def test_download_file_http_error(self, tmp_path):
        destination = tmp_path / "image.qcow2"
        err = HTTPError("https://example.com/img.qcow2", 404, "Not Found", hdrs=None, fp=None)
        with patch("app.utils.urlopen", side_effect=err):
            with pytest.raises(ManagerError, match="HTTP error downloading"):
                download_file("https://example.com/img.qcow2", destination)

    def test_download_file_url_error(self, tmp_path):
        destination = tmp_path / "image.qcow2"
        with patch("app.utils.urlopen", side_effect=URLError("boom")):
            with pytest.raises(ManagerError, match="Failed to download"):
                download_file("https://example.com/img.qcow2", destination)

    def test_download_with_retry_retries_once(self, tmp_path):
        destination = tmp_path / "image.qcow2"
        with (
            patch("app.utils.download_file", side_effect=[ManagerError("fail"), None]) as mock_download,
            patch("app.utils.time.sleep") as mock_sleep,
        ):
            download_file_with_retry("https://example.com/img.qcow2", destination, retries=2)
        assert mock_download.call_count == 2
        mock_sleep.assert_called_once()


class TestPlatformHelpers:
    def test_kvm_available_true(self):
        with (
            patch("app.utils.Path.exists", return_value=True),
            patch("app.utils.os.open", return_value=3),
            patch("app.utils.os.close") as mock_close,
        ):
            assert kvm_available() is True
        mock_close.assert_called_once_with(3)

    def test_kvm_available_open_error(self):
        with (
            patch("app.utils.Path.exists", return_value=True),
            patch("app.utils.os.open", side_effect=OSError),
        ):
            assert kvm_available() is False

    def test_has_controlling_tty_false(self, monkeypatch):
        monkeypatch.setattr("app.utils.sys.stdin", SimpleNamespace(isatty=lambda: True))
        monkeypatch.setattr("app.utils.sys.stdout", SimpleNamespace(isatty=lambda: False))
        assert has_controlling_tty() is False

    def test_wait_for_path_true(self, tmp_path):
        p = tmp_path / "ready"
        p.write_text("ok", encoding="utf-8")
        assert wait_for_path(p, timeout=0.1, interval=0.01) is True

    def test_wait_for_path_false(self, tmp_path):
        p = tmp_path / "missing"
        assert wait_for_path(p, timeout=0.01, interval=0.0) is False


class TestDetectionAndResources:
    def test_detect_host_mtu(self):
        route = subprocess.CompletedProcess(
            args=["ip"],
            returncode=0,
            stdout="default via 192.0.2.1 dev eth9 proto dhcp\n",
            stderr="",
        )
        fake_path = MagicMock()
        fake_path.exists.return_value = True
        fake_path.read_text.return_value = "9000\n"
        with (
            patch("app.utils.subprocess.run", return_value=route),
            patch("app.utils.Path", return_value=fake_path),
        ):
            assert detect_host_mtu() == 9000

    def test_detect_filesystem_and_format(self, tmp_path):
        fs = subprocess.CompletedProcess(args=["stat"], returncode=0, stdout="overlayfs\n", stderr="")
        img = subprocess.CompletedProcess(args=["qemu-img"], returncode=0, stdout='{"format":"qcow2"}', stderr="")
        with patch("app.utils.subprocess.run", side_effect=[fs, img]):
            assert detect_filesystem(tmp_path) == "overlayfs"
            assert detect_image_format(tmp_path / "disk.qcow2") == "qcow2"

    def test_detect_image_format_unknown_on_error(self, tmp_path):
        with patch("app.utils.subprocess.run", side_effect=FileNotFoundError):
            assert detect_image_format(tmp_path / "disk.qcow2") == "unknown"

    def test_get_host_info(self):
        cpuinfo = io.StringIO("model name\t: Unit Test CPU\n")
        meminfo = io.StringIO("MemTotal:       1000 kB\nMemAvailable:   500 kB\n")

        def _open(path, *args, **kwargs):
            if path == "/proc/cpuinfo":
                return cpuinfo
            if path == "/proc/meminfo":
                return meminfo
            raise FileNotFoundError(path)

        with (
            patch("builtins.open", side_effect=_open),
            patch("app.utils.os.cpu_count", return_value=8),
            patch("app.utils.os.uname", return_value=SimpleNamespace(release="6.0-test")),
        ):
            info = get_host_info()
        assert info["cpu_model"] == "Unit Test CPU"
        assert info["cpu_count"] == 8
        assert info["mem_total"] == 1000 * 1024
        assert info["mem_available"] == 500 * 1024
        assert info["kernel"] == "6.0-test"

    def test_get_available_disk_space_oserror(self, tmp_path):
        with patch("app.utils.os.statvfs", side_effect=OSError):
            assert get_available_disk_space(tmp_path) == 0

    def test_get_available_memory_and_cpu_helpers(self):
        with patch("builtins.open", return_value=io.StringIO("MemAvailable:   2048 kB\n")):
            assert get_available_memory() == 2048 * 1024

    def test_get_cpu_vendor_and_flags(self):
        def _open(*_args, **_kwargs):
            return io.StringIO("vendor_id\t: GenuineIntel\nflags\t\t: sse avic\n")

        with patch("builtins.open", side_effect=_open):
            assert get_cpu_vendor() == "intel"
            assert "avic" in get_cpu_flags()

    def test_parse_resource_size(self):
        with (
            patch("app.utils.get_available_memory", return_value=8 * 1024**3),
            patch("app.utils.get_cpu_count", return_value=8),
        ):
            assert parse_resource_size("max", "memory") >= 512
            assert parse_resource_size("half", "cpus") == 4
            assert parse_resource_size("max", "disk") == 0
        with pytest.raises(ManagerError, match="Invalid memory value"):
            parse_resource_size("1234", "memory")


class TestExtractionAndFilesystemChecks:
    def test_extract_compressed_gz_xz_bz2(self, tmp_path):
        payload = b"payload"

        gz = tmp_path / "disk.raw.gz"
        with gzip.open(gz, "wb") as f:
            f.write(payload)
        assert extract_compressed(gz, tmp_path).read_bytes() == payload

        xz = tmp_path / "disk.raw.xz"
        with lzma.open(xz, "wb") as f:
            f.write(payload)
        assert extract_compressed(xz, tmp_path).read_bytes() == payload

        bz = tmp_path / "disk.raw.bz2"
        with bz2.open(bz, "wb") as f:
            f.write(payload)
        assert extract_compressed(bz, tmp_path).read_bytes() == payload

    def test_extract_compressed_zip_and_tar(self, tmp_path):
        zip_path = tmp_path / "disk.zip"
        with zipfile.ZipFile(zip_path, "w") as zf:
            zf.writestr("small.txt", b"a")
            zf.writestr("disk/big.qcow2", b"x" * 10)
        extracted_zip = extract_compressed(zip_path, tmp_path)
        assert extracted_zip.name.endswith("big.qcow2")

        tar_path = tmp_path / "disk.tar"
        with tarfile.open(tar_path, "w") as tf:
            data = b"y" * 20
            info = tarfile.TarInfo(name="disk/big.raw")
            info.size = len(data)
            tf.addfile(info, io.BytesIO(data))
        extracted_tar = extract_compressed(tar_path, tmp_path)
        assert extracted_tar.name.endswith("big.raw")

    def test_extract_compressed_unsupported_raises(self, tmp_path):
        unknown = tmp_path / "disk.unknown"
        unknown.write_bytes(b"x")
        with pytest.raises(ManagerError, match="Unsupported compressed format"):
            extract_compressed(unknown, tmp_path)

    def test_disable_cow_and_checks(self, tmp_path):
        with patch("app.utils.subprocess.run") as mock_run:
            mock_run.side_effect = [
                subprocess.CompletedProcess(args=["chattr"], returncode=0, stdout="", stderr=""),
                subprocess.CompletedProcess(args=["lsattr"], returncode=0, stdout="----C---- test\n", stderr=""),
            ]
            disable_cow(tmp_path)

    def test_check_filesystem_compatibility_and_disk_space(self, tmp_path):
        with (
            patch("app.utils.detect_filesystem", return_value="btrfs"),
            patch("app.utils.disable_cow") as mock_disable,
        ):
            check_filesystem_compatibility(tmp_path)
        mock_disable.assert_called_once_with(tmp_path)

        with patch("app.utils.get_available_disk_space", return_value=5 * 1024**3), patch("app.utils.log") as mock_log:
            check_disk_space(tmp_path, required_bytes=8 * 1024**3)
            check_disk_space(tmp_path, required_bytes=3 * 1024**3)
        assert any(call.args[0] == "ERROR" for call in mock_log.mock_calls)
        assert any(call.args[0] == "WARN" for call in mock_log.mock_calls)


class TestRunWrapper:
    def test_run_passes_text_mode(self):
        cp = subprocess.CompletedProcess(args=["echo"], returncode=0, stdout="", stderr="")
        with patch("app.utils.subprocess.run", return_value=cp) as mock_run:
            result = run(["echo", "ok"])
        assert result.returncode == 0
        assert mock_run.call_args.kwargs["text"] is True
