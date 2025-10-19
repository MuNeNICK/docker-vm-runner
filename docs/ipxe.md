# iPXE Boot Guide

This guide covers the optional iPXE workflow for Docker-VM-Runner. When enabled, the container injects an iPXE ROM into the virtio-net interface so the guest can chainload a remote installer or boot script.

## Prerequisites

- `IPXE_ENABLE=1` must be set when starting the container.
- The image ships with iPXE ROMs via `ipxe-qemu`:  
  - `x86_64` → `/usr/lib/ipxe/qemu/pxe-virtio.rom`  
  - `aarch64` → `/usr/lib/ipxe/qemu/efi-virtio.rom`
- For realistic PXE/iPXE environments, prefer `NETWORK_MODE=bridge` or `NETWORK_MODE=direct` so the guest reaches your upstream DHCP/TFTP/HTTP services. User-mode NAT exposes only QEMU’s built-in DHCP/TFTP service.

## Basic Usage

```bash
docker run --rm -it \
  --name vm-netboot \
  --device /dev/kvm:/dev/kvm \
  -e IPXE_ENABLE=1 \
  -e BOOT_ORDER=network,hd \
  -e NETWORK_MODE=bridge \
  -e NETWORK_BRIDGE=br0 \
  ghcr.io/munenick/docker-vm-runner:latest
```

- `IPXE_ENABLE=1` injects the ROM and automatically promotes `network` to the highest boot priority.
- `BOOT_ORDER` can include `network`, `hd`, and `cdrom`. The manager ensures `network` is first whenever iPXE is enabled.
- With bridge or direct networking, the guest acquires an address from your upstream DHCP server and follows your TFTP/HTTP boot profile.

## Custom ROM Builds

Provide your own ROM by mounting it into the container and overriding `IPXE_ROM_PATH`:

```bash
docker run --rm -it \
  --name vm-netboot \
  --device /dev/kvm:/dev/kvm \
  -e IPXE_ENABLE=1 \
  -e IPXE_ROM_PATH=/images/ipxe/custom.rom \
  -v "$PWD/ipxe:/images/ipxe:ro" \
  ghcr.io/munenick/docker-vm-runner:latest
```

Override is mandatory when using an architecture that lacks a bundled ROM.

## Scripting and Chainloading

You can mix iPXE with cloud-init for post-boot configuration:

1. Host an iPXE script on your infrastructure.
2. Use an `#!ipxe` script to chainload a kernel, or to fetch installation media.
3. Provide cloud-init metadata (e.g., via `BOOT_ISO` or a blank disk with `cloud-init`) to finish provisioning once the OS installer completes.

Example iPXE script snippet:

```
#!ipxe
dhcp
set base-url http://boot.example.com/images/alpine
kernel ${base-url}/vmlinuz alpine_repo=${base-url}/repo
initrd ${base-url}/initramfs
boot
```

## Troubleshooting

- **ROM not found**: Confirm `ipxe-qemu` is installed (bundled in the Dockerfile) or use `IPXE_ROM_PATH` to supply your own file.
- **Boot loops to disk**: Ensure `BOOT_ORDER` includes `network` and that no other device is listed ahead of it after iPXE promotion.
- **No network**: Check bridge/direct permissions (`--cap-add NET_ADMIN` or `--privileged` for direct/macvtap), and confirm the upstream DHCP server is reachable.
- **User-mode NAT limitations**: iPXE relies on DHCP options that QEMU’s built-in server might not expose. For production use, switch to `NETWORK_MODE=bridge` or `direct`.
