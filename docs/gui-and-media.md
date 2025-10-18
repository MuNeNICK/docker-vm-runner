# GUI & Installation Media

Docker-VM-Runner can expose a web-based console via noVNC and boot from local ISO media or blank disks for desktop installs.

## Enabling the noVNC Console

Set `GRAPHICS=novnc` (and optionally `NO_CONSOLE=1`) to launch the bundled websockify/noVNC stack. Bind ports 5900 (VNC) and 6080 (noVNC) if you want to reach it from the host.

```bash
docker run --rm \
  --name vm1 \
  --device /dev/kvm:/dev/kvm \
  -p 2222:2222 \
  -p 6080:6080 \
  -e GRAPHICS=novnc \
  -e NO_CONSOLE=1 \
  ghcr.io/munenick/docker-vm-runner:latest
```

Navigate to `https://localhost:6080/` and the viewer will auto-connect (`autoconnect=1`) and scale to the browser window. The TLS certificate is the same self-signed cert generated for Redfish.

## Booting from an Installation ISO

1. Place the ISO under `./images/base/` (e.g., `./images/base/ubuntu-24.04.3-desktop-amd64.iso`).
2. Bind-mount `./images` and (optionally) `./images/state` so disks and certificates persist.
3. Set `BOOT_ISO` to the in-container path (`/images/base/...`). If you don’t specify a base disk, Docker-VM-Runner automatically provisions a blank QCOW2 sized by `DISK_SIZE`.

Example: Ubuntu Desktop with noVNC and a 40G blank disk.

```bash
docker run --rm \
  --name ubuntu-desktop-vm \
  --device /dev/kvm:/dev/kvm \
  --security-opt apparmor=unconfined \
  -v "$(pwd)/images:/images" \
  -v "$(pwd)/images/state:/var/lib/docker-vm-runner" \
  -v "$(pwd)/distros.yaml:/config/distros.yaml:ro" \
  -p 2222:2222 \
  -p 6080:6080 \
  -e GUEST_NAME=ubuntu-desktop \
  -e GRAPHICS=novnc \
  -e NO_CONSOLE=1 \
  -e DISK_SIZE=40G \
  -e BOOT_ISO=/images/base/ubuntu-24.04.3-desktop-amd64.iso \
  -e BOOT_ORDER=cdrom,hd \
  -e CLOUD_INIT=0 \
  -e EXTRA_ARGS="-device virtio-gpu-pci,edid=on,xres=1920,yres=1080" \
  docker-vm-runner-novnc-test

# Add Redfish support if required:
#   -e REDFISH_ENABLE=1 -p 8443:8443
```

Notes:

- After installation, drop `BOOT_ISO` and set `BOOT_ORDER=hd` to boot directly from disk.
- To reuse an existing base disk, set `BASE_IMAGE=/images/base/<disk>.qcow2` instead of relying on the blank-disk automatic path.
- Cloud-init can stay enabled (default) to inject credentials, or disable it with `CLOUD_INIT=0` when installing manually.

## Display Scaling & Resolution

The viewer scales to fit the browser window, but QEMU’s VNC server keeps whatever resolution the guest exposes. For higher resolutions:

- Use a GPU device that advertises larger EDID: `-e EXTRA_ARGS="-device virtio-gpu-pci,edid=on,xres=1920,yres=1080"`.
- Configure the guest OS (e.g., `xrandr`) to switch to the desired resolution after boot.
