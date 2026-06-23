# Boot, Networking, and Storage

Use `--show-config`, `--show-xml`, or `--dry-run` before starting VMs with
custom boot, networking, or storage settings.

## Images and boot sources

List and search catalog images:

```bash
docker run --rm ghcr.io/munenick/docker-vm-runner:latest --list-distros
docker run --rm ghcr.io/munenick/docker-vm-runner:latest --list-distros --search ubuntu
docker run --rm ghcr.io/munenick/docker-vm-runner:latest --list-distros --type cloud-image
docker run --rm ghcr.io/munenick/docker-vm-runner:latest --list-distros --type iso
docker run --rm ghcr.io/munenick/docker-vm-runner:latest --list-distros --type disk-image
```

Run a catalog image:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Use `BOOT_FROM` for a URL, local path, OCI reference, ISO, disk image, or
`blank`:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=https://example.com/image.qcow2 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

For local boot media, bind-mount it into the container first:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -v "$PWD/images:/boot-media:ro" \
  -e BOOT_FROM=/boot-media/image.qcow2 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## ISO install and rescue

ISO boot creates a blank working disk by default and puts `cdrom` first in the
boot order:

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

When an ISO-backed persistent VM stops after installation, the next run reuses
the VM disk and boots from disk. Use `FORCE_ISO=1` for rescue media, live media,
or repeated installer boots.

Use `BOOT_MODE=secure` for Secure Boot. Secure mode enables TPM by default.
Use `BOOT_MODE=legacy` for BIOS-style boot.

## Networking

Default NAT keeps the host network unchanged and supports `SSH_PORT` and
`PORT_FWD`.

Add a second bridged NIC while keeping NAT for management:

```bash
docker run --rm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e NETWORK_MODE=nat \
  -e NETWORK2_MODE=bridge \
  -e NETWORK2_BRIDGE=docker0 \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

Bridge mode requires an existing bridge and Docker options that allow tap
creation:

```bash
docker run -d --name bridge-vm \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/kvm \
  --device /dev/net/tun \
  --device /dev/vhost-net \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e NETWORK_MODE=bridge \
  -e NETWORK_BRIDGE=br0 \
  -e NO_CONSOLE=1 \
  -v bridge-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Container mode attaches the VM NIC to a Docker network interface that is visible
inside the runner container:

```bash
docker run -d --name container-net-vm \
  --network provisioning-net \
  --cap-add NET_ADMIN \
  --device /dev/kvm \
  --device /dev/net/tun \
  --device /dev/vhost-net \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e NETWORK_MODE=container \
  -e NETWORK_CONTAINER_INTERFACE=eth0 \
  -e NO_CONSOLE=1 \
  -v container-net-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Direct mode attaches a VM NIC to a host interface through macvtap:

```bash
MACVTAP_MAJOR="$(awk '$2 == "macvtap" { print $1 }' /proc/devices)"

docker run -d --name direct-vm \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/kvm \
  --device /dev/vhost-net \
  --device-cgroup-rule "c ${MACVTAP_MAJOR}:* rwm" \
  -v /dev:/dev:ro \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e NETWORK_MODE=direct \
  -e NETWORK_DIRECT_DEV=eth0 \
  -e NO_CONSOLE=1 \
  -v direct-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## iPXE

Use iPXE when the VM should boot from DHCP/TFTP/HTTP infrastructure:

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

Bridge or Direct is the practical choice for real PXE infrastructure. Provide
`IPXE_ROM_PATH` when using a custom ROM or an architecture/model combination
without a bundled ROM.

## Storage

Mount `/data` for persistent VM disks, state, and image cache:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Mount `/config` to keep the catalog cache:

```bash
docker run --rm \
  -v docker-vm-runner-config:/config \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros
```

Add extra disks with `DISK2_SIZE` through `DISK6_SIZE`:

```bash
docker run --rm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e DISK_TYPE=scsi \
  -e DISK2_SIZE=10G \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

Filesystem sharing uses a container-visible path. Bind-mount the host directory
into the container, then point `FILESYSTEM_SOURCE` at that path:

```bash
mkdir -p share

docker run --rm -it \
  --device /dev/kvm \
  -v "$PWD/share:/shared" \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e FILESYSTEM_SOURCE=/shared \
  -e FILESYSTEM_TARGET=shared \
  -e FILESYSTEM_DRIVER=9p \
  -e FILESYSTEM_ACCESSMODE=mapped \
  -v shared-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Inside the guest:

```bash
sudo mkdir -p /mnt/shared
sudo mount -t 9p -o trans=virtio,version=9p2000.L shared /mnt/shared
```

Host block devices require the matching Docker `--device` option and `DEVICE`
or `DEVICE2` through `DEVICE6`. Use a real block device path, not a mounted
filesystem path.
