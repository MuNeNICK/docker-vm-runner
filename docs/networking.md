# Networking Guide

This project keeps QEMU’s user-mode NAT as the default because it “just works” with a single `docker run`. When you need a routable address on the VM, switch to bridge or direct/macvtap mode by setting environment variables and adjusting container privileges.

## Default: NAT (user-mode)

- Works out of the box; no host networking changes.
- Container exposes the SSH/Redfish/VNC ports defined in `docker-compose.yml` or your `docker run` command.
- Ideal for quick tests or development shells where port forwarding is sufficient.

## Bridge Mode (libvirt bridge)

Bridge mode attaches the guest NIC to a pre-existing Linux bridge on the host (e.g., `br0`). The guest receives an address from whatever network is connected to that bridge.

1. Prepare the host bridge (example):
   ```bash
   sudo ip link add name br0 type bridge
   sudo ip link set dev br0 up
   sudo ip link set dev eth0 master br0  # or use a dedicated NIC
   ```
2. Start the container with extra permissions:
   ```bash
   docker run --rm -it \\
     --privileged \\                       # or: --cap-add NET_ADMIN --device /dev/net/tun
     --network host \\                     # libvirt needs host networking to tap the bridge
     -e NETWORK_MODE=bridge \\
     -e NETWORK_BRIDGE=br0 \\
     ghcr.io/munenick/docker-vm-runner:latest
   ```
3. The guest now appears directly on the bridged network. Use DHCP or configure a static IP through cloud-init.

### Static addressing with cloud-init

Create a `network-config.yaml` under `/images/cloud-init/` (bind-mount the directory) and reference it via `EXTRA_ARGS='--nicparm'` or render static IPs inside `user-data`—see the distribution’s cloud-init documentation for field names.

## Direct Mode (macvtap)

Direct mode (libvirt `type='direct'`) connects the guest to a physical NIC using macvtap. It is useful when you cannot modify the host network bridge but still need a layer-2 presence.

```bash
docker run --rm -it \\
  --privileged \\                         # direct/macvtap requires elevated networking privileges
  --network host \\
  -v /dev:/dev \\                         # bind-mount host /dev so /dev/tap* is visible
  -e NETWORK_MODE=direct \\
  -e NETWORK_DIRECT_DEV=eth1 \\
  ghcr.io/munenick/docker-vm-runner:latest
```

Notes:

- Some hypervisors block MAC spoofing on host NICs; allow it if your upstream switch enforces port security.
- macvtap traffic is not visible to the host IP stack. Use bridge mode if the host must communicate with the guest.
- Ensure the host kernel has `macvtap`/`macvlan` loaded. Libvirt will create `/dev/tap*` automatically on the host, and the bind-mounted `/dev` makes it visible inside the container.

## Choosing a mode

| Requirement | Recommended mode |
| --- | --- |
| Quick SSH access via forwarded port | `nat` (default) |
| Guest needs an address on the same LAN/subnet as the host | `bridge` |
| No bridge available, but the guest must appear on the physical network | `direct` |

After changing the networking mode, restart the container. Persisted domains (`PERSIST=1`) should be undefined or updated before reusing with a different mode.
