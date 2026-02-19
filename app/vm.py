"""VM lifecycle management for Docker-VM-Runner."""

from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import signal
import subprocess
import tempfile
import textwrap
import time
from pathlib import Path
from typing import Dict, Optional
from urllib.parse import urlparse
from xml.etree.ElementTree import Element, SubElement, fromstring, register_namespace, tostring

try:
    import yaml  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit("PyYAML is required but not installed") from exc

try:
    import libvirt  # type: ignore
except ImportError as exc:  # pragma: no cover
    raise SystemExit(f"libvirt python bindings not available: {exc}")

from app.constants import (
    BASE_IMAGES_DIR,
    BOOT_ISO_CACHE_DIR,
    COMPRESSED_EXTENSIONS,
    CONVERTIBLE_FORMATS,
    DISK_CONTROLLERS,
    IMAGES_DIR,
    INSTALLED_MARKER_NAME,
    LIBVIRT_URI,
    OCI_DISK_CACHE_DIR,
    STATE_DIR,
    SUPPORTED_ARCHES,
    VM_IMAGES_DIR,
)
from app.exceptions import ManagerError
from app.models import VMConfig
from app.network import render_network_xml
from app.utils import (
    check_disk_space,
    check_filesystem_compatibility,
    convert_disk_image,
    detect_filesystem,
    detect_image_format,
    download_file_with_retry,
    ensure_directory,
    extract_compressed,
    get_cpu_flags,
    get_cpu_vendor,
    get_env_bool,
    has_ipv6,
    hash_password,
    is_oci_reference,
    kvm_available,
    log,
    parse_size_to_bytes,
    pull_oci_disk,
    run,
    sanitize_mount_target,
)


class VMManager:
    def __init__(self, vm_config: VMConfig, service_manager) -> None:
        self.cfg = vm_config
        self.service_manager = service_manager
        self.conn: Optional[libvirt.virConnect] = None
        self.domain: Optional[libvirt.virDomain] = None
        self._kvm_available = kvm_available()
        self._effective_cpu_model = self.cfg.cpu_model
        self._arch_profile = SUPPORTED_ARCHES[self.cfg.arch]
        self._firmware_loader_path: Optional[Path] = None
        self._firmware_vars_path: Optional[Path] = None
        ensure_directory(IMAGES_DIR)
        ensure_directory(BASE_IMAGES_DIR)
        ensure_directory(VM_IMAGES_DIR)
        self.vm_dir = VM_IMAGES_DIR / self.cfg.vm_name
        if self.cfg.persist:
            ensure_directory(self.vm_dir)
        else:
            if self.vm_dir.exists():
                shutil.rmtree(self.vm_dir, ignore_errors=True)
            ensure_directory(self.vm_dir)
        self.base_image = BASE_IMAGES_DIR / f"{self.cfg.distro}.{self.cfg.image_format}"
        self.work_image = self.vm_dir / f"disk.{self.cfg.image_format}"
        self.boot_iso: Optional[Path] = None
        self.seed_iso = self.vm_dir / "seed.iso" if self.cfg.cloud_init_enabled else None
        self._disk_reused = False
        self._network_macs: Dict[int, str] = {}
        self._ipxe_rom_path: Optional[Path] = None
        if self.cfg.ipxe_enabled and self.cfg.ipxe_rom_path:
            self._ipxe_rom_path = Path(self.cfg.ipxe_rom_path)
        self._tpm_process: Optional[subprocess.Popen] = None

    def connect(self) -> None:
        self.conn = libvirt.open(LIBVIRT_URI)
        if self.conn is None:
            raise ManagerError(f"Failed to open libvirt connection to {LIBVIRT_URI}")

    def close(self) -> None:
        if self.conn is not None:
            self.conn.close()
            self.conn = None

    def prepare(self) -> None:
        if not self._kvm_available:
            log("WARN", "=" * 60)
            log("WARN", "  /dev/kvm not found!")
            log("WARN", "  Running in software emulation mode (TCG).")
            log("WARN", "  Performance will be 10-50x slower.")
            log("WARN", "  Fix: add --device /dev/kvm:/dev/kvm")
            log("WARN", "=" * 60)
            if get_env_bool("REQUIRE_KVM", False):
                raise ManagerError(
                    "REQUIRE_KVM=1 is set but /dev/kvm is not available. "
                    "Add --device /dev/kvm:/dev/kvm or unset REQUIRE_KVM."
                )
            cpu_lower = self.cfg.cpu_model.lower()
            if cpu_lower in {"host", "host-passthrough"}:
                fallback = self._arch_profile.get("tcg_fallback")
                if not fallback:
                    raise ManagerError(f"CPU_MODEL={self.cfg.cpu_model} requires KVM for architecture {self.cfg.arch}.")
                self._effective_cpu_model = fallback
                log(
                    "WARN",
                    f"CPU_MODEL=host is not compatible with TCG on {self.cfg.arch}. Using {fallback} instead.",
                )
        self._resolve_boot_from()

        if not self.cfg.blank_work_disk:
            self._ensure_base_image()

        self._prepare_work_image()
        # Smart ISO skip: if disk was reused from a prior install, skip ISO boot
        if self.boot_iso and self._disk_reused and self._is_installed() and not self.cfg.force_iso:
            log("INFO", "Persistent disk with prior install found; skipping ISO boot (set FORCE_ISO=1 to override)")
            self.boot_iso = None
            if "cdrom" in self.cfg.boot_order:
                self.cfg.boot_order = [d for d in self.cfg.boot_order if d != "cdrom"]
            if "hd" not in self.cfg.boot_order:
                self.cfg.boot_order = ["hd"] + self.cfg.boot_order

        if self.boot_iso and not self.boot_iso.exists():
            raise ManagerError(f"Boot ISO not found: {self.boot_iso}")

        self._extract_qemu_binary()

        self._prepare_firmware()
        self._start_tpm()

        self._generate_cloud_init()

        self._define_domain()

    def _resolve_boot_from(self) -> None:
        """Resolve BOOT_FROM: download if URL, pull if OCI, detect type (ISO vs disk image)."""
        boot_from = self.cfg.boot_from
        if not boot_from:
            return

        is_url = boot_from.startswith(("http://", "https://"))
        is_oci = not is_url and is_oci_reference(boot_from)

        if is_url:
            ensure_directory(BOOT_ISO_CACHE_DIR)
            digest = hashlib.sha256(boot_from.encode("utf-8")).hexdigest()[:12]
            parsed = urlparse(boot_from)
            filename = Path(parsed.path or "").name or "boot_from"
            safe_name = re.sub(r"[^A-Za-z0-9._-]", "_", filename) or "boot_from"
            destination = BOOT_ISO_CACHE_DIR / f"{digest}-{safe_name}"
            if destination.exists() and destination.stat().st_size > 0:
                log("INFO", f"Using cached download: {destination}")
            else:
                download_file_with_retry(
                    boot_from,
                    destination,
                    label="Downloading boot source",
                    retries=self.cfg.download_retries,
                )
            resolved = destination
        elif is_oci:
            ensure_directory(OCI_DISK_CACHE_DIR)
            resolved = pull_oci_disk(boot_from, OCI_DISK_CACHE_DIR)
        else:
            resolved = Path(boot_from)
            if not resolved.exists():
                raise ManagerError(f"BOOT_FROM path not found: {resolved}")

        # Detect type by extension (strip compression layers)
        check = resolved.name.lower()
        is_iso = check.endswith(".iso")

        if is_iso:
            self.boot_iso = resolved
        else:
            # Disk image (OVA, qcow2, vmdk, raw, etc.) — use as base image
            self.base_image = resolved
            self._post_process_image(resolved)

    def _ensure_base_image(self) -> None:
        if self.base_image.exists() and self.base_image.stat().st_size > 100 * 1024 * 1024:
            log("INFO", f"Using cached image: {self.base_image}")
            return
        if self.base_image.exists():
            size_mb = self.base_image.stat().st_size / (1024 * 1024)
            log(
                "WARN",
                f"Cached image too small ({size_mb:.1f} MiB < 100 MiB threshold); re-downloading {self.base_image}",
            )
            self.base_image.unlink()

        # Download to a temp name that preserves the URL extensions so that
        # _post_process_image can detect compressed/archive layers (e.g. .tar.xz)
        url_path = urlparse(self.cfg.image_url).path
        url_filename = Path(url_path).name or self.base_image.name
        download_path = self.base_image.parent / url_filename
        download_file_with_retry(
            self.cfg.image_url,
            download_path,
            label="Downloading base image",
            retries=self.cfg.download_retries,
        )
        self._post_process_image(download_path)

    def _post_process_image(self, image_path: Path) -> None:
        """Handle compressed/archive extraction and format conversion.

        After processing, the final disk image is placed at ``self.base_image``
        and ``self.work_image`` / ``self.cfg.image_format`` are updated.
        """
        # Compressed/archive extraction (may loop for layered formats like .tar.xz)
        current = image_path
        while current.suffix.lower() in COMPRESSED_EXTENSIONS:
            suffix = current.suffix.lower()
            log("INFO", f"Extracting compressed image ({suffix})...")
            extracted = extract_compressed(current, current.parent)
            if extracted != current:
                current.unlink(missing_ok=True)
            current = extracted
            log("SUCCESS", "Image extracted")

        # Detect actual disk format after all extraction layers
        actual_format = detect_image_format(current)

        # Convert foreign or raw formats to qcow2
        if actual_format in CONVERTIBLE_FORMATS or actual_format == "raw":
            converted = current.with_name(current.stem + ".converted.qcow2")
            convert_disk_image(current, converted)
            current.unlink(missing_ok=True)
            current = converted
            actual_format = "qcow2"

        # Place the final image at self.base_image and update paths
        if actual_format != self.cfg.image_format:
            self.cfg.image_format = actual_format
            self.base_image = self.base_image.with_suffix(f".{actual_format}")
            self.work_image = self.vm_dir / f"disk.{actual_format}"
        if current != self.base_image:
            self.base_image.parent.mkdir(parents=True, exist_ok=True)
            current.rename(self.base_image)

        # Clean up original download file if it still exists and differs
        if image_path != self.base_image and image_path.exists():
            image_path.unlink(missing_ok=True)

    def _prepare_work_image(self) -> None:
        # Filesystem compatibility check
        check_filesystem_compatibility(self.vm_dir)

        # Disk space check
        if self.cfg.disk_size and self.cfg.disk_size != "0":
            check_disk_space(self.vm_dir, parse_size_to_bytes(self.cfg.disk_size))

        self._disk_reused = False
        if self.cfg.persist and self.work_image.exists():
            size = self.work_image.stat().st_size
            if size > 100 * 1024 * 1024:
                log("INFO", f"Reusing persistent disk {self.work_image}")
                self._disk_reused = True
                # Expand disk if DISK_SIZE is larger than current virtual size
                if self.cfg.disk_size and self.cfg.disk_size != "0":
                    info = subprocess.run(
                        ["qemu-img", "info", "--output=json", str(self.work_image)],
                        capture_output=True,
                        text=True,
                    )
                    if info.returncode == 0:
                        current_vsize = json.loads(info.stdout).get("virtual-size", 0)
                        requested_bytes = parse_size_to_bytes(self.cfg.disk_size)
                        if requested_bytes > current_vsize:
                            log("INFO", f"Expanding disk from {current_vsize // (1024**3)}G to {self.cfg.disk_size}...")
                            run(["qemu-img", "resize", str(self.work_image), self.cfg.disk_size])
                            log("SUCCESS", f"Disk expanded to {self.cfg.disk_size}")
            else:
                size_mb = size / (1024 * 1024)
                log(
                    "WARN",
                    f"Existing disk too small ({size_mb:.1f} MiB < 100 MiB threshold); recreating {self.work_image}",
                )
                self.work_image.unlink(missing_ok=True)

        if not self._disk_reused:
            if self.cfg.blank_work_disk:
                log("INFO", f"Creating blank disk {self.work_image} ({self.cfg.disk_size})")
                create_cmd = [
                    "qemu-img",
                    "create",
                    "-f",
                    self.cfg.image_format,
                ]
                if self.cfg.disk_preallocate:
                    create_cmd.extend(["-o", "preallocation=falloc"])
                create_cmd.extend([str(self.work_image), self.cfg.disk_size])
                run(create_cmd)
            else:
                log("INFO", f"Creating working disk {self.work_image}")
                if self.base_image.suffix.lower() == ".iso":
                    raise ManagerError(
                        f"Base image is an ISO ({self.base_image}). "
                        f"Try: BOOT_FROM={self.base_image} (and optionally BLANK_DISK=1)"
                    )
                shutil.copy2(self.base_image, self.work_image)
                if self.cfg.disk_size and self.cfg.disk_size != "0":
                    # Only expand, never shrink
                    info = subprocess.run(
                        ["qemu-img", "info", "--output=json", str(self.work_image)],
                        capture_output=True,
                        text=True,
                    )
                    current_vsize = 0
                    if info.returncode == 0:
                        current_vsize = json.loads(info.stdout).get("virtual-size", 0)
                    requested_bytes = parse_size_to_bytes(self.cfg.disk_size)
                    if requested_bytes > current_vsize:
                        log("INFO", f"Resizing disk to {self.cfg.disk_size}...")
                        run(["qemu-img", "resize", str(self.work_image), self.cfg.disk_size])
                    elif current_vsize > requested_bytes:
                        cur_gb = current_vsize // (1024**3)
                        log("INFO", f"Base image already {cur_gb}G (>= {self.cfg.disk_size}); skip resize")
        else:
            log("INFO", f"Persistent disk retained at {self.work_image}")

        # Prepare extra disks (DISK2-6)
        self._prepare_extra_disks()

    def _prepare_extra_disks(self) -> None:
        """Create extra disk images for DISK2_SIZE through DISK6_SIZE."""
        for disk_cfg in self.cfg.extra_disks:
            disk_path = self.vm_dir / f"disk{disk_cfg.index}.{self.cfg.image_format}"
            if disk_path.exists() and self.cfg.persist:
                log("INFO", f"Reusing extra disk {disk_path}")
                continue
            log("INFO", f"Creating extra disk {disk_path} ({disk_cfg.size})")
            create_cmd = [
                "qemu-img",
                "create",
                "-f",
                self.cfg.image_format,
            ]
            if self.cfg.disk_preallocate:
                create_cmd.extend(["-o", "preallocation=falloc"])
            create_cmd.extend([str(disk_path), disk_cfg.size])
            run(create_cmd)

    def _is_installed(self) -> bool:
        return (self.vm_dir / INSTALLED_MARKER_NAME).exists()

    def _mark_installed(self) -> None:
        marker = self.vm_dir / INSTALLED_MARKER_NAME
        if not marker.exists():
            marker.write_text(f"Installed on {time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}\n")
            log("INFO", f"Marked VM as installed ({marker})")

    _QEMU_DEBS = {
        "x86_64": Path("/opt/qemu-x86.deb"),
        "aarch64": Path("/opt/qemu-arm.deb"),
        "ppc64": Path("/opt/qemu-ppc.deb"),
        "s390x": Path("/opt/qemu-s390x.deb"),
        "riscv64": Path("/opt/qemu-riscv.deb"),
    }
    _QEMU_EMULATORS = {
        "x86_64": "qemu-system-x86_64",
        "aarch64": "qemu-system-aarch64",
        "ppc64": "qemu-system-ppc64",
        "s390x": "qemu-system-s390x",
        "riscv64": "qemu-system-riscv64",
    }

    def _extract_qemu_binary(self) -> None:
        """Extract QEMU binary from the bundled .deb for the target architecture."""
        arch = self.cfg.arch
        deb = self._QEMU_DEBS.get(arch)
        if not deb or not deb.exists():
            return  # binaries already installed or unknown arch
        emulator = self._QEMU_EMULATORS.get(arch, "")
        if Path(f"/usr/bin/{emulator}").exists():
            return  # already extracted
        log("INFO", f"Extracting QEMU binaries for {arch}...")
        subprocess.run(["dpkg-deb", "-x", str(deb), "/"], check=True)
        log("SUCCESS", f"QEMU binaries for {arch} extracted.")

    @staticmethod
    def _extract_aavmf_deb() -> None:
        """Extract AAVMF firmware from the bundled .deb package on demand."""
        deb_path = Path("/opt/aavmf.deb")
        if not deb_path.exists():
            raise ManagerError(
                "AAVMF firmware .deb not found at /opt/aavmf.deb. "
                "Rebuild the container image or install qemu-efi-aarch64 manually."
            )
        log("INFO", "Extracting AAVMF firmware from .deb package...")
        with tempfile.TemporaryDirectory() as tmpdir:
            subprocess.run(
                ["dpkg-deb", "-x", str(deb_path), tmpdir],
                check=True,
            )
            src_dir = Path(tmpdir) / "usr" / "share" / "AAVMF"
            if not src_dir.is_dir():
                raise ManagerError(f"Expected AAVMF directory not found in .deb: {src_dir}")
            dst_dir = Path("/usr/share/AAVMF")
            ensure_directory(dst_dir)
            for f in src_dir.iterdir():
                shutil.copy2(f, dst_dir / f.name)
        log("SUCCESS", "AAVMF firmware extracted successfully.")

    def _prepare_firmware(self) -> None:
        arch = self.cfg.arch
        firmware_cfg = self._arch_profile.get("firmware")

        # x86_64 firmware depends on boot_mode
        if arch == "x86_64":
            if self.cfg.boot_mode == "legacy":
                return  # No firmware needed for legacy BIOS boot
            # UEFI or Secure Boot
            if not firmware_cfg or self.cfg.boot_mode not in firmware_cfg:
                raise ManagerError(f"Firmware configuration for boot_mode='{self.cfg.boot_mode}' not found for {arch}.")
            mode_cfg = firmware_cfg[self.cfg.boot_mode]
            loader_path = Path(mode_cfg["loader"])
            vars_template_path = Path(mode_cfg["vars_template"])

            if not loader_path.exists():
                raise ManagerError(f"OVMF firmware not found at {loader_path}. Ensure the 'ovmf' package is installed.")
            if not vars_template_path.exists():
                raise ManagerError(
                    f"OVMF variable template not found at {vars_template_path}. Ensure the 'ovmf' package is installed."
                )
        elif firmware_cfg:
            # aarch64 and other arches with firmware (flat dict with loader/vars_template)
            loader_path = Path(firmware_cfg["loader"])
            vars_template_path = Path(firmware_cfg["vars_template"])

            # Extract AAVMF from .deb on demand if firmware files are missing
            if not loader_path.exists() or not vars_template_path.exists():
                self._extract_aavmf_deb()

            if not loader_path.exists():
                raise ManagerError(f"Firmware loader not found at {loader_path} for arch {arch}.")
            if not vars_template_path.exists():
                raise ManagerError(f"Firmware variable template not found at {vars_template_path} for arch {arch}.")
        else:
            return  # No firmware for this arch

        firmware_dir = STATE_DIR / "firmware"
        ensure_directory(firmware_dir)
        vars_destination = firmware_dir / f"{self.cfg.vm_name}-vars.fd"
        if not vars_destination.exists():
            shutil.copy2(vars_template_path, vars_destination)

        self._firmware_loader_path = loader_path
        self._firmware_vars_path = vars_destination

    def _start_tpm(self) -> None:
        """Start swtpm software TPM emulator if TPM is enabled."""
        if not self.cfg.tpm_enabled:
            return
        tpm_dir = STATE_DIR / "tpm" / self.cfg.vm_name
        ensure_directory(tpm_dir)
        sock_path = tpm_dir / "swtpm-sock"

        log("INFO", "Starting software TPM (swtpm)...")
        cmd = [
            "swtpm",
            "socket",
            "--tpmstate",
            f"dir={tpm_dir}",
            "--ctrl",
            f"type=unixio,path={sock_path}",
            "--tpm2",
        ]
        try:
            proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        except FileNotFoundError:
            raise ManagerError("swtpm not found. Ensure swtpm and swtpm-tools are installed.")
        time.sleep(0.5)
        if proc.poll() is not None:
            stderr = proc.stderr.read().decode() if proc.stderr else ""
            raise ManagerError(f"swtpm failed to start: {stderr}")
        self._tpm_process = proc
        self._tpm_sock_path = sock_path
        log("SUCCESS", "Software TPM started")

    def _generate_cloud_init(self) -> None:
        if not self.cfg.cloud_init_enabled:
            return
        ensure_directory(IMAGES_DIR)
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            passwd_hash = hash_password(self.cfg.password)

            # Vendor cloud-config: written to vendor-data so it is processed
            # independently of user-data and immune to cloud-config merging.
            vendor_cfg: Dict[str, object] = {
                "packages": ["qemu-guest-agent"],
                "users": [
                    {
                        "name": self.cfg.login_user,
                        "lock_passwd": False,
                        "sudo": "ALL=(ALL) NOPASSWD:ALL",
                        "shell": "/bin/bash",
                        "passwd": passwd_hash,
                    }
                ],
                "chpasswd": {"expire": False},
                "ssh_pwauth": True,
                "write_files": [
                    # RHEL/Rocky/Alma blocklist guest-exec by default
                    {
                        "path": "/etc/sysconfig/qemu-ga",
                        "content": "# Managed by docker-vm-runner\nBLACKLIST_RPC=\n",
                    },
                    # Alpine: no udev so /dev/virtio-ports/ symlink is missing;
                    # auto-detect the correct vport device at boot
                    {
                        "path": "/etc/conf.d/qemu-guest-agent",
                        "content": (
                            "# Managed by docker-vm-runner\n"
                            "# Auto-detect virtio guest agent port\n"
                            "GA_PATH=\"$(find /dev -name 'vport*p1' 2>/dev/null | head -1)\"\n"
                        ),
                    },
                ],
                "runcmd": [
                    # SELinux: allow qemu-guest-agent unrestricted access
                    # (only affects virt_qemu_ga_t domain; system SELinux stays enforcing)
                    [
                        "sh",
                        "-c",
                        "command -v semanage >/dev/null 2>&1 && semanage permissive -a virt_qemu_ga_t || true",
                    ],
                    # systemd distros (Debian, RHEL, SUSE, Arch, Kali …)
                    [
                        "sh",
                        "-c",
                        "command -v systemctl >/dev/null 2>&1"
                        " && systemctl enable qemu-guest-agent"
                        " && systemctl restart qemu-guest-agent"
                        " || true",
                    ],
                    # OpenRC distros (Alpine)
                    [
                        "sh",
                        "-c",
                        "command -v rc-update >/dev/null 2>&1"
                        " && rc-update add qemu-guest-agent default"
                        " && rc-service qemu-guest-agent restart"
                        " || true",
                    ],
                ],
            }

            if self.cfg.ssh_pubkey:
                vendor_cfg["users"][0]["ssh_authorized_keys"] = [self.cfg.ssh_pubkey]

            if self.cfg.filesystems:
                mounts = []
                for fs in self.cfg.filesystems:
                    tag = fs.target
                    safe_target = sanitize_mount_target(tag)
                    mount_dir = Path("/mnt") / safe_target
                    vendor_cfg["runcmd"].append(["mkdir", "-p", str(mount_dir)])  # type: ignore[union-attr]
                    if fs.driver == "virtiofs":
                        fstype = "virtiofs"
                        options = ["defaults", "_netdev"]
                    else:
                        fstype = "9p"
                        options = ["trans=virtio,version=9p2000.L", "_netdev"]
                    if fs.readonly:
                        options.append("ro")
                    mounts.append([tag, str(mount_dir), fstype, ",".join(options), "0", "0"])
                if mounts:
                    vendor_cfg["mounts"] = mounts

            vendor_data = "#cloud-config\n" + yaml.safe_dump(vendor_cfg, sort_keys=False, default_flow_style=False)
            (tmp / "vendor-data").write_text(vendor_data, encoding="utf-8")

            # user-data: user's custom payload only (no vendor content mixed in)
            override_path = self.cfg.cloud_init_user_data_path
            if override_path:
                override_content = override_path.read_text(encoding="utf-8")
                if override_content.strip():
                    log("INFO", f"Using user cloud-init data from {override_path}")
                    (tmp / "user-data").write_text(override_content, encoding="utf-8")
                else:
                    log("WARN", f"CLOUD_INIT_USER_DATA file {override_path} is empty; ignored")
                    (tmp / "user-data").write_text("", encoding="utf-8")
            else:
                (tmp / "user-data").write_text("", encoding="utf-8")
            meta_data = (
                textwrap.dedent(
                    f"""
                instance-id: iid-{self.cfg.vm_name}
                local-hostname: {self.cfg.vm_name}
                """
                ).strip()
                + "\n"
            )
            (tmp / "meta-data").write_text(meta_data, encoding="utf-8")

            cmd = [
                "genisoimage",
                "-output",
                str(self.seed_iso),
                "-volid",
                "cidata",
                "-joliet",
                "-rock",
                str(tmp / "meta-data"),
                str(tmp / "user-data"),
                str(tmp / "vendor-data"),
            ]
            run(cmd)

    def _domain_exists(self) -> bool:
        if self.conn is None:
            raise ManagerError("libvirt connection not established")
        try:
            self.conn.lookupByName(self.cfg.vm_name)
            return True
        except libvirt.libvirtError:
            return False

    def _define_domain(self) -> None:
        if self.conn is None:
            raise ManagerError("libvirt connection not established")
        if self._domain_exists():
            log("INFO", f"Domain {self.cfg.vm_name} already defined")
            self.domain = self.conn.lookupByName(self.cfg.vm_name)
            return

        xml = self._render_domain_xml()
        self.domain = self.conn.defineXML(xml)
        if self.domain is None:
            raise ManagerError("Failed to define libvirt domain")
        log("SUCCESS", f"Defined domain {self.cfg.vm_name}")

    _QEMU_NS = "http://libvirt.org/schemas/domain/qemu/1.0"

    def _render_domain_xml(self) -> str:
        register_namespace("qemu", self._QEMU_NS)

        domain_type = "kvm" if self._kvm_available else "qemu"
        effective_model = self._effective_cpu_model
        host_cpu = self._kvm_available and effective_model.lower() in ("host", "host-passthrough")
        # Use cfg.machine_type for x86_64, otherwise arch profile default
        if self.cfg.arch == "x86_64":
            machine_type = self.cfg.machine_type
        else:
            machine_type = self._arch_profile["machine"]
        boot_order_priority = {dev: idx + 1 for idx, dev in enumerate(self.cfg.boot_order)}

        domain = Element("domain", type=domain_type)

        SubElement(domain, "name").text = self.cfg.vm_name
        mem = SubElement(domain, "memory", unit="MiB")
        mem.text = str(self.cfg.memory_mb)
        vcpu = SubElement(domain, "vcpu", placement="static")
        vcpu.text = str(self.cfg.cpus)

        # <iothreads>
        if self.cfg.io_thread:
            SubElement(domain, "iothreads").text = "1"

        # <os>
        os_el = SubElement(domain, "os")
        os_type = SubElement(os_el, "type", arch=self.cfg.arch, machine=machine_type)
        os_type.text = "hvm"

        # Firmware handling
        need_firmware = False
        if self.cfg.arch == "x86_64" and self.cfg.boot_mode != "legacy":
            need_firmware = True
        elif self.cfg.arch != "x86_64" and self._arch_profile.get("firmware"):
            need_firmware = True

        if need_firmware:
            if self._firmware_loader_path is None or self._firmware_vars_path is None:
                raise ManagerError("Firmware assets not prepared for this architecture.")
            secure_val = "yes" if self.cfg.boot_mode == "secure" else "no"
            loader = SubElement(os_el, "loader", readonly="yes", secure=secure_val, type="pflash")
            loader.text = str(self._firmware_loader_path)
            nvram = SubElement(os_el, "nvram")
            nvram.text = str(self._firmware_vars_path)

        # <features>
        arch_features = self._arch_profile.get("features", ())
        if arch_features or self.cfg.hyperv_enabled:
            features_el = SubElement(domain, "features")
            for feature in arch_features:
                SubElement(features_el, feature)

            # Hyper-V enlightenments
            if self.cfg.hyperv_enabled:
                hyperv = SubElement(features_el, "hyperv", mode="passthrough")
                SubElement(hyperv, "relaxed", state="on")
                SubElement(hyperv, "vapic", state="on")
                SubElement(hyperv, "spinlocks", state="on", retries="8191")
                SubElement(hyperv, "vpindex", state="on")
                SubElement(hyperv, "runtime", state="on")
                SubElement(hyperv, "synic", state="on")
                SubElement(hyperv, "stimer", state="on")
                SubElement(hyperv, "frequencies", state="on")

                # Per-vendor optimizations
                cpu_vendor = get_cpu_vendor()
                cpu_flags = get_cpu_flags()
                if cpu_vendor == "amd":
                    SubElement(hyperv, "evmcs", state="off")
                    if "avic" not in cpu_flags:
                        SubElement(hyperv, "avic", state="off")
                elif cpu_vendor == "intel":
                    if "apicv" not in cpu_flags:
                        SubElement(hyperv, "apicv", state="off")
                    SubElement(hyperv, "evmcs", state="off")

        # <clock> for Hyper-V
        if self.cfg.hyperv_enabled:
            clock = SubElement(domain, "clock", offset="localtime")
            SubElement(clock, "timer", name="hypervclock", present="yes")

        # <memoryBacking> (required for virtiofs)
        if any(fs.driver == "virtiofs" for fs in self.cfg.filesystems):
            mb = SubElement(domain, "memoryBacking")
            SubElement(mb, "source", type="memfd")
            SubElement(mb, "access", mode="shared")

        # <cpu>
        if host_cpu:
            SubElement(domain, "cpu", mode="host-passthrough")
        else:
            cpu_el = SubElement(domain, "cpu", mode="custom", match="exact")
            model_el = SubElement(cpu_el, "model", fallback="allow")
            model_el.text = effective_model

        # <devices>
        devices = SubElement(domain, "devices")

        # Disk controller info
        ctrl_info = DISK_CONTROLLERS.get(self.cfg.disk_controller, DISK_CONTROLLERS["virtio"])
        disk_bus = ctrl_info["bus"]
        dev_prefix = ctrl_info["dev_prefix"]

        # SCSI controller (needed for scsi bus)
        if self.cfg.disk_controller == "scsi":
            SubElement(devices, "controller", type="scsi", model="virtio-scsi-pci")

        # Disk I/O mode — detect filesystem and fallback if needed
        effective_disk_io = self.cfg.disk_io
        effective_disk_cache = self.cfg.disk_cache
        fs_type = detect_filesystem(self.work_image.parent).lower()
        if effective_disk_io != "threads" and ("ecryptfs" in fs_type or "tmpfs" in fs_type):
            log("WARN", f"Storage on {fs_type} — falling back to io=threads, cache=writeback")
            effective_disk_io = "threads"
            effective_disk_cache = "writeback"

        # Primary disk
        disk_driver_attrs = {
            "name": "qemu",
            "type": self.cfg.image_format,
            "cache": effective_disk_cache,
            "io": effective_disk_io,
        }
        if self.cfg.io_thread and disk_bus == "virtio":
            disk_driver_attrs["iothread"] = "1"

        disk = SubElement(devices, "disk", type="file", device="disk")
        SubElement(disk, "driver", **disk_driver_attrs)
        SubElement(disk, "source", file=str(self.work_image))
        primary_dev = f"{dev_prefix}a"
        SubElement(disk, "target", dev=primary_dev, bus=disk_bus)
        hd_order = boot_order_priority.get("hd")
        if hd_order is not None:
            SubElement(disk, "boot", order=str(hd_order))

        # Extra disks (DISK2-6)
        for disk_cfg in self.cfg.extra_disks:
            extra_disk_path = self.vm_dir / f"disk{disk_cfg.index}.{self.cfg.image_format}"
            extra_disk = SubElement(devices, "disk", type="file", device="disk")
            extra_driver_attrs = {
                "name": "qemu",
                "type": self.cfg.image_format,
                "cache": effective_disk_cache,
                "io": effective_disk_io,
            }
            if self.cfg.io_thread and disk_bus == "virtio":
                extra_driver_attrs["iothread"] = "1"
            SubElement(extra_disk, "driver", **extra_driver_attrs)
            SubElement(extra_disk, "source", file=str(extra_disk_path))
            # dev names: vdb, vdc, ... or sdb, sdc, ... etc.
            dev_letter = chr(ord("a") + disk_cfg.index - 1)
            SubElement(extra_disk, "target", dev=f"{dev_prefix}{dev_letter}", bus=disk_bus)

        # Block device passthrough
        for blk_dev in self.cfg.block_devices:
            blk = SubElement(devices, "disk", type="block", device="disk")
            blk_driver_attrs = {"name": "qemu", "type": "raw", "cache": "none"}
            SubElement(blk, "driver", **blk_driver_attrs)
            SubElement(blk, "source", dev=blk_dev.path)
            # Assign device names after existing disks
            dev_offset = len(self.cfg.extra_disks) + blk_dev.index
            dev_letter = chr(ord("a") + dev_offset)
            SubElement(blk, "target", dev=f"{dev_prefix}{dev_letter}", bus=disk_bus)
            # Detect sector size
            try:
                ss_result = subprocess.run(
                    ["blockdev", "--getss", blk_dev.path],
                    capture_output=True,
                    text=True,
                    check=True,
                )
                sector_size = ss_result.stdout.strip()
                if sector_size and sector_size != "512":
                    SubElement(blk, "blockio", logical_block_size=sector_size, physical_block_size=sector_size)
            except (subprocess.CalledProcessError, FileNotFoundError):
                pass

        # Seed ISO (cloud-init)
        if self.seed_iso:
            seed_disk = SubElement(devices, "disk", type="file", device="cdrom")
            SubElement(seed_disk, "driver", name="qemu", type="raw")
            SubElement(seed_disk, "source", file=str(self.seed_iso))
            SubElement(seed_disk, "target", dev="sda", bus="sata")
            SubElement(seed_disk, "readonly")

        # Boot ISO
        if self.boot_iso:
            boot_disk = SubElement(devices, "disk", type="file", device="cdrom")
            SubElement(boot_disk, "driver", name="qemu", type="raw")
            SubElement(boot_disk, "source", file=str(self.boot_iso))
            SubElement(boot_disk, "target", dev="sdb", bus="sata")
            SubElement(boot_disk, "readonly")
            cdrom_order = boot_order_priority.get("cdrom")
            if cdrom_order is not None:
                SubElement(boot_disk, "boot", order=str(cdrom_order))

        # Network interfaces
        network_order = boot_order_priority.get("network")
        ipv6_enabled = has_ipv6()
        for idx, nic in enumerate(self.cfg.nics):
            nic_boot_order = network_order if nic.boot else None
            rom_file = str(self._ipxe_rom_path) if self._ipxe_rom_path is not None else None
            ssh_forward = self.cfg.ssh_port if idx == 0 and nic.mode == "user" else None
            pf_list = self.cfg.port_forwards if idx == 0 and nic.mode == "user" else None
            mac_seed = self._network_macs.get(idx)
            nic_xml, resolved_mac = render_network_xml(
                nic,
                ssh_port=ssh_forward,
                mac_address=mac_seed,
                boot_order=nic_boot_order,
                rom_file=rom_file,
                port_forwards=pf_list,
                ipv6_enabled=ipv6_enabled,
            )
            self._network_macs[idx] = resolved_mac
            devices.append(fromstring(nic_xml))

        # Filesystem shares
        for fs in self.cfg.filesystems:
            driver_type = "virtiofs" if fs.driver == "virtiofs" else "path"
            fs_el = SubElement(devices, "filesystem", type="mount", accessmode=fs.accessmode)
            SubElement(fs_el, "driver", type=driver_type)
            if fs.driver == "virtiofs":
                SubElement(fs_el, "binary", path="/usr/lib/qemu/virtiofsd")
            SubElement(fs_el, "source", dir=str(fs.source))
            SubElement(fs_el, "target", dir=fs.target)
            if fs.readonly:
                SubElement(fs_el, "readonly")

        # USB controller + tablet
        if self.cfg.usb_controller:
            SubElement(devices, "controller", type="usb", model="qemu-xhci")
            SubElement(devices, "input", type="tablet", bus="usb")

        # TPM device
        if self.cfg.tpm_enabled:
            tpm_el = SubElement(devices, "tpm", model="tpm-crb")
            SubElement(tpm_el, "backend", type="emulator", version="2.0")

        # Memory balloon
        if self.cfg.balloon_enabled:
            SubElement(devices, "memballoon", model="virtio")

        # RNG (random number generator)
        if self.cfg.rng_enabled:
            rng = SubElement(devices, "rng", model="virtio")
            SubElement(rng, "backend", model="random").text = "/dev/urandom"

        # Guest agent channel
        channel_ga = SubElement(devices, "channel", type="unix")
        SubElement(channel_ga, "target", type="virtio", name="org.qemu.guest_agent.0")

        # Serial & console
        serial = SubElement(devices, "serial", type="pty")
        SubElement(serial, "target", port="0")
        console = SubElement(devices, "console", type="pty")
        SubElement(console, "target", type="virtio", port="0")

        # Graphics / display
        graphics = self.cfg.graphics_type
        if graphics and graphics != "none":
            gfx_attrs = {"type": graphics, "listen": "0.0.0.0"}
            if graphics == "vnc":
                gfx_attrs["port"] = str(self.cfg.vnc_port)
                gfx_attrs["autoport"] = "no"
            else:
                gfx_attrs["autoport"] = "yes"
            if self.cfg.vnc_keymap:
                gfx_attrs["keymap"] = self.cfg.vnc_keymap
            SubElement(devices, "graphics", **gfx_attrs)

            # GPU passthrough (Intel iGPU)
            if self.cfg.gpu_passthrough == "intel":
                video = SubElement(devices, "video")
                SubElement(video, "model", type="virtio", heads="1", primary="yes")
            else:
                video = SubElement(devices, "video")
                vid_model = SubElement(video, "model", type="virtio", heads="1", primary="yes")
                SubElement(vid_model, "resolution", x="1920", y="1080")

            vdagent = SubElement(devices, "channel", type="qemu-vdagent")
            vda_src = SubElement(vdagent, "source")
            SubElement(vda_src, "clipboard", copypaste="yes")
            SubElement(vda_src, "mouse", mode="client")
            SubElement(vdagent, "target", type="virtio", name="com.redhat.spice.0")

        # qemu:commandline for extra args, GPU passthrough, and Windows tuning
        qemu_args = []
        if self.cfg.extra_args:
            qemu_args.extend(self.cfg.extra_args.split())
        if self.cfg.gpu_passthrough == "intel":
            render_node = Path("/dev/dri/renderD128")
            if render_node.exists():
                qemu_args.extend(["-display", "egl-headless"])
                qemu_args.extend(["-device", f"virtio-vga-gl,rendernode={render_node}"])
        if self.cfg.hyperv_enabled:
            qemu_args.extend(["-global", "ICH9-LPC.disable_s3=1"])
            qemu_args.extend(["-global", "ICH9-LPC.disable_s4=1"])

        if qemu_args:
            qemu_cl = SubElement(domain, f"{{{self._QEMU_NS}}}commandline")
            for arg in qemu_args:
                SubElement(qemu_cl, f"{{{self._QEMU_NS}}}arg", value=arg)

        from xml.dom.minidom import parseString

        raw = tostring(domain, encoding="unicode")
        return parseString(raw).toprettyxml(indent="  ").split("\n", 1)[1].rstrip()

    def start(self) -> None:
        if self.domain is None:
            raise ManagerError("Domain not defined")

        if self.domain.isActive():  # type: ignore[attr-defined]
            log("INFO", f"Domain {self.cfg.vm_name} already running")
        else:
            try:
                self.domain.create()  # type: ignore[attr-defined]
            except libvirt.libvirtError as exc:
                message = exc.get_error_message() if hasattr(exc, "get_error_message") else str(exc)
                if "cgroup" in message.lower():
                    raise ManagerError(
                        f"libvirt could not access host cgroups: {message}\n"
                        "Run the container with --cgroupns=host to fix this."
                    ) from exc
                # Network fallback: if passt fails, try slirp
                if "passt" in message.lower() or "backend" in message.lower():
                    log("WARN", f"Network backend failed: {message}")
                    runtime = getattr(self.service_manager, "runtime", None)
                    if runtime and runtime.rootless:
                        log("WARN", "Rootless container — network backend errors are expected")
                    log("INFO", "Attempting fallback to slirp network backend...")
                    if self._try_network_fallback():
                        log("SUCCESS", f"Domain {self.cfg.vm_name} started (with slirp fallback)")
                        if self.cfg.novnc_enabled:
                            self.service_manager.start_novnc()
                        return
                raise ManagerError(f"Failed to start domain: {message}") from exc
            log("SUCCESS", f"Domain {self.cfg.vm_name} started")
        if self.cfg.novnc_enabled:
            self.service_manager.start_novnc()

    def _try_network_fallback(self) -> bool:
        """Attempt to restart domain with slirp backend instead of passt."""
        if self.conn is None or self.domain is None:
            return False
        try:
            # Get current XML and replace passt with slirp
            xml = self.domain.XMLDesc(0)
            if '<backend type="passt"/>' in xml:
                xml = xml.replace('<backend type="passt"/>', "")
                # Undefine old domain and re-define with modified XML
                try:
                    if self._firmware_vars_path is not None:
                        self.domain.undefineFlags(libvirt.VIR_DOMAIN_UNDEFINE_NVRAM)
                    else:
                        self.domain.undefine()
                except libvirt.libvirtError:
                    pass
                self.domain = self.conn.defineXML(xml)
                if self.domain is None:
                    return False
                self.domain.create()
                return True
        except libvirt.libvirtError as exc:
            log("WARN", f"Slirp fallback also failed: {exc}")
        return False

    def _guest_exec(self, command: str, args: list) -> Optional[tuple]:
        """Execute a command inside the guest via guest agent. Returns (exit_code, stdout) or None on failure."""
        exec_payload = json.dumps(
            {
                "execute": "guest-exec",
                "arguments": {"path": command, "arg": args, "capture-output": True},
            }
        )
        try:
            result = subprocess.run(
                ["virsh", "-c", LIBVIRT_URI, "qemu-agent-command", self.cfg.vm_name, exec_payload],
                capture_output=True,
                text=True,
                timeout=10,
            )
            if result.returncode != 0:
                return None
            pid = json.loads(result.stdout).get("return", {}).get("pid")
            if pid is None:
                return None
        except (subprocess.TimeoutExpired, json.JSONDecodeError, Exception):
            return None

        # Poll for completion
        status_payload = json.dumps({"execute": "guest-exec-status", "arguments": {"pid": pid}})
        poll_deadline = time.time() + 30
        while time.time() < poll_deadline:
            try:
                status_result = subprocess.run(
                    ["virsh", "-c", LIBVIRT_URI, "qemu-agent-command", self.cfg.vm_name, status_payload],
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                if status_result.returncode != 0:
                    return None
                ret = json.loads(status_result.stdout).get("return", {})
                if ret.get("exited"):
                    import base64

                    stdout = base64.b64decode(ret.get("out-data", "")).decode("utf-8", errors="replace")
                    return (ret.get("exitcode", -1), stdout)
            except (subprocess.TimeoutExpired, json.JSONDecodeError, Exception):
                return None
            time.sleep(0.5)
        return None

    def wait_for_guest_agent(self, timeout: float = 120.0, interval: float = 3.0, quiet: bool = False) -> bool:
        """Poll QEMU Guest Agent until it responds to guest-ping."""
        if not quiet:
            log("INFO", "Waiting for guest agent to become ready...")
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                result = subprocess.run(
                    ["virsh", "-c", LIBVIRT_URI, "qemu-agent-command", self.cfg.vm_name, '{"execute":"guest-ping"}'],
                    capture_output=True,
                    text=True,
                    timeout=5,
                )
                if result.returncode == 0:
                    if not quiet:
                        log("SUCCESS", "Guest agent is ready")
                    return True
            except subprocess.TimeoutExpired:
                pass
            except Exception:
                pass
            time.sleep(interval)
        if not quiet:
            log("WARN", f"Guest agent did not respond within {int(timeout)}s (VM may still be booting)")
        return False

    def wait_for_guest_ready(self, timeout: float = 120.0, interval: float = 3.0) -> bool:
        """Poll QEMU Guest Agent until the guest OS is responsive, then wait for cloud-init if enabled."""
        if not self.wait_for_guest_agent(timeout, interval):
            return False

        if not self.cfg.cloud_init_enabled:
            return True

        # Phase 2: wait for cloud-init completion
        ci_timeout = 300.0
        ci_interval = 5.0
        ci_fail_limit = 30  # give up after this many consecutive exec failures
        log("INFO", "Waiting for cloud-init to finish...")
        ci_start = time.time()
        ci_deadline = ci_start + ci_timeout
        ci_failures = 0
        while time.time() < ci_deadline:
            ret = self._guest_exec("cloud-init", ["status"])
            if ret is None:
                ci_failures += 1
                if ci_failures >= ci_fail_limit:
                    log("WARN", "Could not query cloud-init status; skipping wait")
                    return True
                time.sleep(ci_interval)
                continue
            ci_failures = 0
            exit_code, stdout = ret
            stdout_lower = stdout.lower()
            if "done" in stdout_lower:
                elapsed = int(time.time() - ci_start)
                log("SUCCESS", f"Cloud-init complete ({elapsed}s)")
                return True
            if "error" in stdout_lower:
                elapsed = int(time.time() - ci_start)
                log("WARN", f"Cloud-init finished with errors ({elapsed}s)")
                return True
            if "disabled" in stdout_lower:
                log("INFO", "Cloud-init is disabled in the guest")
                return True
            time.sleep(ci_interval)
        log("WARN", f"Cloud-init did not finish within {int(ci_timeout)}s (may still be running)")
        return True

    def wait_until_stopped(self) -> None:
        if self.domain is None:
            raise ManagerError("Domain not defined")

        shutdown_requested = False
        _first_sigint_time: Optional[float] = None
        _DOUBLE_PRESS_WINDOW = 3.0  # seconds

        def _do_shutdown():
            nonlocal shutdown_requested
            if shutdown_requested:
                return
            shutdown_requested = True
            log("INFO", "Shutting down VM...")
            try:
                self.domain.shutdown()  # type: ignore[attr-defined]
            except libvirt.libvirtError:
                try:
                    self.domain.destroy()  # type: ignore[attr-defined]
                except libvirt.libvirtError:
                    log("INFO", "libvirt connection lost; VM process will terminate with container")

        def _request_shutdown(signum, frame):
            nonlocal _first_sigint_time
            # SIGTERM always shuts down immediately (Docker stop, orchestrators)
            if signum == signal.SIGTERM:
                sig_name = signal.Signals(signum).name
                log("INFO", f"{sig_name} received, shutting down VM")
                _do_shutdown()
                return
            # SIGINT (Ctrl+C) uses double-press guard
            now = time.time()
            if _first_sigint_time is not None and (now - _first_sigint_time) < _DOUBLE_PRESS_WINDOW:
                log("INFO", "Second Ctrl+C received, shutting down VM")
                _do_shutdown()
            else:
                _first_sigint_time = now
                log("WARN", "Press Ctrl+C again within 3s to shutdown the VM (or Ctrl+] to detach console)")

        prev_sigterm = signal.signal(signal.SIGTERM, _request_shutdown)
        prev_sigint = signal.signal(signal.SIGINT, _request_shutdown)
        try:
            log("INFO", f"Waiting for domain {self.cfg.vm_name} to stop")
            while True:
                try:
                    active = self.domain.isActive()  # type: ignore[attr-defined]
                except libvirt.libvirtError:
                    log("INFO", f"Domain {self.cfg.vm_name} is no longer active")
                    return
                if not active:
                    log("INFO", f"Domain {self.cfg.vm_name} is no longer active")
                    return
                time.sleep(1)
        finally:
            signal.signal(signal.SIGTERM, prev_sigterm)
            signal.signal(signal.SIGINT, prev_sigint)

    def cleanup(self) -> None:
        # Stop TPM if running
        if self._tpm_process is not None:
            try:
                self._tpm_process.terminate()
                self._tpm_process.wait(timeout=5)
            except (subprocess.TimeoutExpired, OSError):
                try:
                    self._tpm_process.kill()
                except OSError:
                    pass

        if self.domain is not None:
            try:
                if self.domain.isActive():  # type: ignore[attr-defined]
                    log("INFO", f"Shutting down domain {self.cfg.vm_name}")
                    self.domain.destroy()  # type: ignore[attr-defined]
            except libvirt.libvirtError:
                log("DEBUG", f"Could not destroy domain {self.cfg.vm_name} (libvirt connection lost)")
            try:
                # NVRAM domains (UEFI) need the NVRAM flag to undefine
                if self._firmware_vars_path is not None:
                    self.domain.undefineFlags(libvirt.VIR_DOMAIN_UNDEFINE_NVRAM)
                else:
                    self.domain.undefine()
            except libvirt.libvirtError:
                log("DEBUG", f"Could not undefine domain {self.cfg.vm_name} (libvirt connection lost)")

        # Safety net: kill any remaining qemu processes to prevent orphans
        self._kill_remaining_qemu()

        if not self.cfg.persist and self.vm_dir.exists():
            try:
                shutil.rmtree(self.vm_dir)
            except OSError:
                log("WARN", f"Failed to remove {self.vm_dir}")

    def _kill_remaining_qemu(self) -> None:
        """Kill any QEMU processes still running inside this container."""
        try:
            result = subprocess.run(
                ["pgrep", "-f", "qemu-system"],
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                return
            for line in result.stdout.strip().splitlines():
                pid = line.strip()
                if pid:
                    log("WARN", f"Killing orphaned QEMU process (PID {pid})")
                    try:
                        os.kill(int(pid), signal.SIGKILL)
                    except (OSError, ValueError):
                        pass
        except FileNotFoundError:
            pass
