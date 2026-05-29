# Operations

## Preview before starting

Use preview commands before complex VM launches:

```bash
docker run --rm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e CPUS=4 \
  -e MEMORY=8192 \
  -e NETWORK_MODE=nat \
  -e PORT_FWD=8080:80 \
  ghcr.io/munenick/docker-vm-runner:latest --show-config

docker run --rm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e CPUS=4 \
  -e MEMORY=8192 \
  ghcr.io/munenick/docker-vm-runner:latest --dry-run
```

Use `--show-xml` when the question is about generated libvirt/QEMU device
semantics.

## Readiness

For background VMs:

```bash
docker logs -f <container>
```

For command execution, prefer:

```bash
docker exec <container> guest-exec --wait "command"
```

`--wait` waits for the guest agent. If cloud-init is still doing first-boot
setup, run:

```bash
docker exec <container> guest-exec --wait "cloud-init status --wait"
```

## Persistence

Mount `/data` when VM disks and image cache should survive container removal:

```bash
-v docker-vm-runner-data:/data
```

Use `GUEST_NAME` when multiple persistent VMs share the same `/data` volume.
Use `PERSIST=0` for a disposable VM even when storage is mounted.

Mount `/config` when catalog cache should persist:

```bash
-v docker-vm-runner-config:/config
```

## Stop behavior

The safest way to stop a persistent VM is from inside the guest:

```bash
docker exec <container> guest-exec --wait "poweroff"
```

When attached through Docker, press `Ctrl+P` then `Ctrl+Q` to leave the VM
running. Avoid `Ctrl+]` when the goal is to keep the VM alive.

## Cleanup

Use cleanup mode when a previous container exited and left runtime resources
behind:

```bash
docker run --rm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest --cleanup
```

Use the same `GUEST_NAME` or `DISTRO` values used for the VM being cleaned.
With `/data` mounted, cleanup keeps persistent disks and cached images.

Remove disposable containers when done:

```bash
docker rm -f <container>
```

Remove persistent Docker volumes only when the VM disk and image cache are no
longer needed:

```bash
docker volume rm <volume>
```
