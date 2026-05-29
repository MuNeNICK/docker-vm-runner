# Redfish Control With Ironic

This example shows Ironic controlling a `docker-vm-runner` VM through its Redfish endpoint.

Use it when you want to validate the BMC-style management path that bare metal tooling expects: Ironic talks to Redfish, Redfish talks to libvirt/QEMU, and management state is changed without using SSH or `guest-exec`.

## What This Reproduces

The Compose project reproduces a minimal bare-metal control plane:

- `ironic`: Metal3 Ironic API service.
- `ironic-client`: CLI container used to register and operate the node.
- `redfish-vm`: a `docker-vm-runner` VM with Redfish and noVNC enabled.

The VM boots with an Alpine installer ISO attached and an empty working disk. This keeps the example focused on Ironic using Redfish management APIs rather than guest login or cloud-init.

This is not a full OpenStack deployment flow. It does not run Keystone, Neutron, Inspector, or automated image deployment.

## Start

```bash
docker compose up -d
```

Wait for both endpoints to answer:

```bash
curl http://localhost:6385/
curl -k -u admin:redfish-lab-password https://localhost:8443/redfish/v1/
```

Open the VM console:

```text
https://localhost:6080/
```

## Register The VM In Ironic

Discover the Redfish System URI:

```bash
SYSTEM_PATH="$(curl -sk -u admin:redfish-lab-password \
  https://localhost:8443/redfish/v1/Systems \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["Members"][0]["@odata.id"])')"
echo "$SYSTEM_PATH"
```

Create an Ironic node that points at the VM's Redfish endpoint inside the Compose network:

```bash
docker compose exec ironic-client baremetal node create \
  --name redfish-vm \
  --driver redfish \
  --management-interface redfish \
  --power-interface redfish \
  --boot-interface redfish-virtual-media \
  --deploy-interface ramdisk \
  --vendor-interface redfish \
  --driver-info redfish_address=https://redfish-vm:8443 \
  --driver-info redfish_system_id="$SYSTEM_PATH" \
  --driver-info redfish_username=admin \
  --driver-info redfish_password=redfish-lab-password \
  --driver-info redfish_verify_ca=false \
  --driver-info redfish_auth_type=basic \
  --property cpu_arch=x86_64 \
  --property cpus=2 \
  --property memory_mb=4096 \
  --property local_gb=8
```

Check what Ironic registered:

```bash
docker compose exec ironic-client baremetal node list
docker compose exec ironic-client baremetal node show redfish-vm
```

## Use Ironic Through Redfish

Validate the Ironic node. The `management` and `power` interfaces should pass. The `boot` and `deploy` interfaces are expected to fail in this example because no deploy kernel or ramdisk is configured.

```bash
docker compose exec ironic-client baremetal node validate redfish-vm
```

Show the current boot device through Ironic. Because the VM starts with an installer ISO attached, the initial boot device is `cdrom`.

```bash
docker compose exec ironic-client baremetal node boot device show redfish-vm
```

Switch the boot target to the disk, then back to the attached ISO:

```bash
docker compose exec ironic-client baremetal node boot device set redfish-vm disk
docker compose exec ironic-client baremetal node boot device show redfish-vm
docker compose exec ironic-client baremetal node boot device set redfish-vm cdrom
```

You can inspect the Redfish endpoint directly:

```bash
curl -k -u admin:redfish-lab-password "https://localhost:8443${SYSTEM_PATH}"
```

## Manage

Stop the lab without deleting the VM disk:

```bash
docker compose stop
```

Remove containers while keeping the VM disk:

```bash
docker compose down
```

Remove containers and VM state:

```bash
docker compose down -v
```

## Notes

The current `docker-vm-runner` requires a non-default `REDFISH_PASSWORD` when `REDFISH_ENABLE=1`; this example uses `redfish-lab-password`.

The Redfish endpoint uses a self-signed certificate, so Ironic is configured with `redfish_verify_ca=false`.

The Redfish endpoint runs inside the same container as the VM. This example avoids `baremetal node power off` because powering off the VM also ends the container-side runner process that hosts the Redfish endpoint.
