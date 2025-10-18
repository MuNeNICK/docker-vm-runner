# Docker-QEMU

Run lightweight QEMU/KVM virtual machines within Docker. Each container hosts a libvirt-managed VM with optional Redfish control and noVNC console support. Images are pulled automatically from upstream cloud sources or can be supplied locally.

## Quick Start

```bash
docker run --rm -it \
  --name vm1 \
  --hostname vm1 \
  -p 2222:2222 \
  -p 8443:8443 \
  --device /dev/kvm:/dev/kvm \
  ghcr.io/munenick/docker-qemu:latest
```

- SSH: `ssh -p 2222 <user>@localhost` (user depends on the distro).
- Redfish: `https://localhost:8443` (`admin` / `password` by default).

For persistence, GUI, ISO installs, and compose workflows see the [documentation](docs/README.md) — start with [Quick Start](docs/quick-start.md) for additional `docker run` variants.

## Highlights

- KVM acceleration with automatic fallback to TCG when `/dev/kvm` is unavailable.
- Libvirt + sushy-emulator provide Redfish power/boot control.
- Cloud-init injects default credentials and optional SSH keys.
- Optional noVNC web console with TLS, local ISO/blank disk workflows, and docker-compose support.

## Documentation

- [Quick Start](docs/quick-start.md)
- [GUI & Installation Media](docs/gui-and-media.md)
- [Configuration Reference](docs/reference.md)
- [Troubleshooting & Operations](docs/troubleshooting.md)

## Project Layout

```
docker-qemu/
  Dockerfile           # QEMU container image
  docker-compose.yml   # Compose configuration
  distros.yaml         # Distribution map (mounted into the container)
  entrypoint.sh        # Startup shim -> Python manager
  app/manager.py       # Libvirt + sushy orchestration
  docs/                # Extended documentation
  images/              # Cached VM images
  README.md
```

## License

MIT License
