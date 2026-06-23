# Public Options

Use these options when constructing `docker-vm-runner` commands. This list is
limited to public, documented workflow options.

## CLI flags

| Flag | Use |
| --- | --- |
| `--no-console` | Do not attach to the VM console. |
| `--list-distros` | List catalog images and exit. |
| `--arch ARCH` | Filter `--list-distros` by architecture. |
| `--type TYPE` | Filter `--list-distros` by `cloud-image`, `iso`, or `disk-image`. |
| `--search TEXT` | Filter `--list-distros` by text. |
| `--show-config` | Print resolved configuration and exit. |
| `--show-xml` | Print generated VM XML and exit. |
| `--dry-run` | Validate configuration without starting a VM. |
| `--cleanup` | Remove leftover VM resources and exit. |
| `--version` | Print version and exit. |

## guest-exec

```bash
docker exec <container> guest-exec [--wait] <command> [args...]
```

| Option | Use |
| --- | --- |
| `--wait` | Wait for the QEMU guest agent before running the command. |

## Core VM

| Variable | Default | Use |
| --- | --- | --- |
| `DISTRO` | `ubuntu-24.04-cloud-amd64` | Catalog image ID. |
| `ARCH` | catalog value | Select architecture when the catalog entry does not define one. |
| `GUEST_NAME` | derived | Stable VM name for persistent state and resources. |
| `PERSIST` | automatic | Persist VM state; defaults on when `/data` is mounted. |
| `MEMORY` | `4096` | VM memory in MiB; also accepts `half` or `max`. |
| `CPUS` | `2` | vCPU count; also accepts `half` or `max`. |
| `DISK_SIZE` | `20G` | Main disk size; also accepts `half` or `max`. |
| `REQUIRE_KVM` | `0` | Fail instead of falling back when KVM is unavailable. |

## Boot and images

| Variable | Default | Use |
| --- | --- | --- |
| `BOOT_FROM` | empty | Boot from URL, local path, OCI reference, ISO, disk image, or `blank`. |
| `BLANK_DISK` | automatic | Create a blank working disk; enabled automatically for ISO boot. |
| `BOOT_ORDER` | `hd` | Comma-separated order using `hd`, `cdrom`, and `network`. |
| `BOOT_MODE` | `uefi` | Firmware mode: `legacy`, `uefi`, or `secure`. |
| `FORCE_ISO` | `0` | Keep ISO media attached on later boots. |
| `DOWNLOAD_RETRIES` | `3` | Image download retry count. |
| `DOWNLOAD_MAX_SIZE` | `64G` | Maximum download size. |
| `EXTRACT_MAX_SIZE` | `512G` | Maximum extracted image size. |
| `CATALOG_URL` | public catalog | Override catalog URL or use a local catalog file. |
| `CATALOG_CACHE` | `/config/os-iso-catalog/v1/all.json` | Catalog cache path. |
| `CATALOG_OFFLINE` | `0` | Load only from `CATALOG_CACHE`. |

## Guest customization

| Variable | Default | Use |
| --- | --- | --- |
| `GUEST_PASSWORD` | `password` | Password for the default guest user. |
| `SSH_PUBKEY` | empty | SSH public key to inject into the guest. |
| `SHELL` | catalog value or `/bin/bash` | Login shell for the default guest user. |
| `CLOUD_INIT` | automatic | Enable or disable cloud-init; ISO boot disables it unless explicitly enabled. |
| `CLOUD_INIT_USER_DATA` | empty | Path to custom cloud-init user-data. |

## Access

| Variable | Default | Use |
| --- | --- | --- |
| `GRAPHICS` | `none` | Display mode: `none`, `vnc`, or `novnc`. |
| `VNC_PORT` | `5900` | VNC port inside the container. |
| `NOVNC_PORT` | `6080` | noVNC HTTPS port inside the container. |
| `VNC_KEYMAP` | empty | Optional VNC keymap. |
| `SSH_PORT` | `2222` | SSH forwarding port inside the container. |
| `PORT_FWD` | empty | Extra NAT forwards as `container_port:guest_port`, comma-separated. |
| `NO_CONSOLE` | automatic | Skip serial console attachment. |

## Redfish

| Variable | Default | Use |
| --- | --- | --- |
| `REDFISH_ENABLE` | `0` | Enable Redfish. |
| `REDFISH_USERNAME` | `admin` | Redfish username. |
| `REDFISH_PASSWORD` | `password` | Redfish password; must be changed when Redfish is enabled. |
| `REDFISH_PORT` | `8443` | Redfish HTTPS port inside the container. |
| `REDFISH_SYSTEM_ID` | VM name | Redfish system identifier. |

## Networking

The first NIC uses unnumbered variables. Additional NICs use `NETWORK2_...`,
`NETWORK3_...`, and so on.

| Variable | Default | Use |
| --- | --- | --- |
| `NETWORK_MODE` | `nat` | NIC mode: `nat`, `bridge`, `container`, or `direct`. |
| `NETWORK_BRIDGE` | empty | Bridge name when `NETWORK_MODE=bridge`. |
| `NETWORK_CONTAINER_INTERFACE` | empty | Container-visible interface to attach to a VM bridge when `NETWORK_MODE=container`. |
| `NETWORK_CONTAINER_BRIDGE` | generated | Linux bridge name created inside the container when `NETWORK_MODE=container`. |
| `NETWORK_DIRECT_DEV` | empty | Host device when `NETWORK_MODE=direct`. |
| `NETWORK_MAC` | derived | Stable MAC address. |
| `NETWORK_MODEL` | `virtio` | NIC model. |
| `NETWORK_MTU` | detected | NIC MTU. |
| `NETWORK_GUEST_IP` | empty | Guest IPv4 address list for NAT mode. |
| `NETWORK_GUEST_IP6` | empty | Guest IPv6 address list for NAT mode. |
| `NETWORK_BOOT` | `0` | Mark this NIC as bootable. |
| `IPXE_ENABLE` | `0` | Enable iPXE boot on the primary NIC. |
| `IPXE_ROM_PATH` | automatic | Override iPXE ROM path. |

## Hardware

| Variable | Default | Use |
| --- | --- | --- |
| `CPU_MODEL` | `host` | CPU model. |
| `MACHINE` | architecture default | Machine type; documented overrides are `q35` and `pc`. |
| `DISK_TYPE` | `virtio` | Disk controller: `virtio`, `scsi`, `nvme`, `ide`, or `usb`. |
| `DISK_IO` | `native` | Disk I/O mode: `native`, `threads`, or `io_uring`. |
| `DISK_CACHE` | `none` | Disk cache mode. |
| `ALLOCATE` | `0` | Preallocate disk space. |
| `TPM` | secure boot default | Enable TPM. |
| `GPU` | `off` | GPU setting; documented value is `intel`. |
| `USB` | `1` | Enable USB controller. |
| `HYPERV` | `0` | Enable Hyper-V enlightenments. |
| `BALLOON` | `1` | Enable memory balloon device. |
| `RNG` | `1` | Enable random number generator device. |
| `IO_THREAD` | `1` | Enable disk I/O thread. |
| `EXTRA_ARGS` | empty | Extra guest command-line arguments. |

## Storage and devices

| Variable | Use |
| --- | --- |
| `DISK2_SIZE` through `DISK6_SIZE` | Add extra VM disks. |
| `DEVICE` through `DEVICE6` | Attach host block devices. |

The first filesystem share uses unnumbered variables. Additional shares use
`FILESYSTEM2_...`, `FILESYSTEM3_...`, and so on.

| Variable | Default | Use |
| --- | --- | --- |
| `FILESYSTEM_SOURCE` | empty | Container-visible directory to share. |
| `FILESYSTEM_TARGET` | basename of source | Guest mount tag. |
| `FILESYSTEM_DRIVER` | `virtiofs` | `virtiofs` or `9p`. |
| `FILESYSTEM_ACCESSMODE` | `passthrough` | `passthrough`, `mapped`, or `squash`. |
| `FILESYSTEM_READONLY` | `0` | Mark the share read-only. |

## Docker options and ports

| Need | Docker option |
| --- | --- |
| KVM acceleration | `--device /dev/kvm` |
| Host block device passthrough | `--device /dev/<device>` |
| Intel GPU with `GPU=intel` | `--device /dev/dri/renderD128` |
| Local boot media or user-data | `-v /host/path:/container/path:ro` |
| Persistent VM state and image cache | `-v <volume>:/data` |
| Catalog cache | `-v <volume>:/config` |
| SSH | `-p 2222:2222` by default |
| VNC | `-p 5900:5900` by default |
| noVNC | `-p 6080:6080` by default |
| Redfish | `-p 8443:8443` by default |
