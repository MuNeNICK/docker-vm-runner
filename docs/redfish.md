# Redfish Guide

Docker-VM-Runner can expose a Redfish-compatible BMC interface via `sushy-emulator`, allowing remote power and boot management of the guest VM. This guide covers enabling the service and performing common operations.

## Enable Redfish

1. Map the HTTPS port and set `REDFISH_ENABLE=1`:
   ```bash
   docker run --rm -it \
     --name vm1 \
     --device /dev/kvm:/dev/kvm \
     -p 2222:2222 \
     -p 8443:8443 \
     -e REDFISH_ENABLE=1 \
     ghcr.io/munenick/docker-vm-runner:latest
   ```
2. Optional: persist certificates using `DATA_DIR`:
   ```bash
   -v ./data:/data -e DATA_DIR=/data
   ```
3. Optional: override credentials using `REDFISH_USERNAME` / `REDFISH_PASSWORD`.

### Disable Redfish

Set `REDFISH_ENABLE=0` (default) or omit the variable entirely. The service and TLS bootstrap are skipped, reducing startup overhead.

## Endpoint & Authentication

- Base URL: `https://<host>:${REDFISH_PORT:-8443}`
- Default credentials: `admin` / `password`
- Trust: self-signed certificate stored under `/var/lib/docker-vm-runner/certs` (persist it if needed).

Example: list systems
```bash
curl -k -u admin:password https://localhost:8443/redfish/v1/Systems
```

## Common Workloads

### Power Control

```bash
# Graceful shutdown
curl -k -u admin:password \
  -H "Content-Type: application/json" \
  -d '{"ResetType":"GracefulShutdown"}' \
  https://localhost:8443/redfish/v1/Systems/vm1/Actions/ComputerSystem.Reset

# Force off
curl -k -u admin:password \
  -H "Content-Type: application/json" \
  -d '{"ResetType":"ForceOff"}' \
  https://localhost:8443/redfish/v1/Systems/vm1/Actions/ComputerSystem.Reset

# Power on
curl -k -u admin:password \
  -H "Content-Type: application/json" \
  -d '{"ResetType":"On"}' \
  https://localhost:8443/redfish/v1/Systems/vm1/Actions/ComputerSystem.Reset

# Reboot
curl -k -u admin:password \
  -H "Content-Type: application/json" \
  -d '{"ResetType":"GracefulRestart"}' \
  https://localhost:8443/redfish/v1/Systems/vm1/Actions/ComputerSystem.Reset
```

Use `ForceRestart` or `ForceOff` when the guest does not react to graceful commands.

### Power State Query

```bash
curl -k -u admin:password \
  https://localhost:8443/redfish/v1/Systems/vm1 | jq '.PowerState'
```

### Boot Source

Change the next boot device (e.g., to network):

```bash
curl -k -u admin:password \
  -H "Content-Type: application/json" \
  -d '{"Boot":{"BootSourceOverrideEnabled":"Once","BootSourceOverrideTarget":"Pxe"}}' \
  https://localhost:8443/redfish/v1/Systems/vm1
```

Valid targets include `Pxe`, `Hdd`, `Cd`, etc. Ensure the VM definition matches the requested devices.

## Tips

- Combine Redfish automation with SSH or noVNC sessions for installation workflows.
- To run multiple VMs, ensure each container uses a unique `REDFISH_PORT` and `SystemId` (`REDFISH_SYSTEM_ID`).
- Logs appear in `docker logs vm1`; Redfish requests are also visible inside the container under `/var/log`.
