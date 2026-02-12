# Quick Start

Run lightweight QEMU VMs from the published container image (`ghcr.io/munenick/docker-vm-runner:latest`). Each container hosts a single VM orchestrated by libvirt and sushy for Redfish management.

## One-Shot, Ephemeral VM

```bash
docker run --rm -it \
  --name vm1 \
  --hostname vm1 \
  -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  ghcr.io/munenick/docker-vm-runner:latest
```

- SSH: `ssh -p 2222 <user>@localhost` (user defaults to the image’s `login_user`).
- Redfish (when enabled): `https://localhost:8443` (default credentials `admin` / `password`).

To launch a different distro or adjust resources:

```bash
docker run --rm -it \
  --name vm1 \
  -p 2201:2201 \
  --device /dev/kvm:/dev/kvm \
  -e DISTRO=debian-12 \
  -e MEMORY=2048 \
  -e CPUS=4 \
  -e SSH_PORT=2201 \
  ghcr.io/munenick/docker-vm-runner:latest

```

## Persisting Disks & Certificates

Bind-mount a host directory to `/images` and opt into persistence:

```bash
mkdir -p ./images ./images/state

docker run --rm -it \
  --name vm1 \
  -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/images:/images" \
  -v "$PWD/images/state:/var/lib/docker-vm-runner" \
  -e PERSIST=1 \
  ghcr.io/munenick/docker-vm-runner:latest

```

Container storage layout:

- `/images/base/<distro>.qcow2` — cached cloud images per distro.
- `/images/vms/<name>/disk.qcow2` — working disk (retained when `PERSIST=1`).
- `/images/vms/<name>/seed.iso` — regenerated cloud-init seed (only when cloud-init is enabled).
- `/var/lib/docker-vm-runner` — management state (Redfish certificates, etc.).

> **Note:** When `PERSIST=1`, cloud-init only runs on the first boot (keyed by the VM name as `instance-id`). Changing `GUEST_PASSWORD` or other cloud-init settings on subsequent runs will not take effect unless you also change `GUEST_NAME` or delete the persistent disk.

## Custom cloud-init

The container always injects a vendor cloud-config that creates the default login user and password. Supply a second, user-controlled stage by mounting a file and pointing `CLOUD_INIT_USER_DATA` at it:

```bash
cat <<'EOF' > ./cloud-init/user-data.yaml
#cloud-config
packages:
  - htop
runcmd:
  - ['bash', '-lc', 'echo hello from user-data']
EOF

docker run --rm -it \
  --name vm1 \
  -p 2222:2222 \
  --device /dev/kvm:/dev/kvm \
  -v "$PWD/cloud-init:/cloud-init:ro" \
  -e CLOUD_INIT_USER_DATA=/cloud-init/user-data.yaml \
  ghcr.io/munenick/docker-vm-runner:latest
```

The file can contain any cloud-init payload (`#cloud-config`, shell script, boothook, etc.). It is attached as the second part of a multipart NoCloud seed so it runs after the built-in configuration, mirroring the “vendor data + user data” flow seen on EC2.

## Console & Logs

- Attach to the serial console: `virsh console <vm_name>` (inside container) or rely on the container entrypoint (unless `NO_CONSOLE=1`).
- Detach from console: `Ctrl+]`.
- Logs: `docker logs -f vm1` (compose: `docker compose logs vm1`).
- Exec shell inside the container: `docker exec -it vm1 /bin/bash`.

## Running Commands Inside the Guest

The built-in `guest-exec` command lets you run commands inside the VM non-interactively via the QEMU Guest Agent — no SSH required:

```bash
docker exec vm1 guest-exec "uname -a"
docker exec vm1 guest-exec "cat /etc/os-release"
docker exec vm1 guest-exec "systemctl status nginx"
```

The command captures stdout/stderr and propagates the guest exit code. Cloud images automatically install and start `qemu-guest-agent` via cloud-init, so `guest-exec` is available once cloud-init completes (typically 1-2 minutes after boot).

If the guest agent is not yet running, `guest-exec` prints a clear error message and exits with code 127.

## Custom distros.yaml

Override the built-in mapping by bind-mounting your own file:

```bash
docker run --rm -it \
  --name vm1 \
  --device /dev/kvm:/dev/kvm \
  -p 2222:2222 \
  -v "$PWD/distros.yaml:/config/distros.yaml:ro" \
  ghcr.io/munenick/docker-vm-runner:latest

```
