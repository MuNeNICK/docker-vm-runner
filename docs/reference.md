# Configuration Reference

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
| `BOOT_ISO` | *(unset)* | Attach an ISO as CD-ROM (`/images/base/...`). |
| `BOOT_ISO_URL` | *(unset)* | HTTP(S) URL to fetch and attach as a CD-ROM. Download occurs inside the container and is cached under `/var/lib/docker-vm-runner/boot-isos`. Mutually exclusive with `BOOT_ISO`. |
| `BOOT_ORDER` | `hd` | Comma-separated boot device order (`cdrom`, `hd`, `network`). |
| `CLOUD_INIT` | `1` | Enable/disable cloud-init seed generation. |
| `CLOUD_INIT_USER_DATA` | *(unset)* | Path to an additional cloud-init payload file. Added as a second multipart section after the built-in configuration. |
| `ARCH` | `x86_64` | Guest architecture. Accepts `x86_64` (alias `amd64`) or `aarch64` (alias `arm64`). Defaults to the distribution's declared `arch` or `x86_64`. |
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

### Filesystem Sharing

| Variable | Default | Description |
| --- | --- | --- |
| `FILESYSTEM_SOURCE` | *(unset)* | Directory inside the container to expose to the guest (bind-mount a host path here). |
| `FILESYSTEM_TARGET` | *(unset)* | Guest-facing tag presented to the VM (mount with `mount -t virtiofs <tag> <path>`). |
| `FILESYSTEM_DRIVER` | `virtiofs` | Filesystem driver: `virtiofs` (default) or `9p` (falls back to virtio-9p). |
| `FILESYSTEM_ACCESSMODE` | `passthrough` | Access mode (`passthrough`, `mapped`, or `squash`). |
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
| `IPXE_ENABLE` | `0` | Inject an iPXE ROM on the virtio-net interface and prioritize `network` in the boot order. |
| `IPXE_ROM_PATH` | *(auto)* | Override the ROM (`/usr/lib/ipxe/qemu/pxe-virtio.rom` on x86_64, `efi-virtio.rom` on aarch64). Provide a full path when using a custom build. |

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
