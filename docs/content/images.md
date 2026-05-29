# Images

`docker-vm-runner` uses image IDs from `os-iso-catalog`.

The default catalog is:

```text
https://munenick.github.io/os-iso-catalog/v1/all.json
```

## List images

```bash
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros
```

Filter by architecture:

```bash
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros --arch amd64
```

Filter by image type:

```bash
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros --type cloud-image
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros --type iso
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros --type disk-image
```

Search by text:

```bash
docker run --rm \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros --search debian
```

## Run a catalog image

Use `DISTRO` with a catalog image ID:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Image types

| Type | Use case |
| --- | --- |
| `cloud-image` | Fast startup with cloud-init and a ready-to-boot disk image. |
| `iso` | Installer media. The VM starts from the ISO with a blank disk. |
| `disk-image` | A ready disk image that is not categorized as a cloud image. |

Cloud images are usually the best starting point for day-to-day use.

Use ISO images when you want to run an installer.

## ISO installs

When an ISO-backed persistent VM stops after installation, `docker-vm-runner` treats it as installed. The next run reuses the VM disk and boots from disk instead of attaching the ISO again.

Keep the ISO attached on the next run when you need to boot the installer or rescue media again:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=https://example.com/installer.iso \
  -e FORCE_ISO=1 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Boot from a custom source

Use `BOOT_FROM` for a URL, local path, OCI reference, or ISO.

Remote image:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=https://example.com/image.qcow2 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Installer ISO:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=https://example.com/installer.iso \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Local file:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -v "$PWD/images:/boot-media:ro" \
  -v docker-vm-runner-data:/data \
  -e BOOT_FROM=/boot-media/image.qcow2 \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Catalog cache

The catalog can be cached under `/config`:

```bash
docker run --rm \
  -v docker-vm-runner-config:/config \
  ghcr.io/munenick/docker-vm-runner:latest --list-distros
```

Use `CATALOG_OFFLINE=1` when you want to use the local catalog cache only.
