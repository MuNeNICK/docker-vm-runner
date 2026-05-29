# Overview

`docker-vm-runner` starts a real virtual machine from a Docker container.

The goal is a simple user experience: run a command that feels close to `docker run ubuntu`, but get an actual VM with its own kernel, firmware, disks, and console.

## Why use it

Use `docker-vm-runner` when a normal container is not enough:

- You need to test a full operating system image.
- You need systemd, kernel behavior, firmware, boot media, or disk layout behavior.
- You want to run cloud images or installer ISOs without writing libvirt or QEMU commands by hand.
- You want a repeatable Docker-based workflow for VM startup.

## What you get

- A VM started from a public catalog image by default.
- Console access by default.
- NAT networking by default.
- Optional SSH, VNC, noVNC, and Redfish access.
- Boot from catalog images, custom disks, installer ISOs, blank disks, or iPXE.
- Persistent VM state when `/data` is mounted.
- Disk sizing, extra disks, and filesystem sharing configuration.
- Support for cloud images, installer ISOs, disk images, local boot media, remote URLs, and OCI references.

## Image

Use the public image:

```text
ghcr.io/munenick/docker-vm-runner:latest
```

## Next step

Start with [Quick Start](quick-start.md) to boot your first VM, jump to [Use Cases](use-cases.md) for task-oriented commands, or use [Examples](examples.md) for complete runnable files.

Use [Agent Skills](agent-skills.md) when you want Codex, Claude Code, or OpenCode to operate `docker-vm-runner` as a VM sandbox.
