# Multiple VMs With Docker Compose

This example runs two persistent VMs from one Compose project:

- `ubuntu`: Ubuntu 24.04 with SSH on `localhost:2222` and noVNC on `https://localhost:6080/`
- `alpine`: Alpine 3.22 with SSH on `localhost:2223`

Each VM has its own `/data` volume, so VM disks and state do not overlap. The catalog cache is shared through the `catalog-cache` volume.

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
