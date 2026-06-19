# IPMI Control With Ironic

This example shows Ironic controlling a `docker-vm-runner` VM through its IPMI endpoint.

Use it when you want to validate the older BMC-style management path that bare metal tooling expects: Ironic talks to IPMI, VirtualBMC talks to libvirt/QEMU, and management state is changed without using SSH or `guest-exec`.

## What This Reproduces

The Compose project reproduces a minimal bare-metal control plane:

- `ironic`: Metal3 Ironic API service.
- `ironic-client`: CLI container used to register and operate the node.
- `ipmi-vm`: a `docker-vm-runner` VM with IPMI and noVNC enabled.

The VM boots with an Alpine installer ISO attached and an empty working disk. This keeps the example focused on Ironic using IPMI management APIs rather than guest login or cloud-init.

This is not a full OpenStack deployment flow. It does not run Keystone, Neutron, Inspector, or automated image deployment.

## Start

```bash
docker compose up -d
```

Wait for Ironic and the IPMI endpoint to answer:

```bash
curl http://localhost:6385/
ipmitool -I lanplus -U admin -P ipmi-lab-password -H localhost -p 623 power status
```

Open the VM console:

```text
https://localhost:6080/
```

## Register The VM In Ironic

Create an Ironic node that points at the VM's IPMI endpoint inside the Compose network:

```bash
docker compose exec ironic-client baremetal node create \
  --name ipmi-vm \
  --driver ipmi \
  --management-interface ipmitool \
  --power-interface ipmitool \
  --boot-interface pxe \
  --deploy-interface ramdisk \
  --vendor-interface no-vendor \
  --driver-info ipmi_address=ipmi-vm \
  --driver-info ipmi_port=623 \
  --driver-info ipmi_username=admin \
  --driver-info ipmi_password=ipmi-lab-password \
  --property cpu_arch=x86_64 \
  --property cpus=2 \
  --property memory_mb=4096 \
  --property local_gb=8
```

Check what Ironic registered:

```bash
docker compose exec ironic-client baremetal node list
docker compose exec ironic-client baremetal node show ipmi-vm
```

## Use Ironic Through IPMI

Validate the Ironic node. The `management` and `power` interfaces should pass. The `boot` and `deploy` interfaces are expected to fail in this example because no deploy kernel, ramdisk, or provisioning network is configured.

```bash
docker compose exec ironic-client baremetal node validate ipmi-vm
```

Show the current boot device through Ironic:

```bash
docker compose exec ironic-client baremetal node boot device show ipmi-vm
```

Switch the boot target to the disk, then back to the attached ISO:

```bash
docker compose exec ironic-client baremetal node boot device set ipmi-vm disk
docker compose exec ironic-client baremetal node boot device show ipmi-vm
docker compose exec ironic-client baremetal node boot device set ipmi-vm cdrom
```

You can inspect the IPMI endpoint directly:

```bash
ipmitool -I lanplus -U admin -P ipmi-lab-password -H localhost -p 623 chassis status
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

The current `docker-vm-runner` requires a non-default `IPMI_PASSWORD` when `IPMI_ENABLE=1`; this example uses `ipmi-lab-password`.

The IPMI endpoint uses UDP port `623`; publish it with `/udp` when accessing it from the Docker host.

The IPMI endpoint runs inside the same container as the VM. With IPMI enabled, the runner stays alive after VM shutdown so Ironic can power the VM back on. Stopping the container still stops both the VM and the IPMI endpoint.
