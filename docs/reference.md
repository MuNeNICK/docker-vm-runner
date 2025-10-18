# Configuration Reference

## Supported Distribution Keys

These map to entries in `distros.yaml` (bind-mount your own file to `/config/distros.yaml` to customize).

- `ubuntu-2404`, `ubuntu-2204`, `ubuntu-2004`
- `debian-12`, `debian-11`
- `centos-stream-9`
- `fedora-41`
- `opensuse-leap-155`
- `rocky-linux-9`, `rocky-linux-8`
- `almalinux-9`, `almalinux-8`
- `archlinux`

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
| `BOOT_ISO` | *(unset)* | Attach an ISO as CD-ROM (`/images/base/...`). |
| `BOOT_ORDER` | `hd` | Comma-separated boot device order (`cdrom`, `hd`, `network`). |
| `CLOUD_INIT` | `1` | Enable/disable cloud-init seed generation. |
| `ARCH` | `x86_64` | QEMU architecture. |
| `CPU_MODEL` | `host` | CPU model (`host`, `host-passthrough`, named models). |
| `EXTRA_ARGS` | *(blank)* | Additional QEMU CLI arguments (space-delimited). |

### Console & Access

| Variable | Default | Description |
| --- | --- | --- |
| `GUEST_NAME` | container hostname | Internal VM name, used for disk paths. Set explicitly when using host networking. |
| `GUEST_PASSWORD` | `password` | Console password injected via cloud-init. |
| `SSH_PORT` | `2222` | Host TCP port forwarded to guest `:22`. |
| `SSH_PUBKEY` | *(unset)* | SSH public key injected via cloud-init. |
| `PERSIST` | `0` | Keep the work disk and libvirt domain after shutdown. |
| `NO_CONSOLE` | `0` | Skip attaching `virsh console` (`1`, `true`, `yes`, `on`). |

### Networking

| Variable | Default | Description |
| --- | --- | --- |
| `NETWORK_MODE` | `nat` | `nat` (QEMU user-mode), `bridge` (libvirt bridge), or `direct` (macvtap). |
| `NETWORK_BRIDGE` | *(required for bridge)* | Name of the host bridge (e.g., `br0`) when `NETWORK_MODE=bridge`. |
| `NETWORK_DIRECT_DEV` | *(required for direct)* | Host NIC to bind (e.g., `eth0`) when `NETWORK_MODE=direct` (requires `--volume /dev:/dev` and `--privileged`). |
| `NETWORK_MAC` | *(auto)* | Override the guest MAC address (`aa:bb:cc:dd:ee:ff`). |

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
| `REDFISH_SYSTEM_ID` | `GUEST_NAME` | Redfish system identifier. |

### Advanced

| Variable | Default | Description |
| --- | --- | --- |
| `LIBVIRT_URI` | `qemu:///system` | Override the libvirt URI used by the manager (uncommon). |
