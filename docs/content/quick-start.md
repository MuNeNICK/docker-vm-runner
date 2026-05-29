# Quick Start

## Requirements

Run the container on a Linux host with Docker or a compatible container runtime.

For the default interactive flow, use:

- an interactive terminal with `-it`
- a `/data` mount if you want to reuse the VM after the container exits

The VM examples below expose `/dev/kvm` for hardware acceleration. Remove `--device /dev/kvm` if your host does not provide KVM.

Start without `--privileged` for the default NAT and console flow. Add broader permissions only for features that need them.

## Start a VM

```bash
docker run --rm -it \
  --device /dev/kvm \
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

Start the container in the background when you want the VM to keep running while you use another terminal:

```bash
docker run -d --name docker-vm-runner \
  --device /dev/kvm \
  -e NO_CONSOLE=1 \
  -e VM_NAME=quickstart \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Follow the startup logs:

```bash
docker logs -f docker-vm-runner
```

Attach to the VM console from inside the running container:

```bash
docker exec -it docker-vm-runner virsh -c qemu:///system console quickstart
```

Replace `quickstart` with your `VM_NAME` when you use a different VM name. Press `Ctrl+]` to detach from the VM console.

`docker attach docker-vm-runner` reconnects to the container process, not directly to a VM console that was skipped with `NO_CONSOLE=1`. Use `docker exec ... virsh console` for a headless container.

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
