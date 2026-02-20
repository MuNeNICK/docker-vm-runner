"""Tests for app.__main__ entrypoint."""

from __future__ import annotations

import runpy
from unittest.mock import patch

import pytest


def test_module_entrypoint_exits_with_cli_status():
    with patch("app.cli.main", return_value=7) as mock_main:
        with pytest.raises(SystemExit) as exc:
            runpy.run_module("app.__main__", run_name="__main__")
    assert exc.value.code == 7
    mock_main.assert_called_once_with()
