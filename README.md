# Docker-QEMU

Run lightweight QEMU VMs inside a Docker container. Pick a Linux
distribution via an environment variable; the image is downloaded and
launched automatically. Uses KVM when available.

## Quick Start

Prefer plain docker commands for one-shot runs. The CI publishes an image to GHCR; the helper script pulls `ghcr.io/munenick/docker-qemu:latest` by default. Compose is for managed usage. Design principle: 1 VM = 1 container, now backed by libvirt for Redfish management.

```bash
# Run (one-shot, ephemeral; enable KVM if available). Uses GHCR image: ghcr.io/munenick/docker-qemu:latest
docker run --rm -it \
  --name vm1 \
  --hostname vm1 \
  -p 2222:2222 \
  -p 8443:8443 \
  --device /dev/kvm:/dev/kvm \
  ghcr.io/munenick/docker-qemu:latest

# Run a different distro with custom resources
docker run --rm -it \
  --name vm1 \
  --hostname vm1 \
  -p 2201:2201 \
  -p 8443:8443 \
  --device /dev/kvm:/dev/kvm \
  -e DISTRO=debian-12 -e VM_MEMORY=2048 -e VM_CPUS=4 -e VM_SSH_PORT=2201 \
  ghcr.io/munenick/docker-qemu:latest

# Persist images across runs (cache under ./images)
docker run --rm -it \
  --name vm1 \
  -p 2222:2222 \
  -p 8443:8443 \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/images:/images" \
  ghcr.io/munenick/docker-qemu:latest

# Use local distros.yaml instead of the baked-in one
docker run --rm -it \
  --name vm1 \
  --hostname vm1 \
  -p 2222:2222 \
  -p 8443:8443 \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/distros.yaml:/config/distros.yaml:ro" \
  ghcr.io/munenick/docker-qemu:latest
```

## Persisting VM Disks

By default VMs are ephemeral: the per-run disk (`disk.qcow2`) and cloud-init ISO are deleted when the container exits.  
Mount a host directory and enable persistence to retain them:

```bash
mkdir -p /var/lib/docker-qemu
docker run --rm -it \
  --name vm1 \
  --hostname vm1 \
  -p 2222:2222 \
  -p 8443:8443 \
  --device /dev/kvm:/dev/kvm \
  -v /var/lib/docker-qemu:/images \
  -e VM_NAME=vm1 \
  -e VM_PERSIST=1 \
  ghcr.io/munenick/docker-qemu:latest
```

Storage layout inside the container:

- `/images/base/<distro>.qcow2` – cached cloud images (shared by all VMs)
- `/images/vms/<vm_name>/disk.qcow2` – working disk (retained only when `VM_PERSIST=1`)
- `/images/vms/<vm_name>/seed.iso` – regenerated cloud-init seed per boot

## Usage

- Interactive console in the same terminal (via `virsh console`). To quit: press `Ctrl+]`.
- Alternative start + attach (docker): add `-d` to `docker run` -> `docker attach vm1`
- Show logs (docker): `docker logs -f vm1`
- Debug inside the container (docker): `docker exec -it vm1 /bin/bash`
- Alternative start + attach (compose): `docker compose up -d` -> `docker attach $(docker compose ps -q vm1)`
 - Show logs (compose): `docker compose logs vm1`
 - Debug inside the container (compose): `docker compose exec vm1 /bin/bash`

## Helper Script

- Run via curl (default image):
  - `curl -fsSL https://raw.github.com/munenick/docker-qemu/main/scripts/run-vm.sh | bash`
- Run with CPU/Memory:
  - `curl -fsSL https://raw.github.com/munenick/docker-qemu/main/scripts/run-vm.sh | bash -s -- --memory 2g --cpus 4`

Preflight checks performed by the script:
- Docker CLI present and Docker daemon reachable
- KVM availability and basic permission hinting (`/dev/kvm` readable)
- Clear errors with next-step guidance if checks fail

Notes:
- If `/dev/kvm` exists, KVM is enabled automatically; otherwise it falls back to TCG (slower).
- The one-shot flow is ephemeral by default (no volumes). Use `--persist` to cache images in `./images`.
- Redfish API is exposed on `8443/tcp` by default and uses a self-signed certificate. Override with `REDFISH_PORT`, `REDFISH_USERNAME`, `REDFISH_PASSWORD`.
- Containers run unprivileged by default; only `/dev/kvm` access is required. On hosts with strict AppArmor/SELinux profiles you may need `--security-opt apparmor=unconfined` (or equivalent) to allow libvirt to access `/dev/kvm`.
- When `--persist` is enabled, helper scripts bind-mount `./images/state` to preserve the generated Redfish certificate and other management artifacts between runs.
- VM names default to the container hostname; pass `--hostname <name>` (or `VM_NAME=<name>`) if you want deterministic names.
- If the GHCR image is private, run `echo $GITHUB_TOKEN | docker login ghcr.io -u munenick --password-stdin` or set the image public in GitHub Packages.

## Supported Distributions (keys)

- `ubuntu-2404`, `ubuntu-2204`, `ubuntu-2004`
- `debian-12`, `debian-11`
- `centos-stream-9`
- `fedora-41`
- `opensuse-leap-155`
- `rocky-linux-9`, `rocky-linux-8`
- `almalinux-9`, `almalinux-8`
- `archlinux`

## Configuration

- `DISTRO`: Default `ubuntu-2404` - distribution key from `distros.yaml`.
- `VM_MEMORY`: Default `4096` - memory in MB.
- `VM_CPUS`: Default `2` - number of vCPUs.
- `VM_DISK_SIZE`: Default `20G` - resize target for the work image.
- `VM_DISPLAY`: Default `none` - headless mode.
- `VM_ARCH`: Default `x86_64` - QEMU system architecture.
- `QEMU_CPU`: Default `host` - CPU model.
- `VM_PASSWORD`: Default `password` - console password set via cloud-init.
- `VM_SSH_PORT`: Default `2222` - container TCP port forwarded to guest `:22` (QEMU user-mode NAT). Useful when running multiple VMs concurrently.
- `VM_NAME`: Optional - per-VM name used to create working artifacts (`<name>-work.qcow2`, `<name>-seed.iso`). Defaults to container hostname or `DISTRO`.
- `VM_SSH_PUBKEY`: Optional - SSH public key injected via cloud-init.
- `EXTRA_ARGS`: Additional QEMU CLI flags.
- `VM_PERSIST`: Set `1` to keep the libvirt domain and work image after shutdown (helper script handles this automatically when you pass `--persist`).
- `VM_NO_CONSOLE`: Set `1` to skip attaching the console (helper script `--no-console`).
- `REDFISH_PORT`: Default `8443` - host/guest port used by the embedded Redfish endpoint.
- `REDFISH_USERNAME`, `REDFISH_PASSWORD`: Credentials for Redfish Basic auth (defaults `admin`/`password`).

Cloud-init is always enabled with a minimal NoCloud seed to set the default
user's password for the chosen distribution. Log in on the console using:
- user: distro default (e.g., `ubuntu`, `debian`, `centos`, ...)
- pass: value of `VM_PASSWORD`

## Project Layout

```
docker-qemu/
  Dockerfile           # QEMU container image
  docker-compose.yml   # Compose configuration
  distros.yaml         # Distribution map (mounted into the container)
  entrypoint.sh        # Startup shim -> Python manager
  app/manager.py       # Libvirt + sushy orchestration
  scripts/run-vm.sh    # One-shot runner for plain docker
  images/              # Cached VM images
  README.md
```

Note: `docker-compose.yml` mounts only `distros.yaml` into
`/config/distros.yaml` to avoid masking the image's `/config` directory.

## Troubleshooting

- KVM not available: check `/dev/kvm` with `ls -la /dev/kvm`. The VM will
  fall back to TCG (slower) if KVM is unavailable.
- Unknown distribution: ensure `DISTRO` matches a key in `distros.yaml` and
  that the file is mounted into the container at `/config/distros.yaml`.
 - If input is not accepted, ensure the container has `stdin_open: true` and `tty: true` (compose already sets these), then re-run `docker attach vm1` (for plain docker) or `docker attach $(docker compose ps -q vm1)` (for compose).
- Redfish connection issues: trust or replace the self-signed certificate under `/var/lib/docker-qemu/certs` (persisted automatically when `--persist` is enabled), or override with `REDFISH_USERNAME` / `REDFISH_PASSWORD`.

## Redfish Management

- Redfish API exposed at `https://localhost:${REDFISH_PORT:-8443}`.
- Default credentials: `admin` / `password`.
- Managed resources map the active libvirt domain. Power and boot operations propagate instantly; virtual media is connected via the generated cloud-init ISO.
- Example quick test:

```bash
curl -k -u admin:password https://localhost:8443/redfish/v1/Systems
```

## Networking

- Default (NAT):
  - Start: `docker compose up -d`
  - SSH: `ssh -p $VM_SSH_PORT <user>@localhost` (default 2222)

## Requirements

- Docker
- KVM-capable host (optional, for acceleration)
- Docker Compose (only for persistent usage)

## Compose (persistent)

For long-running or managed lifecycle use cases, use Compose which pulls the published image, mounts `./images`, and sets restart policy. To run multiple VMs, run multiple containers (1 VM = 1 container) and assign different `VM_SSH_PORT` and `VM_NAME` values.

```bash
docker compose up -d
# Attach interactive console later
docker attach vm1
```

To pre-pull or update the image:

```bash
docker compose pull
```

## CI/CD

- GitHub Actions builds and publishes `ghcr.io/munenick/docker-qemu:latest` on pushes to the default branch and publishes tagged variants on tags.
- Pull on clients via the helper script or use `IMAGE_NAME=ghcr.io/munenick/docker-qemu:latest` to override.

## License

MIT License
