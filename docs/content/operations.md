# Operations

## Persistence

Mount `/data` when you want VM state to survive container removal:

```bash
docker run --rm -it --privileged \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

When `/data` is mounted, persistence is enabled by default.

Use `PERSIST=0` for a disposable VM:

```bash
docker run --rm -it --privileged \
  -e PERSIST=0 \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Storage locations

| Mount | Purpose |
| --- | --- |
| `/data` | Persistent VM disks, state, and image cache. |
| `/config` | Optional catalog cache. |

Mount both when you want predictable repeated runs:

```bash
docker run --rm -it --privileged \
  -v docker-vm-runner-data:/data \
  -v docker-vm-runner-config:/config \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Cleanup

Use cleanup mode to remove stale VM resources for the configured VM:

```bash
docker run --rm --privileged \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest --cleanup
```

Use the same `VM_NAME` or `DISTRO` values you used for the VM you want to clean.

## Stop behavior

The safest way to stop a persistent VM is to shut down the guest OS.

If you detach from the console, the VM may continue running. This is useful for long-running sessions, but it also means you should keep track of the VM lifecycle when running persistent workloads.

## Resource sizing

The common sizing variables are:

| Variable | Default | Description |
| --- | --- | --- |
| `MEMORY` | `4096` | VM memory in MiB. |
| `CPUS` | `2` | Number of vCPUs. |
| `DISK_SIZE` | `20G` | Working disk size. |

`MEMORY`, `CPUS`, and `DISK_SIZE` also accept `half` or `max` where supported by the setting.

## KVM

KVM is used when available. For best performance, run on a host with `/dev/kvm` available to the container.

If you require KVM and do not want fallback behavior, set:

```bash
-e REQUIRE_KVM=1
```
