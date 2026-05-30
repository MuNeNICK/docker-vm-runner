# Reference

## CLI flags

| Flag | Description |
| --- | --- |
| `--no-console` | Do not attach to the VM console. |
| `--list-distros` | List available catalog images and exit. |
| `--arch ARCH` | Filter `--list-distros` by architecture. |
| `--type TYPE` | Filter `--list-distros` by image type: `cloud-image`, `iso`, or `disk-image`. |
| `--search TEXT` | Filter `--list-distros` by search text. |
| `--show-config` | Print the resolved configuration and exit. |
| `--show-xml` | Print the generated VM XML and exit. |
| `--dry-run` | Validate configuration without starting a VM. |
| `--cleanup` | Remove leftover VM resources and exit. |
| `--version` | Print version and exit. |

## guest-exec command

`guest-exec` runs a command inside the running VM through the QEMU guest agent.

```bash
docker exec <container> guest-exec [--wait] <command> [args...]
```

| Option | Description |
| --- | --- |
| `--wait` | Wait for the VM domain to appear and for the QEMU guest agent to become available before running the command. |

Examples:

```bash
docker exec ubuntu-vm guest-exec --wait "uname -a"
docker exec ubuntu-vm guest-exec --wait id user
```

A single command string containing spaces is run through `/bin/sh -c`. Multiple arguments are sent as argv form.

`guest-exec` streams stdout and stderr from the guest command and exits with the guest command's exit code.

Streaming is implemented through QEMU guest agent file polling, not a PTY. Command-side buffering can delay visible output, stdout/stderr ordering is not strict, and temporary guest output files are cleaned up on a best-effort basis if the VM or guest agent disconnects.

## Core environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `DISTRO` | `ubuntu-24.04-cloud-amd64` | Catalog image ID. |
| `ARCH` | catalog value | Select the architecture when the catalog image does not define one. If the catalog image already has an architecture, `ARCH` must match it. Common aliases such as `amd64` and `arm64` are accepted. |
| `GUEST_NAME` | derived | VM name used for persistent state and resource naming. |
| `PERSIST` | automatic | Persist VM state. Defaults to enabled when `/data` is mounted. |
| `MEMORY` | `4096` | VM memory. |
| `CPUS` | `2` | Number of vCPUs. |
| `DISK_SIZE` | `20G` | Working disk size. |
| `REQUIRE_KVM` | `0` | Fail when KVM is unavailable. |

## Boot and image variables

| Variable | Default | Description |
| --- | --- | --- |
| `BOOT_FROM` | empty | Boot from a URL, local path, OCI reference, ISO, or `blank`. |
| `BLANK_DISK` | automatic | Create a blank working disk. Automatically enabled for ISO boot. |
| `BOOT_ORDER` | `hd` | Comma-separated boot order using `hd`, `cdrom`, and `network`. |
| `BOOT_MODE` | `uefi` | Boot mode: `legacy`, `uefi`, or `secure`. |
| `FORCE_ISO` | `0` | Keep ISO media attached on the next boot. |
| `DOWNLOAD_RETRIES` | `3` | Download retry count. |
| `DOWNLOAD_MAX_SIZE` | `64G` | Maximum download size. |
| `EXTRACT_MAX_SIZE` | `512G` | Maximum extracted image size. |

## Catalog variables

| Variable | Default | Description |
| --- | --- | --- |
| `CATALOG_URL` | public catalog | Override the catalog URL or use a local file path. |
| `CATALOG_CACHE` | `/config/os-iso-catalog/v1/all.json` | Catalog cache path. |
| `CATALOG_OFFLINE` | `0` | Load only from `CATALOG_CACHE`. |

## Guest customization

| Variable | Default | Description |
| --- | --- | --- |
| `GUEST_PASSWORD` | `password` | Password for the default guest user. |
| `SSH_PUBKEY` | empty | SSH public key to inject into the guest. |
| `SHELL` | catalog value or `/bin/bash` | Login shell for the default guest user. |
| `CLOUD_INIT` | automatic | Enable or disable cloud-init. ISO boot disables it unless explicitly enabled. |
| `CLOUD_INIT_USER_DATA` | empty | Path to custom cloud-init user-data. |

## Access variables

| Variable | Default | Description |
| --- | --- | --- |
| `GRAPHICS` | `none` | Display mode: `none`, `vnc`, or `novnc`. |
| `VNC_PORT` | `5900` | VNC port inside the container. |
| `NOVNC_PORT` | `6080` | noVNC HTTPS port inside the container. |
| `VNC_KEYMAP` | empty | Optional VNC keymap. |
| `SSH_PORT` | `2222` | SSH forwarding port inside the container. |
| `PORT_FWD` | empty | Additional forwards as `host_port:guest_port`, comma-separated. |
| `NO_CONSOLE` | automatic | Skip serial console attachment. Automatically enabled when stdin/stdout is not a terminal or when `GRAPHICS=novnc`, unless explicitly set. |

## Redfish variables

| Variable | Default | Description |
| --- | --- | --- |
| `REDFISH_ENABLE` | `0` | Enable Redfish. |
| `REDFISH_USERNAME` | `admin` | Redfish username. |
| `REDFISH_PASSWORD` | `password` | Redfish password. Must be changed when Redfish is enabled. |
| `REDFISH_PORT` | `8443` | Redfish HTTPS port inside the container. |
| `REDFISH_SYSTEM_ID` | VM name | Redfish system identifier. |

## IPMI variables

| Variable | Default | Description |
| --- | --- | --- |
| `IPMI_ENABLE` | `0` | Enable IPMI through VirtualBMC. |
| `IPMI_USERNAME` | `admin` | IPMI username. |
| `IPMI_PASSWORD` | `password` | IPMI password. Must be changed when IPMI is enabled. |
| `IPMI_PORT` | `623` | IPMI UDP port inside the container. |
| `IPMI_SYSTEM_ID` | VM name | VirtualBMC domain identifier. |

## Network variables

The first NIC uses unnumbered variables. Additional NICs use `NETWORK2_...`, `NETWORK3_...`, and so on.

| Variable | Default | Description |
| --- | --- | --- |
| `NETWORK_MODE` | `nat` | Network mode: `nat`, `bridge`, or `direct`. |
| `NETWORK_BRIDGE` | empty | Bridge name when `NETWORK_MODE=bridge`. |
| `NETWORK_DIRECT_DEV` | empty | Host device when `NETWORK_MODE=direct`. |
| `NETWORK_MAC` | derived | MAC address. |
| `NETWORK_MODEL` | `virtio` | NIC model. |
| `NETWORK_MTU` | detected | MTU. |
| `NETWORK_GUEST_IP` | empty | Guest IPv4 address list. |
| `NETWORK_GUEST_IP6` | empty | Guest IPv6 address list. |
| `NETWORK_BOOT` | `0` | Mark this NIC as bootable. |
| `IPXE_ENABLE` | `0` | Enable iPXE boot on the primary NIC. |
| `IPXE_ROM_PATH` | automatic | Override iPXE ROM path. |

## Hardware variables

| Variable | Default | Description |
| --- | --- | --- |
| `CPU_MODEL` | `host` | CPU model. |
| `MACHINE` | architecture default | Machine type. Supported override values are `q35` and `pc`. |
| `DISK_TYPE` | `virtio` | Disk controller: `virtio`, `scsi`, `nvme`, `ide`, or `usb`. |
| `DISK_IO` | `native` | Disk I/O mode: `native`, `threads`, or `io_uring`. |
| `DISK_CACHE` | `none` | Disk cache mode. |
| `ALLOCATE` | `0` | Preallocate disk space. |
| `TPM` | secure boot default | Enable TPM. |
| `GPU` | `off` | GPU setting. Supported values: `off`, `intel`. |
| `USB` | `1` | Enable USB controller. |
| `HYPERV` | `0` | Enable Hyper-V enlightenments. |
| `BALLOON` | `1` | Enable memory balloon device. |
| `RNG` | `1` | Enable random number generator device. |
| `IO_THREAD` | `1` | Enable disk I/O thread. |
| `EXTRA_ARGS` | empty | Extra guest command-line arguments. |

## Extra disks and devices

| Variable | Description |
| --- | --- |
| `DISK2_SIZE` through `DISK6_SIZE` | Add extra VM disks. |
| `DEVICE` through `DEVICE6` | Attach host block devices. |

## Filesystem sharing

The first share uses unnumbered variables. Additional shares use `FILESYSTEM2_...`, `FILESYSTEM3_...`, and so on.

| Variable | Default | Description |
| --- | --- | --- |
| `FILESYSTEM_SOURCE` | empty | Container-visible directory to share. Bind-mount a host directory into the container first when sharing host files. |
| `FILESYSTEM_TARGET` | basename of source | Guest mount tag. |
| `FILESYSTEM_DRIVER` | `virtiofs` | `virtiofs` or `9p`. |
| `FILESYSTEM_ACCESSMODE` | `passthrough` | `passthrough`, `mapped`, or `squash`. `virtiofs` supports only `passthrough`; use `FILESYSTEM_DRIVER=9p` for `mapped` or `squash`. |
| `FILESYSTEM_READONLY` | `0` | Mark the share read-only. |

## Common ports

| Port | Used by |
| --- | --- |
| `2222` | SSH forwarding default. |
| `5900` | VNC default. |
| `6080` | noVNC default. |
| `8443` | Redfish default. |

Publish container ports with Docker `-p` when you want to access them from the host.

## Common mounts

| Container path | Purpose |
| --- | --- |
| `/data` | Persistent VM disks, image cache, and state. |
| `/config` | Catalog cache. |
| Custom path | Mount local boot media for `BOOT_FROM`. |

## Common Docker Options

The VM startup examples use `--device /dev/kvm` for better performance. Remove it on hosts without KVM.

| Need | Typical Docker option |
| --- | --- |
| KVM acceleration | `--device /dev/kvm` |
| Host block device passthrough | `--device /dev/<device>` |
| Intel GPU acceleration with `GPU=intel` | `--device /dev/dri/renderD128` |
| Local boot media or cloud-init files | `-v /host/path:/container/path:ro` |
