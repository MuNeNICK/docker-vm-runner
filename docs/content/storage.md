# Storage

`docker-vm-runner` uses container storage unless you mount a volume. Mount `/data` when you want VM disks, image downloads, and runtime state to survive container removal.

## Temporary Storage

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  ghcr.io/munenick/docker-vm-runner:latest
```

This starts an ephemeral VM. When the container exits, the VM disk and downloaded image cache are removed.

## Persistent Storage

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Mount `/data` when you want the next run to reuse VM disks and image cache.

Use a different `GUEST_NAME` when you want multiple persistent VMs from the same image:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e GUEST_NAME=alpine-test-1 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Catalog Cache

Mount `/config` when you want to keep the image catalog cache:

```bash
docker run --rm \
  -v docker-vm-runner-config:/config \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros
```

## Disk Size

Set `DISK_SIZE` for the main VM disk:

```bash
docker run --rm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e DISK_SIZE=4G \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

`DISK_SIZE` also accepts `half` or `max`.

The disk is created under `/data` when `/data` is mounted. Without a `/data` mount, it is removed with the container.

## Extra Disks

Add extra VM disks with `DISK2_SIZE` through `DISK6_SIZE`:

```bash
docker run --rm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e DISK_TYPE=scsi \
  -e DISK2_SIZE=1G \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

Extra disks use the same disk controller as the main disk.

Choose the disk controller with `DISK_TYPE`:

```bash
-e DISK_TYPE=scsi
```

Supported values are `virtio`, `scsi`, `nvme`, `ide`, and `usb`.

## Filesystem Sharing

Filesystem sharing uses a container-visible path. Bind-mount the host directory into the container first, then point `FILESYSTEM_SOURCE` at that container path.

Create a host directory to share:

```bash
mkdir -p share
```

Preview a 9p share configuration:

```bash
docker run --rm \
  -v "$PWD/share:/shared" \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e FILESYSTEM_SOURCE=/shared \
  -e FILESYSTEM_TARGET=shared \
  -e FILESYSTEM_DRIVER=9p \
  -e FILESYSTEM_ACCESSMODE=mapped \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

The preview uses `9p` because `mapped` and `squash` access modes require `FILESYSTEM_DRIVER=9p`.

For the default virtiofs path, use `passthrough` access:

```bash
docker run --rm \
  -v "$PWD/share:/shared" \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e FILESYSTEM_SOURCE=/shared \
  -e FILESYSTEM_TARGET=shared \
  -e FILESYSTEM_DRIVER=virtiofs \
  -e FILESYSTEM_ACCESSMODE=passthrough \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

The guest still needs to mount the shared filesystem using the tag from `FILESYSTEM_TARGET`.

Inside the guest, mount the 9p example with:

```bash
sudo mkdir -p /mnt/shared
sudo mount -t 9p -o trans=virtio,version=9p2000.L shared /mnt/shared
```

Mount the virtiofs example with:

```bash
sudo mkdir -p /mnt/shared
sudo mount -t virtiofs shared /mnt/shared
```

## Host Block Devices

Attach a host block device when the VM must see the real device instead of a qcow2 file.

Expose the block device with Docker `--device`, then set `DEVICE` to the same container path. Additional devices use `DEVICE2` through `DEVICE6`.

Use an actual block device path. Do not point `DEVICE` at a mounted filesystem path.

See [Reference](reference.md#extra-disks-and-devices) and [Reference](reference.md#filesystem-sharing) for all storage variables.
