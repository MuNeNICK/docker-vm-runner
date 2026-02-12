# Filesystem Sharing

## Quick Start

Share a host directory into the guest (simplest method — 9p, no extra privileges):

```bash
docker run --rm -it \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/share:/share" \
  -e FILESYSTEM_SOURCE=/share \
  -e FILESYSTEM_DRIVER=9p \
  ghcr.io/munenick/docker-vm-runner:latest
```

The guest auto-mounts it at `/mnt/share` (tag auto-derived from the source path).

Two drivers are available:

| Driver | Speed | Extra privileges? |
| --- | --- | --- |
| `virtiofs` | Fast | Yes (`--cap-add SYS_ADMIN --security-opt seccomp=unconfined`) |
| `9p` | Adequate | No |

## virtiofs

Fastest option. Requires relaxed container security:

```bash
docker run --rm -it \
  --device /dev/kvm:/dev/kvm \
  --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined \
  -v "$PWD/share:/share" \
  -e FILESYSTEM_SOURCE=/share \
  -e FILESYSTEM_DRIVER=virtiofs \
  ghcr.io/munenick/docker-vm-runner:latest
```

## 9p

No extra privileges needed. Supports UID/GID mapping:

```bash
docker run --rm -it \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/share:/share" \
  -e FILESYSTEM_SOURCE=/share \
  -e FILESYSTEM_DRIVER=9p \
  -e FILESYSTEM_ACCESSMODE=mapped \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Detailed Options

### Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `FILESYSTEM_SOURCE` | *(required)* | Directory inside the container to expose. |
| `FILESYSTEM_TARGET` | *(auto)* | Guest-facing tag. Auto-derived from source path if omitted. Mounted at `/mnt/<tag>`. |
| `FILESYSTEM_DRIVER` | `virtiofs` | `virtiofs` or `9p`. |
| `FILESYSTEM_ACCESSMODE` | `passthrough` | `passthrough`, `mapped`, or `squash` (`mapped`/`squash` are 9p-only). |
| `FILESYSTEM_READONLY` | `0` | Set `1` for read-only. |

For multiple shares, use indexed variables: `FILESYSTEM2_SOURCE`, `FILESYSTEM2_DRIVER`, etc.

### Access Modes

- **passthrough** (virtiofs or 9p): guest UID/GID preserved. Adjust host permissions if needed.
- **mapped** / **squash** (9p only): libvirt maps IDs, so guest users can write without host-side changes.

### Troubleshooting

| Symptom | Fix |
| --- | --- |
| `virtiofsd ... unshare failed with EPERM` | Add `--cap-add SYS_ADMIN --security-opt seccomp=unconfined` |
| Cannot write from guest (passthrough) | Fix host permissions, or use `9p` with `FILESYSTEM_ACCESSMODE=mapped` |
| Share not present in guest | Check `docker logs` for `Filesystem #...` messages |
