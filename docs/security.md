# Security Guide

This document covers security considerations for production deployments of Docker-VM-Runner.

## Default Credentials

When `GUEST_PASSWORD` or `REDFISH_PASSWORD` are not explicitly set, Docker-VM-Runner generates a random password at each startup and displays it in the startup banner.

| Service        | Default User | Default Password | Override Variable      |
|----------------|-------------|------------------|------------------------|
| Guest VM       | `user`      | (random)         | `GUEST_PASSWORD`       |
| Redfish API    | `admin`     | (random)         | `REDFISH_USERNAME` / `REDFISH_PASSWORD` |

**Recommendations:**

- For production VMs, disable password-based SSH in your cloud-init user-data and rely on key-based authentication via `SSH_PUBKEY`.
- Set `REDFISH_USERNAME` and `REDFISH_PASSWORD` to fixed values when `REDFISH_ENABLE=1` and you need stable credentials.

## TLS Certificates

Docker-VM-Runner auto-generates a self-signed TLS certificate for:

- The Redfish (sushy-emulator) HTTPS endpoint
- The noVNC WebSocket proxy

Self-signed certificates are **not suitable for production**. To use your own certificates:

```bash
docker run \
  -v /path/to/my.crt:/var/lib/docker-vm-runner/state/certs/sushy.crt:ro \
  -v /path/to/my.key:/var/lib/docker-vm-runner/state/certs/sushy.key:ro \
  ...
```

When `PERSIST=1` is set, generated certificates are retained across container restarts under the state directory.

## Container Security

Docker-VM-Runner requires elevated container privileges because it manages a full virtualization stack (libvirt + QEMU) inside the container.

### Required Privileges

| Setting | Why |
|---------|-----|
| `--privileged` (or `--device /dev/kvm --cgroupns=host`) | KVM access for hardware-accelerated virtualization |
| `--cap-add SYS_ADMIN` | Required only when using `virtiofs` filesystem sharing |

### libvirt Configuration

The bundled `qemu.conf` uses these settings for container compatibility:

- `security_driver = "none"` — Disables SELinux/AppArmor confinement for QEMU processes. This is necessary because container-internal libvirt cannot manage host security labels.
- `clear_emulator_capabilities = 0` — Retains full capabilities for the QEMU emulator process. Required for device access within containers.
- `user = "root"` / `group = "root"` — QEMU runs as root inside the container.

These settings are standard for containerized libvirt deployments but mean the VM processes are **not sandboxed** within the container.

## Network Exposure

By default, all services bind to `0.0.0.0` (all interfaces):

| Service | Default Port | Env Variable |
|---------|-------------|--------------|
| SSH (forwarded to guest) | 2222 | `SSH_PORT` |
| VNC | 5900 | `VNC_PORT` |
| noVNC (HTTPS) | 6080 | `NOVNC_PORT` |
| Redfish (HTTPS) | 8443 | `REDFISH_PORT` |

**Recommendations:**

- Only publish the ports you need: `-p 127.0.0.1:2222:2222` instead of `-p 2222:2222`.
- Use a reverse proxy with authentication for noVNC and Redfish in production.
- When using bridge or direct (macvtap) networking, the guest VM is directly exposed to the network — apply appropriate firewall rules on the host.

### macvtap / Direct Mode

When `NETWORK_MODE=direct`, the guest NIC uses macvtap in bridge mode. This:

- Gives the guest a real IP address on the host network
- Requires promiscuous mode on the parent interface
- Bypasses Docker's network isolation entirely

Only use direct mode on trusted networks.

## Production Checklist

Before deploying Docker-VM-Runner in production:

- [ ] Changed `GUEST_PASSWORD` to a strong value (or using SSH key auth only)
- [ ] Changed `REDFISH_USERNAME` and `REDFISH_PASSWORD` (if Redfish is enabled)
- [ ] Replaced self-signed TLS certificates with proper ones
- [ ] Published only necessary ports with host-binding (`127.0.0.1:port:port`)
- [ ] Set `PERSIST=1` (or mounted a data volume at `/data`) for data persistence
- [ ] Reviewed network mode — prefer NAT for isolated setups, bridge/direct only on trusted networks
- [ ] Reviewed cloud-init user-data for any hardcoded secrets
