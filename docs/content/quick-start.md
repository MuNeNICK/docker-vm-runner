# Quick Start

## Requirements

Run the container on a Linux host with Docker or a compatible container runtime.

The examples below use `/dev/kvm` for better VM performance. If your host does not have `/dev/kvm`, remove `--device /dev/kvm`.

## Start an ephemeral VM

```bash
docker run --rm -it \
  --name docker-vm-runner \
  --device /dev/kvm \
  ghcr.io/munenick/docker-vm-runner:latest
```

This starts a temporary VM and opens the VM console in your terminal. When the container exits, the VM disk and downloaded image cache are removed.

## Start a persistent VM

```bash
docker run --rm -it \
  --name docker-vm-runner \
  --device /dev/kvm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Use this form when you want the next run to reuse the VM disk and image cache.

## First startup

On first run, the container may need to:

- load the image catalog
- download a boot image
- prepare the VM disk
- start the VM
- attach to the serial console

Persistent runs can reuse cached images and VM state when `/data` is mounted.

## Leave the VM running

If you started the container with an attached terminal, press `Ctrl+P` then `Ctrl+Q` to leave the VM running.

Reconnect later:

```bash
docker attach docker-vm-runner
```

`Ctrl+]` exits the VM console session. Do not use it when you want the VM to keep running in the background.

## Run in the background

Use `-dit` when you want the VM to keep running in the background and still reconnect with `docker attach`:

```bash
docker run -dit --name docker-vm-runner \
  --device /dev/kvm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Follow the startup logs:

```bash
docker logs -f docker-vm-runner
```

Reconnect to the VM console:

```bash
docker attach docker-vm-runner
```

When you are attached through Docker, press `Ctrl+P` then `Ctrl+Q` to leave the VM running.

## Pick an image

List available images:

```bash
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros
```

Search for an image:

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

## Preview configuration

Show the resolved configuration without starting the VM:

```bash
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

Validate without starting:

```bash
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --dry-run
```
