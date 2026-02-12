# Filesystem Sharing

This guide explains how to expose a host directory to the guest VM through the container.
Docker-VM-Runner supports two libvirt-backed transports:

- **virtiofs** — fastest option, but requires the container to permit `virtiofsd` system calls.
- **9p (virtio-9p)** — works in stricter environments and supports UID/GID mapping via `accessmode=mapped`.

Cloud-init automatically mounts each tag as `/mnt/<tag>` (for example, `/mnt/share`), so no manual mount commands are required inside the guest.

## 1. Using virtiofs

`virtiofsd` needs `unshare(2)` and other namespace operations, so Docker's default seccomp/AppArmor constraints must be relaxed. Granting `SYS_ADMIN` capability is also recommended.

```bash
mkdir -p share

docker run --rm -it \
  --name vmfs \
  --device /dev/kvm:/dev/kvm \
  --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  -v "$PWD/share:/share" \
  -e FILESYSTEM_SOURCE=/share \
  -e FILESYSTEM_TARGET=share \
  -e FILESYSTEM_DRIVER=virtiofs \
  ghcr.io/munenick/docker-vm-runner:latest
```

> You can use a custom seccomp profile instead of `unconfined`, provided it allows `unshare`, `setns`, and related calls.

## 2. Using 9p

No additional container privileges are required. With `FILESYSTEM_ACCESSMODE=mapped`, libvirt maps guest UID/GID values on the host.

```bash
docker run --rm -it \
  --name vmfs \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/share:/share" \
  -e FILESYSTEM_SOURCE=/share \
  -e FILESYSTEM_TARGET=share \
  -e FILESYSTEM_DRIVER=9p \
  -e FILESYSTEM_ACCESSMODE=mapped \
  ghcr.io/munenick/docker-vm-runner:latest
```

> `mapped` and `squash` are only supported by 9p. Virtiofs is limited to `passthrough` by libvirt.

## 3. Permission considerations

- `passthrough` (virtiofs or 9p): guest UID/GID is preserved. Adjust ownership or ACLs on the host if the guest user needs write access.
- `mapped` / `squash` (9p only): libvirt assigns synthetic IDs, so guest unprivileged users can usually write without host-side changes.

## 4. Troubleshooting

| Symptom | Fix |
| --- | --- |
| `virtiofs only supports passthrough accessmode` | Use `FILESYSTEM_ACCESSMODE=passthrough` with virtiofs. |
| `virtiofsd ... unshare(CLONE_FS) failed with EPERM` | Relax seccomp/AppArmor/capabilities (for example `--security-opt seccomp=unconfined`). |
| Cannot write from guest in passthrough mode | Adjust ownership/permissions on the host, or use 9p with `FILESYSTEM_ACCESSMODE=mapped`. |
| Share not present in guest | Check `docker logs <container>` for `Filesystem #...` messages and verify environment variable names/values. |

## 5. Environment variables summary

| Variable | Description |
| --- | --- |
| `FILESYSTEM_SOURCE` | Directory inside the container (bind-mount the host path here). |
| `FILESYSTEM_TARGET` | Tag presented to the guest. Cloud-init mounts it as `/mnt/<tag>`. |
| `FILESYSTEM_DRIVER` | `virtiofs` or `9p`. |
| `FILESYSTEM_ACCESSMODE` | `passthrough` (default), `mapped`, or `squash` (`mapped`/`squash` are 9p-only). |
| `FILESYSTEM_READONLY` | Set to `1` to export the share read-only. |

For multiple shares use numbered variables (`FILESYSTEM2_SOURCE`, `FILESYSTEM2_TARGET`, …).
