# Troubleshooting & Operations

## Common Issues

- **KVM unavailable**  
  Check `/dev/kvm` with `ls -la /dev/kvm`. If it is missing or unreadable, the VM falls back to TCG (slower). Ensure your user has access to the `kvm` group or run with appropriate privileges.

- **Unknown distribution**  
  Verify `DISTRO` matches a key in `distros.yaml` and that the file is available inside the container at `/config/distros.yaml`.

- **Console input not accepted**  
  Ensure the container runs with `stdin_open: true` and `tty: true` (compose already sets these). Reattach with `docker attach <container>` or `docker compose attach`.

- **Redfish HTTPS warnings / failures**  
  The manager generates a self-signed certificate under `/var/lib/docker-qemu/certs`. Bind-mount that directory to persist or replace it. Override credentials via `REDFISH_USERNAME` / `REDFISH_PASSWORD`.

## Redfish Management

- Endpoint: `https://localhost:${REDFISH_PORT:-8443}`  
  Default credentials: `admin` / `password`.
- Managed resources track the active libvirt domain; power and boot operations propagate instantly.
- Quick smoke test:

  ```bash
  curl -k -u admin:password https://localhost:8443/redfish/v1/Systems
  ```

## Networking

Default networking uses QEMU user-mode NAT:

- Start: `docker compose up -d`
- SSH: `ssh -p $VM_SSH_PORT <user>@localhost` (default `2222`)

To switch to bridge or other configurations, edit the domain XML via `EXTRA_ARGS` or customize the manager as needed.

## Compose Usage

`docker-compose.yml` defines a persistent VM service. Typical workflow:

```bash
docker compose up -d
docker compose logs -f vm1
docker compose exec vm1 /bin/bash
```

Override per-VM settings via environment variables (see [Configuration Reference](reference.md)).
