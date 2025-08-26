# Docker-QEMU

Run lightweight QEMU VMs inside a Docker container. Pick a Linux
distribution via an environment variable; the image is downloaded and
launched automatically. Uses KVM when available.

## Quick Start

```bash
# Build the image
docker compose build

# Start (default: Ubuntu 24.04)
docker compose run --rm qemu

# Start a different distro
DISTRO=debian-12 docker compose run --rm qemu

# Customize resources
DISTRO=fedora-41 VM_MEMORY=2048 VM_CPUS=4 docker compose run --rm qemu
```

## Usage

- Interactive console in the same terminal. To quit QEMU: press Ctrl+A then X.
- Alternative start + attach: `docker compose up -d` → `docker attach docker-qemu-vm`
- Show logs: `docker compose logs`
- Debug inside the container: `docker compose exec qemu /bin/bash`

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
- `VM_PASSWORD`: Default `ubuntu` — console password set via cloud-init.
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
 - If input is not accepted, start with `docker compose run --rm qemu`, or
   use `docker compose up -d` and then `docker attach docker-qemu-vm`.

## Requirements

- Docker
- Docker Compose
- KVM-capable host (optional, for acceleration)

## License

MIT License
