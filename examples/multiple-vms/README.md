# Multiple VMs With Docker Compose

This example shows how to manage more than one VM with Docker Compose.

Use it when you want a small repeatable lab made of separate VM services, for example:

- testing automation against multiple Linux distributions
- keeping a lightweight utility VM and a fuller Ubuntu VM side by side
- checking that VM names, disks, ports, and noVNC access remain isolated per service

## What This Reproduces

The Compose project reproduces a two-node local VM environment:

- `ubuntu`: Ubuntu 24.04 with SSH on `localhost:2222` and noVNC on `https://localhost:6080/`
- `alpine`: Alpine 3.22 with SSH on `localhost:2223`

Each VM is a separate container and a separate libvirt/QEMU VM. Each service has its own `/data` volume, so VM disks and state do not overlap. The catalog cache is shared through the `catalog-cache` volume so catalog metadata can be reused.

The example intentionally publishes different host ports for each VM. Docker Compose cannot publish the same host port from multiple services.

## Start

```bash
docker compose up -d
```

Follow startup logs:

```bash
docker compose logs -f ubuntu
docker compose logs -f alpine
```

## Connect

SSH uses the default guest user `user` and password `password` unless you change `GUEST_PASSWORD`.

```bash
ssh user@localhost -p 2222
ssh user@localhost -p 2223
```

Open the Ubuntu graphical console:

```text
https://localhost:6080/
```

Run a command through the QEMU guest agent:

```bash
docker exec ubuntu-vm guest-exec --wait "uname -a"
docker exec alpine-vm guest-exec --wait "uname -a"
```

## Manage

Stop the VMs without deleting disks:

```bash
docker compose stop
```

Start them again:

```bash
docker compose start
```

Remove the containers while keeping VM disks and cached images:

```bash
docker compose down
```

Remove containers and persistent VM state:

```bash
docker compose down -v
```

## Notes

The example maps `/dev/kvm` into each container for hardware acceleration. If the host does not provide `/dev/kvm`, remove the `devices` block from `docker-compose.yaml`; the VMs will run more slowly through software emulation.

This is not a cluster example. The VMs are independent machines on Docker-managed NAT networks, with host access through published ports.
