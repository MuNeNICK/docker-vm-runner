# Redfish Lab Demo

Redfish virtual-media boot demo for Docker-VM-Runner. [Ironic](https://docs.openstack.org/ironic/latest/) drives each guest through ISO mount, boot override, and reboot over the Redfish API; the control plane image is the Metal³ build of Ironic.

## Quick Start
```bash
cd examples/redfish-lab

# Optional: tweak ISO settings (defaults to Alpine Linux standard ISO)
cp .env.example .env

# Launch the stack and tail the provisioning logs
docker compose up -d
docker compose logs -f redfish-provisioner

# Watch the consoles
#   https://localhost:6081/  (redfish-client-1 via noVNC)
#   https://localhost:6082/  (redfish-client-2 via noVNC)

# Cleanup when done
docker compose down
```

If you change the ISO filename or URL in `.env`, recreate the stack so the new image is downloaded and served.

## What’s running
- **redfish-controller** – Ironic API plus the HTTP server hosting the boot ISO (default: Alpine Linux `alpine-standard-3.20.2-x86_64.iso`).
- **redfish-provisioner** – runs the Ironic CLI client against `TARGET_REDFISH_NODES` (defaults: `redfish-client-1 redfish-client-2`), mirroring an OpenStack baremetal deployment workflow.
- **redfish-client-1 / redfish-client-2** – docker-vm-runner guests with Redfish enabled.
