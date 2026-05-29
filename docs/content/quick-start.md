# Quick Start

## Requirements

Run the container on a Linux host with Docker or a compatible container runtime.

The simplest mode requires:

- `--privileged`
- an interactive terminal with `-it`
- a persistent `/data` mount if you want to reuse the VM after the container exits

KVM is used when available. If KVM is not available, startup may be slower.

## Start a VM

```bash
docker run --rm -it --privileged \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

The runner resolves the default image, prepares storage, starts the VM, and attaches to the console.

## What to expect

On first run, the container may need to:

- load the image catalog
- download a boot image
- prepare the VM disk
- start the VM
- attach to the serial console

Later runs can reuse cached images and persisted VM state when `/data` is mounted.

## Detach from the console

Press:

```text
Ctrl+]
```

Detaching from the console does not necessarily mean the VM has shut down. Use the guest OS shutdown command when you want the VM to stop cleanly.

## Run without attaching to the console

```bash
docker run --rm --privileged \
  -e NO_CONSOLE=1 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Pick an image

List available images:

```bash
docker run --rm --privileged \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros
```

Search for an image:

```bash
docker run --rm --privileged \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros --search ubuntu
```

Run a specific image:

```bash
docker run --rm -it --privileged \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Preview configuration

Show the resolved configuration without starting the VM:

```bash
docker run --rm --privileged \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

Validate without starting:

```bash
docker run --rm --privileged \
  ghcr.io/munenick/docker-vm-runner:latest --dry-run
```
