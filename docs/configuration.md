# Configuration

Configuration is passed through environment variables and CLI flags. Environment variables describe the VM. CLI flags control runner behavior such as listing, dry-run, cleanup, or console attachment.

## Core VM settings

| Variable | Default | Description |
| --- | --- | --- |
| `DISTRO` | `ubuntu-24.04-cloud-amd64` | Catalog image ID to boot. |
| `ARCH` | catalog value | Override architecture when the catalog entry supports it. |
| `MEMORY` | `4096` | VM memory in MiB or a size value. |
| `CPUS` | `2` | vCPU count. |
| `DISK_SIZE` | `20G` | Working disk size. |
| `VM_NAME` | derived | Stable libvirt domain and state name. |
| `PERSIST` | automatic | Persist VM state. Defaults to enabled when `/data` is mounted. |

## Boot media

| Variable | Default | Description |
| --- | --- | --- |
| `BOOT_FROM` | empty | Boot from a URL, local path, OCI reference, `blank`, or catalog ISO URL. |
| `BLANK_DISK` | automatic | Create a blank working disk. Automatically enabled for ISO boot. |
| `BOOT_ORDER` | `hd` | Boot order. ISO boot moves `cdrom` first. |
| `BOOT_MODE` | `uefi` | `legacy`, `uefi`, or `secure`. |
| `FORCE_ISO` | `0` | Keep ISO attached when applicable. |

Catalog entries with `image_type=iso` are treated as installer media even when the downloaded cache filename does not end in `.iso`.

## Guest access

| Variable | Default | Description |
| --- | --- | --- |
| `GUEST_PASSWORD` | `password` | Default guest password for generated cloud-init users. |
| `SSH_PUBKEY` | empty | SSH public key injected through cloud-init. |
| `SHELL` | catalog value or `/bin/bash` | Login shell for generated users. |
| `CLOUD_INIT` | automatic | Enable or disable cloud-init. ISO boot disables it unless explicitly enabled. |
| `USER_DATA` | empty | Path to custom cloud-init user-data. |

## Hardware and firmware

| Variable | Default | Description |
| --- | --- | --- |
| `CPU_MODEL` | `host` | libvirt CPU model. |
| `MACHINE` | architecture default | libvirt machine type. |
| `DISK_TYPE` | `virtio` | Disk controller. |
| `DISK_IO` | `native` | Disk I/O mode. |
| `DISK_CACHE` | `none` | Disk cache mode. |
| `ALLOCATE` | `0` | Preallocate disk space. |
| `TPM` | secure boot default | Enable TPM. Secure boot enables it by default. |
| `GPU` | `off` | GPU passthrough setting. |
| `USB` | `1` | Enable USB controller. |
| `HYPERV` | `0` | Enable Hyper-V enlightenments. |
| `BALLOON` | `1` | Enable virtio balloon. |
| `RNG` | `1` | Enable virtio RNG. |
| `IO_THREAD` | `1` | Enable disk I/O thread. |
| `REQUIRE_KVM` | `0` | Fail instead of falling back when KVM is unavailable. |

## Limits and downloads

| Variable | Default | Description |
| --- | --- | --- |
| `DOWNLOAD_RETRIES` | `3` | Download retry count. |
| `DOWNLOAD_MAX_SIZE` | `64G` | Maximum downloaded size. |
| `EXTRACT_MAX_SIZE` | `512G` | Maximum extracted image size. |

## CLI flags

| Flag | Description |
| --- | --- |
| `--no-console` | Do not attach to the VM console. |
| `--list-distros` | List catalog images and exit. |
| `--arch` | Filter `--list-distros` by architecture. |
| `--type` | Filter `--list-distros` by `cloud-image`, `iso`, or `disk-image`. |
| `--search` | Filter `--list-distros` by text. |
| `--show-config` | Print resolved configuration and exit. |
| `--show-xml` | Print libvirt XML and exit. |
| `--dry-run` | Validate without starting a VM. |
| `--cleanup` | Cleanup stale VM resources and exit. |
| `--version` | Print version and exit. |
