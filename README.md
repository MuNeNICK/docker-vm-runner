# docker-vm-runner

Run a real virtual machine from Docker.

`docker-vm-runner` is for workflows where a normal container is not enough: full OS images, systemd, kernel behavior, firmware, installer media, disk layout testing, or VM access through SSH, VNC, noVNC, Redfish, and IPMI.

The default experience is intentionally close to `docker run ubuntu`, but the guest is an actual VM backed by QEMU and libvirt.

## Quick Start

Start a temporary VM and attach to its serial console:

```bash
docker run --rm -it \
  --name docker-vm-runner \
  --device /dev/kvm \
  ghcr.io/munenick/docker-vm-runner:latest
```

`/dev/kvm` enables hardware acceleration. If it is not available, remove `--device /dev/kvm` to use software emulation.

Detach from the VM without stopping it by pressing `Ctrl+P` then `Ctrl+Q`.

`docker-vm-runner` uses [os-iso-catalog](https://github.com/MuNeNiCK/os-iso-catalog), a public OS image catalog with many Linux and BSD entries across cloud images, installer ISOs, and disk images.

Search the catalog:

```bash
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros --search ubuntu
```

Run a specific image:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

List all catalog images:

```bash
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros
```

For persistent disks, background startup, SSH, noVNC, Redfish, ISO installs, storage, network examples, and image selection details, see [Quick Start](docs/content/quick-start.md), [Use Cases](docs/content/use-cases.md), and [Images](docs/content/images.md).

## Features

- Docker-first VM startup with QEMU and libvirt.
- Public image catalog support for cloud images, installer ISOs, and disk images.
- Boot from catalog entries, custom URLs, local media, OCI disk images, blank disks, ISO installers, and iPXE.
- Serial console by default, plus optional SSH, VNC, noVNC, Redfish, and IPMI access.
- Cloud-init user data, SSH key injection, and fixed default guest credentials.
- Persistent VM state and image cache through `/data`.
- NAT networking by default, with optional port forwarding, Bridge, Direct, and multi-NIC setups.
- Disk sizing, extra disks, filesystem sharing, and host block-device attachment.
- `guest-exec` support through the QEMU guest agent.

## Host Platform Support

| Host | Support level | Notes |
| --- | --- | --- |
| Linux | Primary target | Supports the default NAT and console flow. `/dev/kvm` is recommended. Bridge, Direct networking, host block devices, GPU, USB, TPM, and filesystem sharing depend on the matching host devices, mounts, and Docker options. |
| macOS | Limited | Linux containers run inside a VM provided by the selected container environment. `/dev/kvm` is not available from the macOS host, so hardware-accelerated guests are not expected to work. Default NAT with published ports may be usable; host NIC modes such as Bridge and Direct are not a supported target. |
| Windows | Limited | Use Linux containers, not native Windows containers. KVM availability depends on the selected container environment, commonly WSL2-backed. Default NAT with published ports may be usable; Bridge and Direct host NIC modes are not a supported target. |

Published container images are built for `linux/amd64` and `linux/arm64`. Choose a guest image that matches the host architecture unless you intentionally want slower emulation.

## Documentation

- [Quick Start](docs/content/quick-start.md)
- [Use Cases](docs/content/use-cases.md)
- [Images](docs/content/images.md)
- [Boot](docs/content/boot.md)
- [Networking](docs/content/networking.md)
- [Storage](docs/content/storage.md)
- [Access](docs/content/access.md)
- [Operations](docs/content/operations.md)
- [Reference](docs/content/reference.md)

## License

MIT License
