# Operations

## Persistence

Mount `/data` when you want VM state to survive container removal:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

When `/data` is mounted, persistence is enabled by default.

Use `PERSIST=0` for a disposable VM:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e PERSIST=0 \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Storage locations

| Mount | Purpose |
| --- | --- |
| `/data` | Persistent VM disks, state, and image cache. |
| `/config` | Optional catalog cache. |

Mount both when you want to keep VM disks and the catalog cache across runs:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -v docker-vm-runner-data:/data \
  -v docker-vm-runner-config:/config \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Cleanup

Use cleanup mode when a previous container exited and left VM resources behind:

```bash
docker run --rm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest --cleanup
```

Use the same `GUEST_NAME` or `DISTRO` values you used for the VM you want to clean.

When `/data` is mounted, cleanup keeps persistent VM disks and cached images. It removes stale runtime resources only.

## Stop behavior

The safest way to stop a persistent VM is to shut down the guest OS.

If you detach from the console, the VM may continue running. This is useful for long-running sessions, but remember to shut down the guest when you are done.

## Resource sizing

The common sizing variables are:

| Variable | Default | Description |
| --- | --- | --- |
| `MEMORY` | `4096` | VM memory in MiB. |
| `CPUS` | `2` | Number of vCPUs. |
| `DISK_SIZE` | `20G` | Working disk size. |

`MEMORY`, `CPUS`, and `DISK_SIZE` also accept `half` or `max`.

## KVM

For best performance, expose `/dev/kvm` to the container when it is available on the host:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

If you require KVM and do not want fallback behavior, set:

```bash
-e REQUIRE_KVM=1
```

## Docker options

| Feature | Typical Docker option |
| --- | --- |
| KVM acceleration | `--device /dev/kvm` |
| Host block device passthrough | `--device /dev/<device>` |
| Access to local boot media | `-v /host/path:/container/path:ro` |
