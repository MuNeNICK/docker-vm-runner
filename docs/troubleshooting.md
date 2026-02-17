# Troubleshooting & Operations

## Common Issues

- **KVM unavailable**  
  Check `/dev/kvm` with `ls -la /dev/kvm`. The container requires hardware virtualization; if the device is missing or unreadable, libvirt fails to start the guest. Ensure the module is loaded and your user has access to the `kvm` group (or run the container with sufficient privileges).

- **Unknown distribution**  
  Verify `DISTRO` matches a key in `distros.yaml` and that the file is available inside the container at `/config/distros.yaml`.

- **libvirt cgroup error on VM start**
  If you see an error about cgroups when starting the VM, add `--cgroupns=host` to your `docker run` command (or `cgroupns_mode: host` in compose). This allows libvirt to access the host cgroup hierarchy.

- **Console input not accepted**  
  Ensure the container runs with `stdin_open: true` and `tty: true` (compose already sets these). Reattach with `docker attach <container>` or use `docker compose exec <service> virsh console <guest>`.

- **Redfish HTTPS warnings / failures**  
  Redfish is disabled unless `REDFISH_ENABLE=1`. When enabled, the manager generates a self-signed certificate under the state directory. Use `-v myvm:/data` for automatic persistence, or set `DATA_DIR` explicitly. Override credentials via `REDFISH_USERNAME` / `REDFISH_PASSWORD`.

For detailed Redfish enablement and workflows, see the [Redfish Guide](redfish.md).

- **UEFI boot fails with "OVMF firmware not found"**
  Ensure the container image includes the `ovmf` package. If using a custom image, install it with `apt-get install ovmf`.

- **TPM error: "swtpm not found"**
  The `swtpm` and `swtpm-tools` packages are required for `BOOT_MODE=secure` or `TPM=1`. These are included in the official image.

- **Network fallback warning (passt → slirp)**
  If the passt network backend fails (e.g., due to missing capabilities), the container automatically falls back to QEMU's built-in slirp networking. Performance and features may differ. Check container capabilities if this happens unexpectedly.

- **Podman rootless / Docker rootless**
  The container detects rootless mode automatically. When running rootless, libvirt socket timeouts and network backend errors are downgraded to warnings instead of fatal errors. Some features (bridge/direct networking, GPU passthrough) may not work in rootless mode. The runtime detection result is displayed in the Host info block at startup (e.g. `Runtime: podman (unprivileged, rootless)`).

- **BTRFS / OverlayFS performance warning**
  The container warns at startup if the storage path is on BTRFS (COW overhead) or OverlayFS (Docker's default). For BTRFS, disable COW on the data directory: `chattr +C /path/to/data`. For best performance, use a dedicated volume with ext4 or xfs.

- **Disk space warning**
  The container checks available disk space before creating VM disks. If space is tight, you'll see a warning. Use `DISK_SIZE=half` or reduce the requested size.

## Networking

Default networking uses QEMU user-mode NAT:

- Start: `docker compose up -d`
- SSH: `ssh -p $SSH_PORT <user>@localhost` (default `2222`)

To expose the VM on an upstream network, switch to bridge or direct/macvtap mode with the variables described in the [Networking Guide](networking.md).

## Compose Usage

`docker-compose.yml` defines a persistent VM service. Typical workflow:

```bash
docker compose up -d
docker compose logs -f vm1
docker compose exec vm1 /bin/bash
```

Override per-VM settings via environment variables (see [Configuration Reference](reference.md)).
