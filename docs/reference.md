# Configuration Reference

## Common Examples

```bash
# Change distro and resources
docker run --rm -it --device /dev/kvm:/dev/kvm \
  -e DISTRO=debian-12 -e MEMORY=8192 -e CPUS=4 ...

# Boot from ISO with GUI
docker run --rm -it --device /dev/kvm:/dev/kvm -p 6080:6080 \
  -e BOOT_ISO=https://example.com/install.iso -e GRAPHICS=novnc ...

# Persistent VM with DATA_DIR
docker run --rm -it --device /dev/kvm:/dev/kvm \
  -v ./data:/data -e DATA_DIR=/data -e PERSIST=1 ...

# Share a host directory into the guest
docker run --rm -it --device /dev/kvm:/dev/kvm \
  -v ./share:/share -e FILESYSTEM_SOURCE=/share -e FILESYSTEM_DRIVER=9p ...

# List available distributions
docker run --rm ghcr.io/munenick/docker-vm-runner:latest --list-distros
```

## Supported Distribution Keys

These map to entries in `distros.yaml` (bind-mount your own file to `/config/distros.yaml` to customize).

- `ubuntu-2404`, `ubuntu-2404-arm64`, `ubuntu-2204`, `ubuntu-2204-arm64`, `ubuntu-2004`
- `debian-12`, `debian-12-arm64`, `debian-11`
- `centos-stream-9`
- `fedora-41`, `fedora-41-arm64`
- `opensuse-leap-155`
- `rocky-linux-9`, `rocky-linux-9-arm64`, `rocky-linux-8`
- `almalinux-9`, `almalinux-9-arm64`, `almalinux-8`
- `archlinux`

Each entry can declare an `arch` field to set the default architecture for that image. If omitted it defaults to `x86_64`.

## Environment Variables

### Compute & Storage

| Variable | Default | Description |
| --- | --- | --- |
| `DISTRO` | `ubuntu-2404` | Distribution key from `distros.yaml`. |
| `MEMORY` | `4096` | Memory in MiB. |
| `CPUS` | `2` | Number of vCPUs. |
| `DISK_SIZE` | `20G` | Working disk size; resized on first boot. |
| `BASE_IMAGE` | *(auto downloaded)* | Override base QCOW2/RAW image path. Use `blank` to create a fresh disk. |
| `BLANK_DISK` | `0` | Set `1` to create a blank disk sized by `DISK_SIZE`. |
| `BOOT_ISO` | *(unset)* | Attach an ISO as CD-ROM. Accepts a local path or an HTTP(S) URL (URLs are auto-detected and downloaded). When an ISO is detected, `cdrom` is auto-added to `BOOT_ORDER` and cloud-init is auto-disabled (override with `CLOUD_INIT=1`). A blank work disk is also created by default unless `BASE_IMAGE` or `BLANK_DISK` is explicitly set. |
| `BOOT_ISO_URL` | *(unset)* | Explicit URL form (same as setting `BOOT_ISO` to a URL). |
| `BOOT_ORDER` | `hd` | Comma-separated boot device order: `hd`, `cdrom`, `network`. |
| `CLOUD_INIT` | `1` | Enable/disable cloud-init seed generation. Auto-disabled when `BOOT_ISO` is set. |
| `CLOUD_INIT_USER_DATA` | *(unset)* | Path to an additional cloud-init payload file. Added as a second multipart section after the built-in configuration. |
| `ARCH` | `x86_64` | Guest architecture. Accepts `x86_64` (alias `amd64`) or `aarch64` (alias `arm64`). Defaults to the distribution's declared `arch` or `x86_64`. |
| `CPU_MODEL` | `host` | CPU model (`host`, `host-passthrough`, named models). |
| `EXTRA_ARGS` | *(blank)* | Additional QEMU CLI arguments (space-delimited). |

### Console & Access

| Variable | Default | Description |
| --- | --- | --- |
| `GUEST_NAME` | *(auto)* | Internal VM name, used for disk paths. Fallback: `GUEST_NAME` -> `HOSTNAME` -> distro key. Set explicitly when using host networking. |
| `GUEST_PASSWORD` | `password` | Console password injected via cloud-init. |
| `SSH_PORT` | `2222` | Host TCP port forwarded to guest `:22`. |
| `SSH_PUBKEY` | *(unset)* | SSH public key injected via cloud-init. |
| `PERSIST` | `0` | Keep the work disk and libvirt domain after shutdown. |
| `NO_CONSOLE` | `0` | Skip attaching `virsh console` (`1`, `true`, `yes`, `on`). |

### Filesystem Sharing

| Variable | Default | Description |
| --- | --- | --- |
| `FILESYSTEM_SOURCE` | *(unset)* | Directory inside the container to expose to the guest (bind-mount a host path here). |
| `FILESYSTEM_TARGET` | *(auto)* | Guest-facing tag presented to the VM. Auto-derived from the last segment of `FILESYSTEM_SOURCE` when omitted. |
| `FILESYSTEM_DRIVER` | `virtiofs` | Filesystem driver: `virtiofs` or `9p`. |
| `FILESYSTEM_ACCESSMODE` | `passthrough` | Access mode (`passthrough`, `mapped`, or `squash`). Note: virtiofs only supports `passthrough`; use `9p` driver for `mapped` or `squash`. |
| `FILESYSTEM_READONLY` | `0` | Set to `1` to present the share as read-only. |

Append an index (`FILESYSTEM2_SOURCE`, `FILESYSTEM3_SOURCE`, …) to define multiple shares. Only the variables you override are required for each additional index.
The guest automatically mounts each tag at `/mnt/<tag>` using cloud-init. Virtiofs requires the container to allow `unshare(2)` (e.g., `--security-opt seccomp=unconfined`). If that isn’t possible, set `FILESYSTEM_DRIVER=9p`.

### Networking

| Variable | Default | Description |
| --- | --- | --- |
| `NETWORK_MODE` | `nat` | `nat` (QEMU user-mode), `bridge` (libvirt bridge), or `direct` (macvtap). |
| `NETWORK_BRIDGE` | *(required for bridge)* | Name of the host bridge (e.g., `br0`) when `NETWORK_MODE=bridge`. |
| `NETWORK_DIRECT_DEV` | *(required for direct)* | Host NIC to bind (e.g., `eth0`) when `NETWORK_MODE=direct` (requires `--volume /dev:/dev` and `--privileged`). |
| `NETWORK_MAC` | *(auto)* | Override the guest MAC address (`aa:bb:cc:dd:ee:ff`). |
| `NETWORK_MODEL` | `virtio` | NIC model: `virtio`, `e1000`, `e1000e`, `rtl8139`, `ne2k_pci`, `pcnet`, `vmxnet3`. |
| `NETWORK_BOOT` | `0` | Set `1` to include this NIC in the boot order (useful for PXE without iPXE). |
| `IPXE_ENABLE` | `0` | Inject an iPXE ROM on the primary NIC and prioritize `network` in the boot order. |
| `IPXE_ROM_PATH` | *(auto)* | Override the iPXE ROM path. Auto-selected based on `NETWORK_MODEL` (e.g. `pxe-virtio.rom` for virtio on x86_64). Provide a full path when using a custom build. |

**Multi-NIC:** Append an index after the prefix to define additional NICs: `NETWORK2_MODE`, `NETWORK2_BRIDGE`, `NETWORK2_MAC`, etc. The first NIC uses the base name (no index).

### Graphics & GUI

| Variable | Default | Description |
| --- | --- | --- |
| `GRAPHICS` | `none` | Graphics backend (`none`, `vnc`, `novnc`). |
| `VNC_PORT` | `5900` | VNC listen port. |
| `NOVNC_PORT` | `6080` | noVNC/websockify port. |

### Redfish

| Variable | Default | Description |
| --- | --- | --- |
| `REDFISH_ENABLE` | `0` | Start sushy-emulator for Redfish. |
| `REDFISH_PORT` | `8443` | Redfish HTTPS port. |
| `REDFISH_USERNAME` | `admin` | Redfish username. |
| `REDFISH_PASSWORD` | `password` | Redfish password. |
| `REDFISH_SYSTEM_ID` | *(derived VM name)* | Redfish system identifier. Defaults to the resolved VM name (same as `GUEST_NAME` when set). |

### Advanced

| Variable | Default | Description |
| --- | --- | --- |
| `DATA_DIR` | *(unset)* | Single volume mount for all persistent data. When set, `base/`, `vms/`, and `state/` subdirectories are created under this path. Replaces the need for separate `/images` and `/var/lib/docker-vm-runner` mounts. |
| `REQUIRE_KVM` | `0` | Set `1` to abort if `/dev/kvm` is not available (instead of falling back to TCG). |
| `LIBVIRT_URI` | `qemu:///system` | Override the libvirt URI used by the manager (uncommon). |
| `LOG_VERBOSE` | `0` | Set `1` to enable verbose debug logging (shows all subprocess commands). |
| `REDFISH_STORAGE_POOL` | `default` | Libvirt storage pool name used by sushy-emulator. |
| `REDFISH_STORAGE_PATH` | `/var/lib/libvirt/images` | Path for the Redfish storage pool. |

### CLI Flags

The manager script accepts these flags (passed as container command arguments):

| Flag | Description |
| --- | --- |
| `--list-distros` | Print available distributions from `distros.yaml` and exit. |
| `--show-config` | Parse environment variables, print the resolved configuration, and exit. |
| `--no-console` | Do not attach to the serial console (same as `NO_CONSOLE=1`). |

## Guest Command Execution

The `guest-exec` utility runs commands inside the VM via the QEMU Guest Agent (no SSH required).

```bash
docker exec <container> guest-exec "<command>"
```

### How It Works

1. A virtio serial channel (`org.qemu.guest_agent.0`) is always configured in the VM's libvirt domain.
2. Cloud-init installs and enables `qemu-guest-agent` inside the guest on first boot.
3. `guest-exec` sends a `guest-exec` QMP command via `virsh qemu-agent-command`, polls `guest-exec-status` until the command completes, then outputs the decoded stdout/stderr and exits with the guest's exit code.

### Examples

```bash
# Simple commands
docker exec vm1 guest-exec "uname -a"
docker exec vm1 guest-exec "df -h"
docker exec vm1 guest-exec "cat /etc/os-release"

# Exit code propagation
docker exec vm1 guest-exec "exit 42"    # exits with code 42

# Multi-argument form (no shell wrapping)
docker exec vm1 guest-exec ls -la /tmp
```

### Notes

- Available once cloud-init completes and `qemu-guest-agent` is running (typically 1-2 minutes after boot).
- When the guest agent is not connected, `guest-exec` prints a descriptive error and exits with code 127.
- Commands passed as a single string containing spaces are automatically wrapped with `/bin/sh -c`; multiple arguments are executed directly.
