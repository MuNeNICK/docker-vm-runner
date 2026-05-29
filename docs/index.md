# docker-vm-runner

`docker-vm-runner` runs a real VM from a Docker container while keeping the user-facing workflow close to a normal `docker run`.

It is intended for cases where a container is not enough: kernel behavior, systemd, disk images, installers, firmware, libvirt, and Redfish-style control all need an actual virtual machine.

## What it does

- Starts QEMU/KVM virtual machines through libvirt inside the container.
- Uses `os-iso-catalog` dynamically for cloud images, installer ISOs, and disk images.
- Downloads, validates, extracts, converts, and caches boot media.
- Supports interactive serial console by default.
- Supports noVNC, VNC, SSH port forwarding, filesystem sharing, extra disks, TPM, secure boot, and Redfish via sushy-tools.
- Persists VM state when `/data` is mounted or `DATA_DIR` is configured.

## Default experience

The default command starts a VM from the default catalog image and attaches to the VM console:

```bash
docker run --rm -it --privileged \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

For a detached or web-console workflow, use `NO_CONSOLE=1` or `GRAPHICS=novnc`.

## Project status

This documentation is written for the Go-based v2 implementation. It is not a line-by-line migration of the previous Python documentation.
