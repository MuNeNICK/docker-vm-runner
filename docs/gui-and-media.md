# GUI & Installation Media

## Quick Examples

**VM with noVNC web console:**

```bash
docker run --rm -it \
  --device /dev/kvm:/dev/kvm \
  -p 6080:6080 \
  -e GRAPHICS=novnc \
  ghcr.io/munenick/docker-vm-runner:latest
```

Open `https://localhost:6080/` in your browser.

**Boot from an ISO installer:**

```bash
docker run --rm -it \
  --device /dev/kvm:/dev/kvm \
  -p 6080:6080 \
  -e BOOT_FROM=https://releases.ubuntu.com/24.04/ubuntu-24.04.3-live-server-amd64.iso \
  -e GRAPHICS=novnc \
  ghcr.io/munenick/docker-vm-runner:latest
```

`BOOT_FROM` accepts both URLs (auto-downloaded) and local paths:

```bash
docker run --rm -it \
  --device /dev/kvm:/dev/kvm \
  -p 6080:6080 \
  -v ./my.iso:/boot.iso:ro \
  -e BOOT_FROM=/boot.iso \
  -e GRAPHICS=novnc \
  ghcr.io/munenick/docker-vm-runner:latest
```

When an ISO is detected (by `.iso` extension), the following are auto-configured:
- `BOOT_ORDER` includes `cdrom`
- `CLOUD_INIT=0` (manual install assumed)
- A blank work disk is created (unless `BLANK_DISK` is explicitly set)

## Detailed Options

### noVNC Settings

| Variable | Default | Description |
| --- | --- | --- |
| `GRAPHICS` | `none` | Set to `novnc` for VNC + web console. |
| `VNC_PORT` | `5900` | VNC listen port. |
| `NOVNC_PORT` | `6080` | noVNC web port. |

`GRAPHICS=novnc` auto-disables the serial console (override with `NO_CONSOLE=0`). The TLS certificate is shared with Redfish (self-signed).

### ISO Boot Details

For a local ISO, specify the in-container path:

```bash
-v "$(pwd)/images:/images" \
-e BOOT_FROM=/images/ubuntu-desktop.iso
```

For a URL, the ISO is downloaded and cached inside the container. To persist the cache, mount a volume at `/data`:

```bash
-v myvm:/data \
-e BOOT_FROM=https://releases.ubuntu.com/24.04/ubuntu-24.04.3-desktop-amd64.iso
```

When using a persistent volume (`-v myvm:/data`), you can keep the same `docker run` command for both initial installation and later boots. The ISO is automatically skipped on subsequent boots once the VM has been installed. To force the ISO to attach again, set `FORCE_ISO=1`.

### Display Resolution

To increase the default VGA resolution:

```
-e EXTRA_ARGS="-device virtio-gpu-pci,edid=on,xres=1920,yres=1080"
```

The guest OS may also need configuration (e.g., `xrandr`).
