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
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
from pathlib import Path
from typing import Dict, List, Optional
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
    IMAGES_DIR,
    INSTALLED_MARKER_NAME,
    LIBVIRT_URI,
    STATE_DIR,
    SUPPORTED_ARCHES,
    VM_IMAGES_DIR,
)
from app.exceptions import ManagerError
from app.models import VMConfig
from app.network import render_network_xml
from app.utils import (
    detect_cloud_init_content_type,
    download_file,
    ensure_directory,
    get_env_bool,
    hash_password,
    kvm_available,
    log,
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
        self._external_base_image = False
        if self.cfg.base_image_path:
            self.base_image = Path(self.cfg.base_image_path)
            self._external_base_image = True
        else:
            self.base_image = BASE_IMAGES_DIR / f"{self.cfg.distro}.{self.cfg.image_format}"
        self.work_image = self.vm_dir / f"disk.{self.cfg.image_format}"
        self.boot_iso = Path(self.cfg.boot_iso_path) if self.cfg.boot_iso_path else None
        self.boot_iso_url = self.cfg.boot_iso_url
        self.seed_iso = self.vm_dir / "seed.iso" if self.cfg.cloud_init_enabled else None
        self._disk_reused = False
        self._network_macs: Dict[int, str] = {}
        self._ipxe_rom_path: Optional[Path] = None
        self._boot_iso_cache_path: Optional[Path] = None
        if self.cfg.ipxe_enabled and self.cfg.ipxe_rom_path:
            self._ipxe_rom_path = Path(self.cfg.ipxe_rom_path)

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
                    raise ManagerError(
                        f"CPU_MODEL={self.cfg.cpu_model} requires KVM for architecture {self.cfg.arch}."
                    )
                self._effective_cpu_model = fallback
                log(
                    "WARN",
                    f"CPU_MODEL=host is not compatible with TCG on {self.cfg.arch}. Using {fallback} instead.",
                )
        if not self.cfg.blank_work_disk:
            self._ensure_base_image()
        self._prepare_work_image()
        # Smart ISO skip: if disk was reused from a prior install, skip ISO boot
        iso_requested = bool(self.boot_iso or self.boot_iso_url)
        if (
            iso_requested
            and self._disk_reused
            and self._is_installed()
            and not self.cfg.force_iso
        ):
            log("INFO", "Persistent disk with prior install found; skipping ISO boot (set FORCE_ISO=1 to override)")
            self.boot_iso = None
            self.boot_iso_url = None
            if "cdrom" in self.cfg.boot_order:
                self.cfg.boot_order = [d for d in self.cfg.boot_order if d != "cdrom"]
            if "hd" not in self.cfg.boot_order:
                self.cfg.boot_order = ["hd"] + self.cfg.boot_order
        self._prepare_boot_iso()
        if self.boot_iso and not self.boot_iso.exists():
            raise ManagerError(f"Boot ISO not found: {self.boot_iso}")
        self._extract_qemu_binary()
        self._prepare_firmware()
        self._generate_cloud_init()
        self._define_domain()

    def _ensure_base_image(self) -> None:
        if self._external_base_image:
            if not self.base_image.exists():
                raise ManagerError(f"Base image not found: {self.base_image}")
            log("INFO", f"Using external base image: {self.base_image}")
            return
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

        download_file(self.cfg.image_url, self.base_image, label="Downloading base image")

    def _prepare_work_image(self) -> None:
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
                        capture_output=True, text=True,
                    )
                    if info.returncode == 0:
                        current_vsize = json.loads(info.stdout).get("virtual-size", 0)
                        requested = self.cfg.disk_size
                        # Parse requested size to bytes for comparison
                        suffix = requested[-1].upper() if requested[-1].isalpha() else ""
                        num = int(requested[:-1]) if suffix else int(requested)
                        multiplier = {"K": 1024, "M": 1024**2, "G": 1024**3, "T": 1024**4}.get(suffix, 1)
                        requested_bytes = num * multiplier
                        if requested_bytes > current_vsize:
                            log("INFO", f"Expanding disk from {current_vsize // (1024**3)}G to {requested}...")
                            run(["qemu-img", "resize", str(self.work_image), requested])
                            log("SUCCESS", f"Disk expanded to {requested}")
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
                run(
                    [
                        "qemu-img",
                        "create",
                        "-f",
                        self.cfg.image_format,
                        str(self.work_image),
                        self.cfg.disk_size,
                    ]
                )
            else:
                log("INFO", f"Creating working disk {self.work_image}")
                if self.base_image.suffix.lower() == ".iso":
                    raise ManagerError(
                        f"BASE_IMAGE points to an ISO ({self.base_image}). "
                        f"Try: BOOT_ISO={self.base_image} (and optionally BLANK_DISK=1)"
                    )
                shutil.copy2(self.base_image, self.work_image)
                if self.cfg.disk_size and self.cfg.disk_size != "0":
                    log("INFO", f"Resizing disk to {self.cfg.disk_size}...")
                    run(["qemu-img", "resize", str(self.work_image), self.cfg.disk_size])
        else:
            log("INFO", f"Persistent disk retained at {self.work_image}")

    def _prepare_boot_iso(self) -> None:
        if self.boot_iso:
            return
        if not self.boot_iso_url:
            return

        ensure_directory(BOOT_ISO_CACHE_DIR)
        digest = hashlib.sha256(self.boot_iso_url.encode("utf-8")).hexdigest()
        parsed = urlparse(self.boot_iso_url)
        base_name = Path(parsed.path or "").name or "boot.iso"
        safe_name = re.sub(r"[^A-Za-z0-9._-]", "_", base_name) or "boot.iso"
        if not Path(safe_name).suffix:
            safe_name = f"{safe_name}.iso"
        destination = BOOT_ISO_CACHE_DIR / f"{digest[:12]}-{safe_name}"

        if destination.exists() and destination.stat().st_size > 0:
            log("INFO", f"Using cached BOOT_ISO_URL download: {destination}")
        else:
            download_file(self.boot_iso_url, destination, label="Downloading boot ISO")

        self._boot_iso_cache_path = destination
        self.boot_iso = destination

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
        firmware_cfg = self._arch_profile.get("firmware")
        if not firmware_cfg:
            return

        loader_path = Path(firmware_cfg["loader"])
        vars_template_path = Path(firmware_cfg["vars_template"])

        # Extract AAVMF from .deb on demand if firmware files are missing
        if not loader_path.exists() or not vars_template_path.exists():
            self._extract_aavmf_deb()

        if not loader_path.exists():
            raise ManagerError(
                f"Firmware loader not found at {loader_path} for arch {self.cfg.arch}."
            )
        if not vars_template_path.exists():
            raise ManagerError(
                f"Firmware variable template not found at {vars_template_path} for arch {self.cfg.arch}."
            )

        firmware_dir = STATE_DIR / "firmware"
        ensure_directory(firmware_dir)
        vars_destination = firmware_dir / f"{self.cfg.vm_name}-vars.fd"
        if not vars_destination.exists():
            shutil.copy2(vars_template_path, vars_destination)

        self._firmware_loader_path = loader_path
        self._firmware_vars_path = vars_destination

    def _generate_cloud_init(self) -> None:
        if not self.cfg.cloud_init_enabled:
            return
        ensure_directory(IMAGES_DIR)
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            passwd_hash = hash_password(self.cfg.password)

            cloud_cfg: Dict[str, object] = {
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
            }

            if self.cfg.ssh_pubkey:
                cloud_cfg["ssh_authorized_keys"] = [self.cfg.ssh_pubkey]

            runcmd: List[List[str]] = [
                ["systemctl", "enable", "--now", "qemu-guest-agent"],
            ]

            if self.cfg.filesystems:
                mounts = []
                for fs in self.cfg.filesystems:
                    tag = fs.target
                    safe_target = sanitize_mount_target(tag)
                    mount_dir = Path("/mnt") / safe_target
                    mkdir_cmd = ["mkdir", "-p", str(mount_dir)]
                    if mkdir_cmd not in runcmd:
                        runcmd.append(mkdir_cmd)
                    if fs.driver == "virtiofs":
                        fstype = "virtiofs"
                        options = ["defaults", "_netdev"]
                    else:
                        fstype = "9p"
                        options = ["trans=virtio,version=9p2000.L", "_netdev"]
                    if fs.readonly:
                        options.append("ro")
                    mount_entry = [
                        tag,
                        str(mount_dir),
                        fstype,
                        ",".join(options),
                        "0",
                        "0",
                    ]
                    mounts.append(mount_entry)
                if mounts:
                    cloud_cfg["mounts"] = mounts

            if runcmd:
                cloud_cfg["runcmd"] = runcmd

            vendor_user_data = "#cloud-config\n" + yaml.safe_dump(
                cloud_cfg, sort_keys=False, default_flow_style=False
            )

            user_data_payload = vendor_user_data
            override_path = self.cfg.cloud_init_user_data_path
            if override_path:
                override_content = override_path.read_text(encoding="utf-8")
                if override_content.strip():
                    log("INFO", f"Appending user cloud-init data from {override_path}")
                    multipart = MIMEMultipart()
                    vendor_part = MIMEText(
                        vendor_user_data,
                        _subtype="cloud-config",
                        _charset="utf-8",
                    )
                    vendor_part.add_header(
                        "Content-Disposition",
                        "attachment",
                        filename="00-vendor-cloud-config.yaml",
                    )
                    multipart.attach(vendor_part)

                    content_type = detect_cloud_init_content_type(override_content)
                    main_type, _, subtype = content_type.partition("/")
                    if main_type != "text" or not subtype:
                        raise ManagerError(
                            f"Unsupported content type '{content_type}' inferred for CLOUD_INIT_USER_DATA"
                        )
                    user_part = MIMEText(
                        override_content,
                        _subtype=subtype,
                        _charset="utf-8",
                    )
                    user_part.add_header(
                        "Content-Disposition",
                        "attachment",
                        filename=f"99-user-data.{subtype.replace('/', '-')}",
                    )
                    multipart.attach(user_part)
                    user_data_payload = multipart.as_string()
                else:
                    log(
                        "WARN",
                        f"CLOUD_INIT_USER_DATA file {override_path} is empty; "
                        "only vendor cloud-config will be applied.",
                    )

            (tmp / "user-data").write_text(user_data_payload, encoding="utf-8")
            meta_data = textwrap.dedent(
                f"""
                instance-id: iid-{self.cfg.vm_name}
                local-hostname: {self.cfg.vm_name}
                """
            ).strip() + "\n"
            (tmp / "meta-data").write_text(meta_data, encoding="utf-8")

            cmd = [
                "genisoimage",
                "-output",
                str(self.seed_iso),
                "-volid",
                "cidata",
                "-joliet",
                "-rock",
                str(tmp / "user-data"),
                str(tmp / "meta-data"),
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
        machine_type = self._arch_profile["machine"]
        boot_order_priority = {dev: idx + 1 for idx, dev in enumerate(self.cfg.boot_order)}

        domain = Element("domain", type=domain_type)

        SubElement(domain, "name").text = self.cfg.vm_name
        mem = SubElement(domain, "memory", unit="MiB")
        mem.text = str(self.cfg.memory_mb)
        vcpu = SubElement(domain, "vcpu", placement="static")
        vcpu.text = str(self.cfg.cpus)

        # <os>
        os_el = SubElement(domain, "os")
        os_type = SubElement(os_el, "type", arch=self.cfg.arch, machine=machine_type)
        os_type.text = "hvm"
        if self._arch_profile.get("firmware"):
            if self._firmware_loader_path is None or self._firmware_vars_path is None:
                raise ManagerError("Firmware assets not prepared for this architecture.")
            loader = SubElement(os_el, "loader", readonly="yes", secure="no", type="pflash")
            loader.text = str(self._firmware_loader_path)
            nvram = SubElement(os_el, "nvram")
            nvram.text = str(self._firmware_vars_path)

        # <features>
        features = self._arch_profile.get("features", ())
        if features:
            features_el = SubElement(domain, "features")
            for feature in features:
                SubElement(features_el, feature)

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

        # Primary disk
        disk = SubElement(devices, "disk", type="file", device="disk")
        SubElement(disk, "driver", name="qemu", type=self.cfg.image_format, cache="none")
        SubElement(disk, "source", file=str(self.work_image))
        SubElement(disk, "target", dev="vda", bus="virtio")
        hd_order = boot_order_priority.get("hd")
        if hd_order is not None:
            SubElement(disk, "boot", order=str(hd_order))

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

            video = SubElement(devices, "video")
            vid_model = SubElement(video, "model", type="virtio", heads="1", primary="yes")
            SubElement(vid_model, "resolution", x="1920", y="1080")

            vdagent = SubElement(devices, "channel", type="qemu-vdagent")
            vda_src = SubElement(vdagent, "source")
            SubElement(vda_src, "clipboard", copypaste="yes")
            SubElement(vda_src, "mouse", mode="client")
            SubElement(vdagent, "target", type="virtio", name="com.redhat.spice.0")

        # qemu:commandline for extra args
        if self.cfg.extra_args:
            qemu_cl = SubElement(domain, f"{{{self._QEMU_NS}}}commandline")
            for arg in self.cfg.extra_args.split():
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
                raise ManagerError(f"Failed to start domain: {message}") from exc
            log("SUCCESS", f"Domain {self.cfg.vm_name} started")
        if self.cfg.novnc_enabled:
            self.service_manager.start_novnc()

    def wait_for_guest_ready(self, timeout: float = 120.0, interval: float = 3.0) -> bool:
        """Poll QEMU Guest Agent until the guest OS is responsive."""
        log("INFO", "Waiting for guest agent to become ready...")
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                result = subprocess.run(
                    ["virsh", "-c", LIBVIRT_URI, "qemu-agent-command",
                     self.cfg.vm_name, '{"execute":"guest-ping"}'],
                    capture_output=True, text=True, timeout=5,
                )
                if result.returncode == 0:
                    log("SUCCESS", "Guest agent is ready — VM is fully booted")
                    return True
            except subprocess.TimeoutExpired:
                pass
            except Exception:
                pass
            time.sleep(interval)
        log("WARN", f"Guest agent did not respond within {int(timeout)}s (VM may still be booting)")
        return False

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
        if self.domain is not None:
            try:
                if self.domain.isActive():  # type: ignore[attr-defined]
                    log("INFO", f"Shutting down domain {self.cfg.vm_name}")
                    self.domain.destroy()  # type: ignore[attr-defined]
            except libvirt.libvirtError:
                log("DEBUG", f"Could not destroy domain {self.cfg.vm_name} (libvirt connection lost)")
            try:
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
                capture_output=True, text=True, check=False,
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
