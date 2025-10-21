# Redfish Lab Demo

Redfish virtual-media boot demo for Docker-VM-Runner. [Ironic](https://docs.openstack.org/ironic/latest/) drives each guest through ISO mount, boot override, and reboot over the Redfish API; the control plane image is the Metal³ build of Ironic.

## Quick Start
```bash
cd examples/redfish-lab

# Optional: tweak ISO settings (defaults to Alpine Linux standard ISO)
cp .env.example .env

# Create persistence directories (guests keep their disks here)
mkdir -p images/base \
         images/vms/redfish-client-1 images/state/redfish-client-1 \
         images/vms/redfish-client-2 images/state/redfish-client-2

# Launch the stack and tail the provisioning logs
docker compose up -d
docker compose logs -f redfish-provisioner

# Watch the consoles
#   https://localhost:6080/  (redfish-client-1 via noVNC)
#   https://localhost:6081/  (redfish-client-2 via noVNC)

# Cleanup when done
docker compose down
```

If you change the ISO filename or URL in `.env`, recreate the stack so the new image is downloaded and served.

## What’s running
- **redfish-controler** – Ironic API plus the HTTP server hosting the boot ISO (default: Alpine Linux `alpine-standard-3.20.2-x86_64.iso`).
- **redfish-provisioner** – runs the Ironic CLI client against `TARGET_REDFISH_NODES` (defaults: `redfish-client-1 redfish-client-2`), mirroring an OpenStack baremetal deployment workflow.
- **redfish-client-1 / redfish-client-2** – docker-vm-runner guests with Redfish enabled; disks live under `images/vms/*`, state under `images/state/*`, and noVNC consoles on ports 6080/6081.

Append additional guest services in `docker-compose.yml` and add their container names to `TARGET_REDFISH_NODES` if you want to provision more than two VMs. The format `node@host:port#/redfish/...` is supported, but plain container names work when Redfish uses defaults.
