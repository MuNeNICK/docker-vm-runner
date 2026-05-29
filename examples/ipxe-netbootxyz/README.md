# iPXE Boot With netboot.xyz

This example starts a local netboot.xyz PXE service and boots a `docker-vm-runner` VM from it with iPXE.

Use it when you want to validate a network-boot workflow: DHCP hands the VM a boot filename, the VM loads iPXE over TFTP, and the installer menu appears in the VM console.

## What This Reproduces

The Compose project reproduces a small PXE network:

- `netbootxyz`: DHCP, TFTP, HTTP, and web UI from the netboot.xyz container.
- `ipxe-client`: a `docker-vm-runner` VM attached to a host bridge and configured for iPXE network boot.

The VM uses an empty disk and no cloud-init so the first boot path is the network boot flow.

## Prepare The Bridge

Create an isolated host bridge for the PXE network:

```bash
cp .env.example .env
set -a
. ./.env
set +a

sudo ip link add "$PXE_BRIDGE_NAME" type bridge
sudo ip addr add "$NETBOOT_ROUTER/${NETBOOT_SUBNET#*/}" dev "$PXE_BRIDGE_NAME"
sudo ip link set "$PXE_BRIDGE_NAME" up
```

The default subnet is `192.0.2.0/24`. Change `.env` first if that subnet already exists on your host.

## Start

```bash
docker compose up -d
```

Open the VM console:

```text
https://localhost:6081/
```

Open the netboot.xyz web UI:

```text
http://localhost:3000/
```

Follow logs while the VM requests DHCP and TFTP:

```bash
docker compose logs -f netbootxyz
docker compose logs -f ipxe-client
```

## Inspect The VM Configuration

Preview the VM configuration without starting it:

```bash
docker compose run --rm --no-deps ipxe-client --dry-run
```

The important settings are:

- `NETWORK_MODE=bridge`
- `NETWORK_BRIDGE=ipxe-lab-br`
- `NETWORK_BOOT=1`
- `IPXE_ENABLE=1`
- `BOOT_ORDER=network,hd`

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

Remove the bridge:

```bash
set -a
. ./.env
set +a

sudo ip link set "$PXE_BRIDGE_NAME" down
sudo ip link delete "$PXE_BRIDGE_NAME" type bridge
```

## Notes

The example uses host networking because DHCP and TFTP need to bind to the host bridge.

For installer downloads beyond the local netboot.xyz menu, route the PXE bridge to an uplink with NAT or attach it to an existing network that already has outbound access.
