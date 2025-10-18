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

Notes:
- If `/dev/kvm` exists, KVM is enabled automatically; otherwise it falls back to TCG (slower).
- Containers are ephemeral unless you bind-mount `./images` (or another directory) into `/images` to keep disks/certs.
- To persist generated management artifacts (Redfish certificates, etc.), bind-mount a host directory (e.g. `./images/state`) to `/var/lib/docker-qemu`.
- Redfish API is exposed on `8443/tcp` by default and uses a self-signed certificate. Override with `REDFISH_PORT`, `REDFISH_USERNAME`, `REDFISH_PASSWORD`.
- Containers run unprivileged by default; only `/dev/kvm` access is required. On hosts with strict AppArmor/SELinux profiles you may need `--security-opt apparmor=unconfined` (or equivalent) to allow libvirt to access `/dev/kvm`.
- Browser console: set `VM_DISPLAY=novnc` (and optionally override `VM_NOVNC_PORT`) to expose the built-in noVNC UI secured with the same certificate as Redfish.
- Custom media:
  - Place local disks/ISOs under `./images/base` (auto-mounted to `/images/base` inside the container).
  - Use `VM_BASE_IMAGE` to point at an existing QCOW2/RAW base disk (skips downloads), or `VM_BLANK_DISK=1` to start from an empty disk of size `VM_DISK_SIZE`.
  - Attach installation media by setting `VM_BOOT_ISO=/images/base/<iso>.iso`; the manager will boot from CD first by default when this is present.
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
- `VM_DISPLAY`: Default `none` - headless mode. Set to `vnc` to expose the native VNC server, or `novnc` to enable the bundled noVNC web console.
- `VM_VNC_PORT`: Default `5900` - container/host port that the VNC server listens on when `VM_DISPLAY` is `vnc` or `novnc`.
- `VM_NOVNC_PORT`: Default `6080` - container/host port that serves the noVNC web client when `VM_DISPLAY=novnc`.
- `VM_BASE_IMAGE`: Optional - absolute path (inside the container) to a QCOW2/RAW image to use as the base disk instead of downloading from `distros.yaml`. Set to `blank` (or combine with `VM_BLANK_DISK=1`) to start from an empty disk.
- `VM_BLANK_DISK`: Default `0` - set to `1` to create a fresh blank disk of size `VM_DISK_SIZE`.
- `VM_BOOT_ISO`: Optional - path to an ISO that will be attached as CD-ROM (`/images/base/...` when mounted via `--persist`). When set, boot order automatically prioritises the CD.
- `VM_BOOT_ORDER`: Default `hd` - comma-separated libvirt boot devices (`hd`, `cd`, `network`, ...).
- `VM_CLOUD_INIT`: Default `1` - set `0` to disable cloud-init seed generation/attachment.
- `VM_ARCH`: Default `x86_64` - QEMU system architecture.
- `VM_CPU_MODEL`: Default `host` - CPU model.
- `VM_PASSWORD`: Default `password` - console password set via cloud-init.
- `VM_SSH_PORT`: Default `2222` - container TCP port forwarded to guest `:22` (QEMU user-mode NAT). Useful when running multiple VMs concurrently.
- `VM_NAME`: Optional - per-VM name used to create working artifacts (`<name>-work.qcow2`, `<name>-seed.iso`). Defaults to container hostname or `DISTRO`.
- `VM_SSH_PUBKEY`: Optional - SSH public key injected via cloud-init.
- `EXTRA_ARGS`: Additional QEMU CLI flags.
- `VM_PERSIST`: Set `1` to keep the libvirt domain and work image after shutdown (helper script handles this automatically when you pass `--persist`).
- `VM_NO_CONSOLE`: Set `1` to skip attaching the console (helper script `--no-console`).
- `REDFISH_PORT`: Default `8443` - host/guest port used by the embedded Redfish endpoint.
- `REDFISH_USERNAME`, `REDFISH_PASSWORD`: Credentials for Redfish Basic auth (defaults `admin`/`password`).

### Example: Booting a Local Desktop ISO

Place your media and (optionally) seed disk images under `./images/base` (mounted to
`/images/base` inside the container whenever you pass `--persist`) and launch:

```bash
docker run --rm \
  --name ubuntu-desktop-vm \
  --device /dev/kvm:/dev/kvm \
  --security-opt apparmor=unconfined \
  -v "$(pwd)/images:/images" \
  -v "$(pwd)/images/state:/var/lib/docker-qemu" \
  -v "$(pwd)/distros.yaml:/config/distros.yaml:ro" \
  -p 2222:2222 \
  -p 8443:8443 \
  -p 6080:6080 \
  -e VM_NAME=ubuntu-desktop \
  -e VM_DISPLAY=novnc \
  -e VM_NO_CONSOLE=1 \
  -e VM_DISK_SIZE=40G \
  -e VM_BOOT_ISO=/images/base/ubuntu-24.04.3-desktop-amd64.iso \
  -e VM_BOOT_ORDER=cdrom,hd \
  -e VM_CLOUD_INIT=0 \
  -e EXTRA_ARGS="-device virtio-gpu-pci,edid=on,xres=1920,yres=1080" \
  docker-qemu-novnc-test
```

- Omit the ISO / switch `VM_BOOT_ORDER` back to `hd` after installation to boot from disk directly.
- The noVNC console is reachable at `https://localhost:6080/` when `VM_DISPLAY=novnc`.

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
- Redfish connection issues: trust or replace the self-signed certificate under `/var/lib/docker-qemu/certs` (bind-mount `/var/lib/docker-qemu` to persist it), or override with `REDFISH_USERNAME` / `REDFISH_PASSWORD`.

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
