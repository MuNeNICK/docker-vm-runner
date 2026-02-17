"""Tests for app.status module."""

from __future__ import annotations

from unittest.mock import patch

from app.status import StatusBroadcaster


class TestStatusBroadcaster:
    def test_update_writes_to_file(self, tmp_path):
        status_file = tmp_path / "vm-status.txt"
        with patch("app.status.STATUS_FILE", status_file):
            sb = StatusBroadcaster()
            sb.update("Starting...")
            sb.update("Downloading image...")
            content = status_file.read_text()
            assert "Starting..." in content
            assert "Downloading image..." in content

    def test_ready_removes_file(self, tmp_path):
        status_file = tmp_path / "vm-status.txt"
        status_file.write_text("test")
        with patch("app.status.STATUS_FILE", status_file):
            sb = StatusBroadcaster()
            sb.ready()
            assert not status_file.exists()

    def test_update_after_ready_is_noop(self, tmp_path):
        status_file = tmp_path / "vm-status.txt"
        with patch("app.status.STATUS_FILE", status_file):
            sb = StatusBroadcaster()
            sb.ready()
            sb.update("Should not appear")
            assert not status_file.exists()

    def test_ready_missing_file_is_safe(self, tmp_path):
        status_file = tmp_path / "nonexistent.txt"
        with patch("app.status.STATUS_FILE", status_file):
            sb = StatusBroadcaster()
            sb.ready()  # should not raise
