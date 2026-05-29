# Examples

The `examples/` directory contains complete files for workflows that are easier to copy, run, and modify than a single command.

Each example documents the scenario it is meant to reproduce, the public behavior it exercises, and the commands used to run it.

## Multiple VMs With Docker Compose

Directory:

```text
examples/multiple-vms/
```

Files:

- `docker-compose.yaml` - runs two persistent VM services from one Compose project.
- `README.md` - shows start, access, management, and cleanup commands.

### Use Case

Use this example when you want to manage multiple VM containers as a single Compose project. It is useful for small local labs, distribution comparison, automation testing, and workflows where each VM needs stable state and a separate access port.

### What It Reproduces

The example reproduces a two-node local VM environment:

- `ubuntu`: Ubuntu 24.04 with SSH on `localhost:2222` and noVNC on `https://localhost:6080/`
- `alpine`: Alpine 3.22 with SSH on `localhost:2223`

Each service has its own persistent `/data` volume and its own VM name, so VM disks and libvirt resources do not collide. The services share a catalog cache volume to avoid repeating catalog metadata work.

This is not a clustered network setup. The VMs are independent machines using Docker-published ports for host access.

### Run

Run it from the example directory:

```bash
cd examples/multiple-vms
docker compose up -d
```

Connect with SSH:

```bash
ssh user@localhost -p 2222
ssh user@localhost -p 2223
```

Run commands through the QEMU guest agent:

```bash
docker exec ubuntu-vm guest-exec --wait "uname -a"
docker exec alpine-vm guest-exec --wait "uname -a"
```

Stop the VMs without deleting their disks:

```bash
docker compose stop
```

Remove containers and persistent VM state:

```bash
docker compose down -v
```

See `examples/multiple-vms/README.md` for the full walkthrough.

### Verification

This example was checked with:

```bash
docker compose config
docker compose run --rm --no-deps ubuntu --dry-run
docker compose run --rm --no-deps alpine --dry-run
docker compose up -d
docker exec ubuntu-vm guest-exec --wait "uname -a"
docker exec alpine-vm guest-exec --wait "uname -a"
curl -k -I https://localhost:6080/
docker compose down -v
```

## Redfish Control With Ironic

Directory:

```text
examples/redfish-ironic/
```

Files:

- `docker-compose.yaml` - runs Ironic, an Ironic CLI container, and one Redfish-enabled VM.
- `README.md` - shows Redfish discovery, Ironic node registration, validation, and boot-device management.

### Use Case

Use this example when you want to check that external bare-metal tooling can operate `docker-vm-runner` through Redfish instead of SSH or `guest-exec`.

### What It Reproduces

The example reproduces a minimal bare-metal control plane:

- `ironic`: Metal3 Ironic API service.
- `ironic-client`: CLI container used to register and operate the node.
- `redfish-vm`: a `docker-vm-runner` VM with Redfish and noVNC enabled.

The VM has an Alpine installer ISO attached as virtual CD media and an empty working disk. Ironic uses Redfish to validate the node and manage the boot device.

This is not a full OpenStack deployment flow. It does not run Keystone, Neutron, Inspector, or automated image deployment.

### Run

Run it from the example directory:

```bash
cd examples/redfish-ironic
docker compose up -d
```

Discover the Redfish System URI:

```bash
SYSTEM_PATH="$(curl -sk -u admin:redfish-lab-password \
  https://localhost:8443/redfish/v1/Systems \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["Members"][0]["@odata.id"])')"
```

Register the VM in Ironic:

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

Use Ironic to validate the node and manage the boot device:

```bash
docker compose exec ironic-client baremetal node validate redfish-vm
docker compose exec ironic-client baremetal node boot device show redfish-vm
docker compose exec ironic-client baremetal node boot device set redfish-vm disk
docker compose exec ironic-client baremetal node boot device set redfish-vm cdrom
```

Remove containers and persistent VM state:

```bash
docker compose down -v
```

See `examples/redfish-ironic/README.md` for the full walkthrough and caveats.

### Verification

This example was checked with:

```bash
docker compose config
docker compose run --rm --no-deps redfish-vm --dry-run
docker compose up -d
curl http://localhost:6385/
curl -k -u admin:redfish-lab-password https://localhost:8443/redfish/v1/
docker compose exec ironic-client baremetal node create ...
docker compose exec ironic-client baremetal node validate redfish-vm
docker compose exec ironic-client baremetal node boot device show redfish-vm
docker compose exec ironic-client baremetal node boot device set redfish-vm disk
docker compose exec ironic-client baremetal node boot device set redfish-vm cdrom
curl -k -I https://localhost:6080/
docker compose down -v
```

## iPXE Boot With netboot.xyz

Directory:

```text
examples/ipxe-netbootxyz/
```

Files:

- `docker-compose.yaml` - runs netboot.xyz and one iPXE-booting VM.
- `README.md` - shows host bridge preparation, startup, monitoring, and cleanup commands.

### Use Case

Use this example when you want to test the network boot path used by PXE-style installer environments.

### What It Reproduces

The example reproduces a small PXE network:

- `netbootxyz`: DHCP, TFTP, HTTP, and web UI from the netboot.xyz container.
- `ipxe-client`: a `docker-vm-runner` VM attached to a host bridge and configured for iPXE boot.

The VM uses an empty disk, legacy boot, a bootable bridged NIC, and an iPXE ROM. The expected path is DHCP lease, TFTP download of `netboot.xyz.kpxe`, then iPXE menu files from the netboot.xyz service.

### Run

Run it from the example directory:

```bash
cd examples/ipxe-netbootxyz
```

Create the bridge:

```bash
sudo ip link add ipxe-lab-br type bridge
sudo ip addr add 192.0.2.1/24 dev ipxe-lab-br
sudo ip link set ipxe-lab-br up
```

Start the stack:

```bash
docker compose up -d
```

Open the VM console:

```text
https://localhost:6081/
```

Watch DHCP and TFTP requests:

```bash
docker compose logs -f netbootxyz
```

Remove containers and persistent VM state:

```bash
docker compose down -v
```

Remove the bridge:

```bash
sudo ip link set ipxe-lab-br down
sudo ip link delete ipxe-lab-br type bridge
```

See `examples/ipxe-netbootxyz/README.md` for the full walkthrough.

### Verification

This example was checked with:

```bash
docker compose config
docker compose run --rm --no-deps ipxe-client --dry-run
docker compose up -d
docker compose logs netbootxyz
curl -I http://localhost:3000/
curl -k -I https://localhost:6081/
docker compose down -v
```

The observed netboot.xyz logs included DHCP offer/ack records and TFTP sends for `netboot.xyz.kpxe`, `menu.ipxe`, and `boot.cfg`.

## Host Requirements

The current examples map `/dev/kvm` into VM containers for hardware acceleration. On hosts without KVM, remove the `devices` block from the Compose file; the VMs will run more slowly through software emulation.

Use distinct published ports for each VM service. Docker cannot publish the same host port from multiple containers in one Compose project.

The iPXE example also needs permission to create and delete a host bridge. If the default bridge name or subnet conflicts with the host, change the matching values in `examples/ipxe-netbootxyz/docker-compose.yaml`.
