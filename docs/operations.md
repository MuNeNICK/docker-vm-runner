# Operations

## Persistence

Mount `/data` for persistent VM state:

```bash
docker run --rm -it --privileged \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

When `/data` is mounted, persistence is enabled by default. The runner stores base images, VM disks, and state under that directory.

Without `/data`, images are stored under `/images` and state under `/var/lib/docker-vm-runner` inside the container.

## Cleanup

Clean stale libvirt resources:

```bash
docker run --rm --privileged \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev --cleanup
```

Cleanup mode starts only the services needed for libvirt cleanup.

## Redfish

Redfish support is provided through sushy-tools and is intentionally kept as a runtime dependency.

Enable it with a non-default password:

```bash
docker run --rm -it --privileged \
  -p 8443:8443 \
  -e REDFISH_ENABLE=1 \
  -e REDFISH_PASSWORD='change-me' \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

| Variable | Default | Description |
| --- | --- | --- |
| `REDFISH_ENABLE` | `0` | Enable Redfish. |
| `REDFISH_USERNAME` | `admin` | Redfish username. |
| `REDFISH_PASSWORD` | `password` | Must be changed when Redfish is enabled. |
| `REDFISH_PORT` | `8443` | Redfish HTTPS port. |
| `REDFISH_SYSTEM_ID` | VM name | System identifier exposed to sushy-tools. |

## Resource safety

The runner is designed so VM resources do not silently outlive the container workflow:

- libvirt definitions are cleaned through libvirt APIs.
- VM disk and cache writes use validation and atomic placement where practical.
- noVNC and Redfish services are tied to the runner lifecycle.
- direct `qemu-system-*` process killing is not used as a fallback.
