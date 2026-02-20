"""Tests for app.cli module."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

from app import cli
from app.exceptions import ManagerError
from app.models import PortForward
from app.runtime import RuntimeInfo


class TestListDistros:
    def test_missing_config_logs_error(self, tmp_path):
        missing = tmp_path / "nope.yaml"
        with patch("app.cli.log") as mock_log:
            cli.list_distros(config_path=missing)
        mock_log.assert_called_once()
        level, message = mock_log.call_args[0]
        assert level == "ERROR"
        assert "Distribution config missing" in message

    def test_arch_filter_with_alias(self, tmp_path, capsys):
        config = tmp_path / "distros.yaml"
        config.write_text(
            "\n".join(
                [
                    "distributions:",
                    "  ubuntu:",
                    "    name: Ubuntu",
                    "    arch: x86_64",
                    "    user: ubuntu",
                    "  fedora-arm:",
                    "    name: Fedora ARM",
                    "    arch: aarch64",
                    "    user: fedora",
                ]
            )
            + "\n"
        )
        with patch("app.cli.log") as mock_log:
            cli.list_distros(config_path=config, arch_filter="arm64")
        out = capsys.readouterr().out
        assert "fedora-arm" in out
        assert "ubuntu" not in out
        mock_log.assert_called_once_with("INFO", "Showing distributions for arch: aarch64")

    def test_empty_distro_map_logs_warning(self, tmp_path):
        config = tmp_path / "distros.yaml"
        config.write_text("distributions: {}\n")
        with patch("app.cli.log") as mock_log:
            cli.list_distros(config_path=config)
        mock_log.assert_called_once_with("WARN", "No distributions found")


class TestShowConfig:
    def test_masks_sensitive_fields(self, default_vm_config, capsys):
        cfg = default_vm_config
        cfg.password = "secret1"
        cfg.redfish_password = "secret2"
        cli.show_config(cfg)
        out = capsys.readouterr().out
        assert "password: ********" in out
        assert "redfish_password: ********" in out
        assert "secret1" not in out
        assert "secret2" not in out


class TestPrintStartupBanner:
    def test_includes_publish_and_access_lines(self, default_vm_config):
        cfg = default_vm_config
        cfg.redfish_enabled = True
        cfg.novnc_enabled = True
        cfg.port_forwards = [PortForward(8080, 80)]
        with patch("app.cli._print_block") as mock_print_block:
            cli.print_startup_banner(cfg)
        mock_print_block.assert_called_once()
        title, lines = mock_print_block.call_args[0][0], mock_print_block.call_args[0][1]
        assert title == "Access"
        assert any("SSH:     ssh -p 2222" in line for line in lines)
        assert any("Console: https://localhost:6080/vnc.html" in line for line in lines)
        assert any("Redfish: https://localhost:8443/" in line for line in lines)
        assert any("Ports:   8080->80" in line for line in lines)
        assert any("Publish: " in line for line in lines)


class TestMain:
    def test_list_distros_branch(self):
        with patch("app.cli.list_distros") as mock_list:
            rc = cli.main(["--list-distros", "arm64"])
        assert rc == 0
        mock_list.assert_called_once_with(arch_filter="arm64")

    def test_parse_env_error_returns_1(self):
        with patch("app.cli.parse_env", side_effect=ManagerError("bad config")), patch("app.cli.log") as mock_log:
            rc = cli.main([])
        assert rc == 1
        mock_log.assert_called_with("ERROR", "bad config")

    def test_show_config_branch(self, default_vm_config):
        with (
            patch("app.cli.parse_env", return_value=default_vm_config),
            patch("app.cli.show_config") as mock_show,
        ):
            rc = cli.main(["--show-config"])
        assert rc == 0
        mock_show.assert_called_once_with(default_vm_config)

    def test_show_xml_branch(self, default_vm_config, capsys):
        with (
            patch("app.cli.parse_env", return_value=default_vm_config),
            patch("app.cli.kvm_available", return_value=False),
            patch("app.cli.VMManager._render_domain_xml", return_value="<domain/>"),
        ):
            rc = cli.main(["--show-xml"])
        assert rc == 0
        assert "<domain/>" in capsys.readouterr().out

    def test_dry_run_branch(self, default_vm_config):
        runtime = RuntimeInfo(engine="docker", rootless=False, privileged=True)
        with (
            patch("app.cli.parse_env", return_value=default_vm_config),
            patch("app.runtime.detect_runtime", return_value=runtime),
            patch("app.cli.kvm_available", return_value=True),
            patch("app.cli.show_config") as mock_show,
            patch("app.cli.print_startup_banner") as mock_banner,
        ):
            rc = cli.main(["--dry-run"])
        assert rc == 0
        mock_show.assert_called_once_with(default_vm_config)
        mock_banner.assert_called_once_with(default_vm_config)

    def test_normal_run_no_console_success(self, default_vm_config):
        cfg = default_vm_config
        cfg.persist = True
        fake_service = MagicMock()
        fake_service.runtime = RuntimeInfo(engine="docker", rootless=False, privileged=True)
        fake_vm = MagicMock()

        with (
            patch("app.cli.parse_env", return_value=cfg),
            patch("app.cli.ServiceManager", return_value=fake_service),
            patch("app.cli.VMManager", return_value=fake_vm),
            patch("app.cli.print_host_info"),
            patch("app.cli.print_vm_summary"),
            patch("app.cli.print_startup_banner"),
            patch("app.cli.ensure_directory"),
        ):
            rc = cli.main(["--no-console"])

        assert rc == 0
        fake_service.start.assert_called_once()
        fake_vm.connect.assert_called_once()
        fake_vm.prepare.assert_called_once()
        fake_vm.start.assert_called_once()
        fake_vm.wait_for_guest_ready.assert_called_once_with(timeout=120)
        fake_vm.wait_until_stopped.assert_called_once()
        fake_vm._mark_installed.assert_called_once()
        fake_vm.cleanup.assert_called_once()
        fake_vm.close.assert_called_once()
        fake_service.stop.assert_called_once()

    def test_unexpected_error_returns_1_and_stops(self, default_vm_config):
        fake_service = MagicMock()
        fake_service.runtime = RuntimeInfo(engine="docker", rootless=False, privileged=True)
        fake_vm = MagicMock()
        fake_vm.prepare.side_effect = RuntimeError("boom")

        with (
            patch("app.cli.parse_env", return_value=default_vm_config),
            patch("app.cli.ServiceManager", return_value=fake_service),
            patch("app.cli.VMManager", return_value=fake_vm),
            patch("app.cli.print_host_info"),
            patch("app.cli.print_vm_summary"),
            patch("app.cli.ensure_directory"),
            patch("traceback.print_exc"),
        ):
            rc = cli.main(["--no-console"])
        assert rc == 1
        fake_vm.cleanup.assert_called_once()
        fake_vm.close.assert_called_once()
        fake_service.stop.assert_called_once()


class TestRunConsole:
    def test_keyboard_interrupt_sends_sigint(self):
        proc = MagicMock()
        proc.wait.side_effect = [KeyboardInterrupt(), 0]
        with (
            patch("app.cli.subprocess.Popen", return_value=proc),
            patch("app.cli.signal.signal", side_effect=lambda *_args, **_kwargs: None),
            patch("app.cli.log"),
        ):
            rc = cli.run_console("vm1")
        assert rc == 0
        proc.send_signal.assert_called_once_with(cli.signal.SIGINT)
