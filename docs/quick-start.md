# Quick Start

Run lightweight QEMU VMs from the published container image (`ghcr.io/munenick/docker-vm-runner:latest`). Each container hosts a single VM orchestrated by libvirt and sushy for Redfish management.

## One-Shot, Ephemeral VM

```bash
docker run --rm -it \
  --name vm1 \
  --hostname vm1 \
  -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  ghcr.io/munenick/docker-vm-runner:latest
```

- SSH: `ssh -p 2222 <user>@localhost` (user defaults to the image’s `login_user`).
- Redfish (when enabled): `https://localhost:8443` (default credentials `admin` / `password`).

To launch a different distro or adjust resources:

```bash
docker run --rm -it \
  --name vm1 \
  -p 2201:2201 \
  --device /dev/kvm:/dev/kvm \
  -e DISTRO=debian-12 \
  -e MEMORY=2048 \
  -e CPUS=4 \
  -e SSH_PORT=2201 \
  ghcr.io/munenick/docker-vm-runner:latest

```

## Persisting Disks & Certificates

Bind-mount a host directory to `/images` and opt into persistence:

```bash
mkdir -p ./images ./images/state

docker run --rm -it \
  --name vm1 \
  -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/images:/images" \
  -v "$PWD/images/state:/var/lib/docker-vm-runner" \
  -e PERSIST=1 \
  ghcr.io/munenick/docker-vm-runner:latest

```

Container storage layout:

- `/images/base/<distro>.qcow2` — cached cloud images per distro.
- `/images/vms/<name>/disk.qcow2` — working disk (retained when `PERSIST=1`).
- `/images/vms/<name>/seed.iso` — regenerated cloud-init seed (only when cloud-init is enabled).
- `/var/lib/docker-vm-runner` — management state (Redfish certificates, etc.).

## Console & Logs

- Attach to the serial console: `virsh console <vm_name>` (inside container) or rely on the container entrypoint (unless `NO_CONSOLE=1`).
- Detach from console: `Ctrl+]`.
- Logs: `docker logs -f vm1` (compose: `docker compose logs vm1`).
- Exec shell inside the container: `docker exec -it vm1 /bin/bash`.

## Custom distros.yaml

Override the built-in mapping by bind-mounting your own file:

```bash
docker run --rm -it \
  --name vm1 \
  --device /dev/kvm:/dev/kvm \
  -p 2222:2222 \
  -v "$PWD/distros.yaml:/config/distros.yaml:ro" \
  ghcr.io/munenick/docker-vm-runner:latest

```
