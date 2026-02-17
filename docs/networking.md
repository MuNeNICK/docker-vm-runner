# Networking Guide

## Quick Start

By default, networking just works — no configuration needed:

```bash
docker run --rm -it \
  --device /dev/kvm:/dev/kvm \
  -p 2222:2222 \
  ghcr.io/munenick/docker-vm-runner:latest
```

SSH into the guest with `ssh -p 2222 user@localhost`.

Three modes are available:

| Mode | Use case | Extra privileges? |
| --- | --- | --- |
| `nat` (default) | SSH via port forwarding | No |
| `bridge` | Guest on the same LAN as the host | Yes (`--privileged --network host`) |
| `direct` | Guest on physical network without a bridge | Yes (`--privileged --network host -v /dev:/dev`) |

## NAT (default)

Works out of the box. The container forwards ports (SSH, VNC, Redfish) to the guest.

No environment variables needed — `NETWORK_MODE` defaults to `nat`.

## Bridge

Attaches the guest to a host Linux bridge. The guest gets an IP from the same network as the host.

```bash
docker run --rm -it \
  --device /dev/kvm:/dev/kvm \
  --privileged \
  --network host \
  -e NETWORK_MODE=bridge \
  -e NETWORK_BRIDGE=br0 \
  ghcr.io/munenick/docker-vm-runner:latest
```

Requires a pre-existing bridge on the host:

```bash
sudo ip link add name br0 type bridge
sudo ip link set dev br0 up
sudo ip link set dev eth0 master br0
```

## Direct (macvtap)

Connects the guest to a physical NIC without a bridge. Useful when you cannot modify the host network.

```bash
docker run --rm -it \
  --device /dev/kvm:/dev/kvm \
  --privileged \
  --network host \
  -v /dev:/dev \
  -e NETWORK_MODE=direct \
  -e NETWORK_DIRECT_DEV=eth1 \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Detailed Options

### Multiple NICs

Add a secondary NIC by appending an index: `NETWORK2_MODE`, `NETWORK2_MODEL`, `NETWORK2_MAC`, etc.

A common pattern is bridge + NAT fallback (bridge for LAN access, NAT for internet):

```bash
-e NETWORK_MODE=bridge \
-e NETWORK_BRIDGE=br0 \
-e NETWORK2_MODE=nat
```

### Static IP with cloud-init

Use `CLOUD_INIT_USER_DATA` to supply a cloud-config with static networking. See the distribution's cloud-init documentation for netplan/ENI syntax.

### MTU Auto-Detection

The host's default interface MTU is automatically detected. If it differs from 1500 (e.g. jumbo frames at 9000), the detected MTU is applied to the guest NIC. Override with `NETWORK_MTU`:

```bash
-e NETWORK_MTU=9000
```

Per-NIC override is supported with indexed variables: `NETWORK2_MTU`, `NETWORK3_MTU`, etc.

### IPv6

IPv6 is automatically enabled in user-mode (NAT) networking when the host has IPv6 support (detected via `/proc/net/if_inet6`). The guest receives a link-local IPv6 address (`fec0::2/64`) in addition to the IPv4 address.

### Direct mode caveats

- macvtap traffic is not visible to the host IP stack. Add `NETWORK2_MODE=nat` if you need host-to-guest connectivity via forwarded ports.
- If a second VM gets "Device or resource busy" on the same NIC, enable promiscuous mode: `ip link set dev eth1 promisc on`.
- The host kernel must have `macvtap`/`macvlan` modules loaded.
