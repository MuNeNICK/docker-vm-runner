# Boot

Boot settings control what the VM starts from: a catalog disk, an installer ISO, a local image, a blank disk, or a network boot path.

## Default Boot

By default, `docker-vm-runner` starts the catalog image selected by `DISTRO`:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Cloud images usually boot directly from disk and use cloud-init for the default user, password, and SSH key setup.

## Boot From A Custom Source

Use `BOOT_FROM` for a URL, local path, OCI reference, ISO, disk image, or `blank`.

Remote disk image:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=https://example.com/image.qcow2 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Local disk image:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -v "$PWD/images:/boot-media:ro" \
  -e BOOT_FROM=/boot-media/image.qcow2 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Blank disk:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=blank \
  -e DISK_SIZE=40G \
  -v blank-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## ISO Install

Use an ISO when you want to run an installer. ISO boot creates a blank working disk by default and puts `cdrom` first in the boot order.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=https://example.com/installer.iso \
  -e GUEST_NAME=installed-vm \
  -e CPUS=4 \
  -e MEMORY=8192 \
  -e DISK_SIZE=80G \
  -v installed-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

When an ISO-backed persistent VM stops after installation, `docker-vm-runner` treats it as installed. The next run reuses the VM disk and boots from disk instead of attaching the ISO again.

Keep the ISO attached when you need to boot the installer or rescue media again:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=https://example.com/installer.iso \
  -e FORCE_ISO=1 \
  -v installed-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Cloud-init is disabled automatically for ISO boot. Set `CLOUD_INIT=1` only when the ISO workflow expects cloud-init data.

## Boot Order

Use `BOOT_ORDER` to choose the boot priority. Supported values are `hd`, `cdrom`, and `network`.

```bash
docker run --rm \
  -e BOOT_ORDER=network,hd \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

When `BOOT_FROM` points to an ISO, `cdrom` is automatically added before the disk unless you choose a different flow after installation.

## Boot Mode

Use `BOOT_MODE` for firmware selection:

| Value | Use case |
| --- | --- |
| `uefi` | Default modern boot mode. |
| `legacy` | BIOS-style boot for older images or network boot environments. |
| `secure` | Secure Boot with TPM enabled by default. |

Example:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e BOOT_MODE=secure \
  -v secure-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## iPXE Network Boot

Use iPXE when the VM should network boot from your DHCP/TFTP/HTTP infrastructure.

```bash
docker run --rm -it \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/kvm \
  --device /dev/net/tun \
  --device /dev/vhost-net \
  -e IPXE_ENABLE=1 \
  -e BOOT_ORDER=network,hd \
  -e NETWORK_MODE=bridge \
  -e NETWORK_BRIDGE=br0 \
  -e GUEST_NAME=ipxe-vm \
  -v ipxe-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

`IPXE_ENABLE=1` injects an iPXE ROM into the primary NIC and moves `network` to the front of the boot order.

Bridge, Container, or Direct networking is the practical choice for real PXE
environments because the guest needs to reach your upstream DHCP/TFTP/HTTP
services. NAT can be used for simple tests, but it relies on user-mode
networking and is usually not the right path for infrastructure PXE.

## Custom iPXE ROM

The image includes iPXE ROMs from `ipxe-qemu` for common NIC models. Provide `IPXE_ROM_PATH` when you want to use your own ROM or an architecture/model combination without a bundled ROM.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -v "$PWD/ipxe:/ipxe:ro" \
  -e IPXE_ENABLE=1 \
  -e IPXE_ROM_PATH=/ipxe/custom.rom \
  -e GUEST_NAME=ipxe-vm \
  -v ipxe-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

The default ROM is selected from the VM architecture and `NETWORK_MODEL`. For example, x86_64 with the default `virtio` NIC uses `/usr/share/qemu/pxe-virtio.rom`.

## iPXE Script Example

Host an iPXE script on your network and point your DHCP/TFTP environment at it. A minimal script can chainload a kernel and initramfs:

```text
#!ipxe
dhcp
set base-url http://boot.example.com/images/alpine
kernel ${base-url}/vmlinuz alpine_repo=${base-url}/repo
initrd ${base-url}/initramfs
boot
```
