# Docker-VM-Runner

Run lightweight QEMU/KVM virtual machines within Docker. Each container hosts a libvirt-managed VM with optional Redfish control and noVNC console support. Images are pulled automatically from upstream cloud sources or can be supplied locally.

https://github.com/user-attachments/assets/434dc6cf-d06c-4bb0-b951-9ec4b81fe5f0

## Background

I rely on containers to avoid polluting host machines while developing applications, but containerized environments have limits around systemd, networking, and kernel features. Docker-VM-Runner bridges that gap by making it just as easy to spin up a full virtual machine: a single `docker run` fetches the requested cloud image, boots it, and attaches your terminal to the VM console immediately.

## Quick Start

```bash
docker run --rm -it \
  --name vm1 \
  -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  ghcr.io/munenick/docker-vm-runner:latest
```

- SSH: `ssh -p 2222 user@localhost` (password: `password`).
- Optional Redfish API: add `-e REDFISH_ENABLE=1 -p 8443:8443` and visit `https://localhost:8443` (`admin` / `password`).
- Multi-arch images: published tags target both `linux/amd64` and `linux/arm64`; with host KVM available the guest runs on its native architecture.

For persistence, GUI, ISO installs, and compose workflows see the [documentation](docs/README.md) — start with [Quick Start](docs/quick-start.md) for additional `docker run` variants.

## Highlights

- KVM acceleration with automatic fallback to TCG when `/dev/kvm` is unavailable.
- Libvirt manages lifecycle, with optional sushy-emulator (Redfish) power/boot control.
- Cloud-init injects default credentials and optional SSH keys.
- Run commands inside the guest with `docker exec <container> guest-exec "command"` — no SSH required.
- Bind-mount host directories into the guest via virtiofs or 9p for easy file sharing.
- Optional noVNC web console with TLS, local ISO/blank disk workflows, and docker-compose support.

## Documentation

- [Quick Start](docs/quick-start.md)
- [GUI & Installation Media](docs/gui-and-media.md)
- [iPXE Boot Guide](docs/ipxe.md)
- [Configuration Reference](docs/reference.md)
- [Security Guide](docs/security.md)
- [Troubleshooting & Operations](docs/troubleshooting.md)
- [Redfish Guide](docs/redfish.md)

## Host Platform Support

- **Linux** (`/dev/kvm` available) — highest performance. Containers can pass through KVM directly on bare-metal hosts or inside nested environments that expose the KVM device. This is the primary development and validation target.
- **Windows** (Docker Desktop / WSL2) — supports `/dev/kvm` when the host provides nested virtualization (Windows 11+ with WSL2 + `wsl --install --web-download`, or Hyper-V with virtualization enabled). Performance matches the Linux case when KVM is available; otherwise the container falls back to TCG emulation.
- **macOS** (Docker Desktop) — functional with the TCG fallback. Apple’s Virtualization.framework cannot be accessed from Linux containers, so expect reduced performance compared to native macOS hypervisors.

## License

MIT License
