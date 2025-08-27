# Docker-QEMU

Run lightweight QEMU VMs inside a Docker container. Pick a Linux
distribution via an environment variable; the image is downloaded and
launched automatically. Uses KVM when available.

## Quick Start

Prefer plain docker commands for one-shot runs. The CI publishes an image to GHCR; the helper script pulls `ghcr.io/munenick/docker-qemu:latest` by default. Compose is for persistent usage.

```bash
# Run (one-shot, ephemeral; enable KVM if available). Uses GHCR image: ghcr.io/munenick/docker-qemu:latest
docker run --rm -it \
  --name docker-qemu-vm \
  --privileged -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  ghcr.io/munenick/docker-qemu:latest

# Run a different distro with custom resources
docker run --rm -it \
  --name docker-qemu-vm \
  --privileged -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  -e DISTRO=debian-12 -e VM_MEMORY=2048 -e VM_CPUS=4 \
  ghcr.io/munenick/docker-qemu:latest

# Persist images across runs (cache under ./images)
docker run --rm -it \
  --name docker-qemu-vm \
  --privileged -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/images:/images" \
  ghcr.io/munenick/docker-qemu:latest

# Use local distros.yaml instead of the baked-in one
docker run --rm -it \
  --name docker-qemu-vm \
  --privileged -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/distros.yaml:/config/distros.yaml:ro" \
  ghcr.io/munenick/docker-qemu:latest
```

## Usage

- Interactive console in the same terminal. To quit QEMU: press Ctrl+A then X.
- Alternative start + attach (docker): add `-d` to `docker run` → `docker attach docker-qemu-vm`
- Show logs (docker): `docker logs -f docker-qemu-vm`
- Debug inside the container (docker): `docker exec -it docker-qemu-vm /bin/bash`
- Alternative start + attach (compose): `docker compose up -d` → `docker attach docker-qemu-vm`
- Show logs (compose): `docker compose logs`
- Debug inside the container (compose): `docker compose exec qemu /bin/bash`

## Helper Script (optional)

- Default run (ephemeral): `bash scripts/run-vm.sh` (uses `ghcr.io/munenick/docker-qemu:latest` by default)
- Change distro/resources: `DISTRO=debian-12 VM_MEMORY=2048 VM_CPUS=4 bash scripts/run-vm.sh`
- Persist images: `bash scripts/run-vm.sh --persist`
- Use local config: `bash scripts/run-vm.sh --use-local-config`
  

Run directly via curl (no clone):

```bash
curl -fsSL https://raw.githubusercontent.com/munenick/docker-qemu/main/scripts/run-vm.sh | bash
```

Preflight checks performed by the script:
- Docker CLI present and Docker daemon reachable
- KVM availability and basic permission hinting (`/dev/kvm` readable)
- Clear errors with next-step guidance if checks fail

Notes:
- If `/dev/kvm` exists, KVM is enabled automatically; otherwise it falls back to TCG (slower).
- The one-shot flow is ephemeral by default (no volumes). Use `--persist` to cache images in `./images`.
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

- `DISTRO`: Default `ubuntu-2404` — distribution key from `distros.yaml`.
- `VM_MEMORY`: Default `4096` — memory in MB.
- `VM_CPUS`: Default `2` — number of vCPUs.
- `VM_DISK_SIZE`: Default `20G` — resize target for the work image.
- `VM_DISPLAY`: Default `none` — headless mode.
- `VM_ARCH`: Default `x86_64` — QEMU system architecture.
- `QEMU_CPU`: Default `host` — CPU model.
- `VM_PASSWORD`: Default `password` — console password set via cloud-init.
- `NET_MODE`: Default `user` — currently only `user` (NAT with hostfwd :2222) is supported inside the container.
- `VM_SSH_PUBKEY`: Optional — SSH public key injected via cloud-init.
- `EXTRA_ARGS`: Additional QEMU CLI flags.

Cloud-init is always enabled with a minimal NoCloud seed to set the default
user's password for the chosen distribution. Log in on the console using:
- user: distro default (e.g., `ubuntu`, `debian`, `centos`, ...)
- pass: value of `VM_PASSWORD`

## Project Layout

```
docker-qemu/
├── Dockerfile          # QEMU container image
├── docker-compose.yml  # Compose configuration
├── distros.yaml        # Distribution map (mounted into the container)
├── entrypoint.sh       # Startup script
├── scripts/run-vm.sh   # One-shot runner for plain docker
├── images/             # Cached VM images
└── README.md
```

Note: `docker-compose.yml` mounts only `distros.yaml` into
`/config/distros.yaml` to avoid masking the image's `/config` directory.

## Troubleshooting

- KVM not available: check `/dev/kvm` with `ls -la /dev/kvm`. The VM will
  fall back to TCG (slower) if KVM is unavailable.
- Unknown distribution: ensure `DISTRO` matches a key in `distros.yaml` and
  that the file is mounted into the container at `/config/distros.yaml`.
 - If input is not accepted, ensure the container has `stdin_open: true` and `tty: true` (compose already sets these), then re-run `docker attach docker-qemu-vm`.

## Networking

- Default (NAT):
  - Start: `docker compose up -d`
  - SSH: `ssh -p 2222 <user>@localhost`

## Requirements

- Docker
- KVM-capable host (optional, for acceleration)
- Docker Compose (only for persistent usage)

## Compose (persistent)

For long-running or managed lifecycle use cases, use Compose which pulls the published image, mounts `./images`, and sets restart policy.

```bash
docker compose up -d
# Attach interactive console later
docker attach docker-qemu-vm
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
