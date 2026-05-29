# Getting Started

## Build the image

From the repository root:

```bash
docker build -t docker-vm-runner:dev .
```

## Start the default VM

```bash
docker run --rm -it --privileged \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

The container starts libvirt services, prepares the VM disk, starts the VM, and attaches to the serial console.

Press `Ctrl+]` to detach from the console. The VM may keep running depending on its state and persistence settings.

## Select an image

List available catalog entries:

```bash
docker run --rm --privileged docker-vm-runner:dev --list-distros
```

Filter by image type:

```bash
docker run --rm --privileged docker-vm-runner:dev --list-distros --type cloud-image
docker run --rm --privileged docker-vm-runner:dev --list-distros --type iso
docker run --rm --privileged docker-vm-runner:dev --list-distros --type disk-image
```

Search the catalog:

```bash
docker run --rm --privileged docker-vm-runner:dev --list-distros --search ubuntu
```

Run a specific catalog image:

```bash
docker run --rm -it --privileged \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

## Inspect before starting

Print the resolved configuration:

```bash
docker run --rm --privileged docker-vm-runner:dev --show-config
```

Print the libvirt domain XML:

```bash
docker run --rm --privileged docker-vm-runner:dev --show-xml
```

Validate configuration without starting a VM:

```bash
docker run --rm --privileged docker-vm-runner:dev --dry-run
```
