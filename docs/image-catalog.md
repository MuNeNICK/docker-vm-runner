# Image Catalog

`docker-vm-runner` uses `os-iso-catalog` at runtime. The catalog is not vendored into this repository.

The default catalog URL is:

```text
https://munenick.github.io/os-iso-catalog/v1/all.json
```

## Catalog IDs

`DISTRO` uses the catalog image ID directly:

```bash
docker run --rm -it --privileged \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

Old Python-era distro aliases are not preserved in v2.

## Listing and filtering

```bash
docker run --rm --privileged docker-vm-runner:dev --list-distros
docker run --rm --privileged docker-vm-runner:dev --list-distros --arch amd64
docker run --rm --privileged docker-vm-runner:dev --list-distros --type cloud-image
docker run --rm --privileged docker-vm-runner:dev --list-distros --search debian
```

The `--type` filter accepts:

- `cloud-image`
- `iso`
- `disk-image`

## Cache and offline use

| Variable | Description |
| --- | --- |
| `CATALOG_URL` | Override the catalog URL or use a local file path. |
| `CATALOG_CACHE` | Path where the fetched catalog is cached. |
| `CATALOG_OFFLINE` | Load only from `CATALOG_CACHE`. |

By default, the cache path is:

```text
/config/os-iso-catalog/v1/all.json
```

Mount `/config` if you want the catalog cache to survive container removal.
