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

| Variable | Default | Description |
| --- | --- | --- |
| `DISTRO` | `ubuntu-2404` | Distribution key from `distros.yaml`. |
| `VM_MEMORY` | `4096` | Memory in MiB. |
| `VM_CPUS` | `2` | Number of vCPUs. |
| `VM_DISK_SIZE` | `20G` | Size for the working disk (resize target). |
| `VM_DISPLAY` | `none` | Graphics backend (`none`, `vnc`, `novnc`). |
| `VM_VNC_PORT` | `5900` | VNC listen port (when `VM_DISPLAY` is `vnc` or `novnc`). |
| `VM_NOVNC_PORT` | `6080` | noVNC/websockify port (when `VM_DISPLAY=novnc`). |
| `VM_BASE_IMAGE` | *(downloaded per distro)* | Override base QCOW2/RAW path. Use `blank` to force blank disk creation. |
| `VM_BLANK_DISK` | `0` | Set `1` to create a blank disk (size from `VM_DISK_SIZE`). |
| `VM_BOOT_ISO` | *(unset)* | Path to attach as CD-ROM (`/images/base/…`). |
| `VM_BOOT_ORDER` | `hd` | Comma-separated boot devices (`cdrom`, `hd`, `network`). Order controls `<boot order>` hints. |
| `VM_CLOUD_INIT` | `1` | Enable/disable cloud-init seed generation. |
| `VM_ARCH` | `x86_64` | QEMU architecture. |
| `VM_CPU_MODEL` | `host` | CPU model (`host`, `host-passthrough`, or named). |
| `EXTRA_ARGS` | *(blank)* | Additional QEMU CLI arguments (space-delimited). |
| `VM_PASSWORD` | `password` | Console password (cloud-init). |
| `VM_SSH_PORT` | `2222` | Host TCP port forwarded to guest `:22` (user-mode networking). |
| `VM_NAME` | hostname | VM name (affects disk paths). |
| `VM_SSH_PUBKEY` | *(unset)* | SSH public key injected via cloud-init. |
| `VM_PERSIST` | `0` | Keep work disk & domain after shutdown. |
| `VM_NO_CONSOLE` | `0` | Skip `virsh console` attachment in the entrypoint. |
| `REDFISH_PORT` | `8443` | Redfish HTTPS port. |
| `REDFISH_USERNAME` | `admin` | Redfish username. |
| `REDFISH_PASSWORD` | `password` | Redfish password. |
| `REDFISH_SYSTEM_ID` | `VM_NAME` | Redfish system identifier. |

### Advanced

| Variable | Default | Description |
| --- | --- | --- |
| `LIBVIRT_URI` | `qemu:///system` | Override the libvirt URI used by the manager (uncommon). |
