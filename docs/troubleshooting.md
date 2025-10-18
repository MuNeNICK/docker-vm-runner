# Troubleshooting & Operations

## Common Issues

- **KVM unavailable**  
  Check `/dev/kvm` with `ls -la /dev/kvm`. The container requires hardware virtualization; if the device is missing or unreadable, libvirt fails to start the guest. Ensure the module is loaded and your user has access to the `kvm` group (or run the container with sufficient privileges).

- **Unknown distribution**  
  Verify `DISTRO` matches a key in `distros.yaml` and that the file is available inside the container at `/config/distros.yaml`.

- **Console input not accepted**  
  Ensure the container runs with `stdin_open: true` and `tty: true` (compose already sets these). Reattach with `docker attach <container>` or use `docker compose exec <service> virsh console <guest>`.

- **Redfish HTTPS warnings / failures**  
  Redfish is disabled unless `REDFISH_ENABLE=1`. When enabled, the manager generates a self-signed certificate under `/var/lib/docker-vm-runner/certs`. Bind-mount that directory to persist or replace it. Override credentials via `REDFISH_USERNAME` / `REDFISH_PASSWORD`.

For detailed Redfish enablement and workflows, see the [Redfish Guide](redfish.md).

## Networking

Default networking uses QEMU user-mode NAT:

- Start: `docker compose up -d`
- SSH: `ssh -p $SSH_PORT <user>@localhost` (default `2222`)

To expose the VM on an upstream network, switch to bridge or direct/macvtap mode with the variables described in the [Networking Guide](networking.md).

## Compose Usage

`docker-compose.yml` defines a persistent VM service. Typical workflow:

```bash
docker compose up -d
docker compose logs -f vm1
docker compose exec vm1 /bin/bash
```

Override per-VM settings via environment variables (see [Configuration Reference](reference.md)).
