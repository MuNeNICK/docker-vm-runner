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

## Host Requirements

The current example maps `/dev/kvm` into each VM container for hardware acceleration. On hosts without KVM, remove the `devices` block from the Compose file; the VMs will run more slowly through software emulation.

Use distinct published ports for each VM service. Docker cannot publish the same host port from multiple containers in one Compose project.
