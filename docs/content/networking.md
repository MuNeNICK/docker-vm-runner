# Networking

The default network is NAT. It works well for the same workflow as `docker run`: start a VM, publish the ports you need, and keep the host network unchanged.

Bridge and direct modes are available when you want the VM to join a network more like a normal machine. Those modes depend on host networking, so their Docker options are different from the default NAT examples.

## Default NAT

NAT uses libvirt user-mode networking. The VM gets an internal address, and selected ports are forwarded to the container.

Publish the default SSH port:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 2222:2222 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Connect with:

```bash
ssh user@localhost -p 2222
```

The default login is `user` with password `password`.

## Additional Ports

Use `PORT_FWD` to forward another container port to a VM port:

```bash
docker run --rm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e PORT_FWD=8080:80 \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

The format is `container_port:guest_port`. When you boot the VM, publish the same container port with Docker:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 8080:8080 \
  -e PORT_FWD=8080:80 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Use comma-separated values for multiple forwards:

```bash
-e PORT_FWD=8080:80,8443:443
```

`PORT_FWD` applies to NAT NICs only. If every NIC is bridge or direct, there is no user-mode NAT device to own the forwarded port.

## Multiple NICs

The first NIC uses unnumbered variables. Additional NICs use `NETWORK2_...`, `NETWORK3_...`, and so on.

Keep NAT as the management path and add a second bridged NIC:

```bash
docker run --rm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e NETWORK_MODE=nat \
  -e NETWORK2_MODE=bridge \
  -e NETWORK2_BRIDGE=docker0 \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

This pattern keeps `SSH_PORT` and `PORT_FWD` available on the NAT NIC while the second NIC joins the bridge.

## Bridge Mode

Bridge mode connects the VM NIC to an existing Linux bridge such as `br0`. Use it when the VM should appear on the bridged network instead of only behind NAT.

Example: use Bridge when your host already has `br0` on the LAN and you want the VM to receive an address from that LAN.

Required variables:

```bash
-e NETWORK_MODE=bridge
-e NETWORK_BRIDGE=br0
```

Preview the configuration:

```bash
docker run --rm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e NETWORK_MODE=bridge \
  -e NETWORK_BRIDGE=docker0 \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

To boot with bridge mode from Docker, the bridge must be visible to the container and libvirt must be able to create a tap device. One verified headless shape is:

```bash
docker run -d --name docker-vm-runner-bridge \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/kvm \
  --device /dev/net/tun \
  --device /dev/vhost-net \
  -e NETWORK_MODE=bridge \
  -e NETWORK_BRIDGE=docker0 \
  -e NO_CONSOLE=1 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Replace `docker0` with the bridge you actually want to use. For a VM on your LAN, that is usually a host bridge such as `br0`, not Docker's default bridge.

When bridge mode is the only NIC, connect to the VM through the bridged network address. Docker `-p`, `SSH_PORT`, and `PORT_FWD` are not the access path for that NIC.

## Direct Mode

Direct mode attaches the VM NIC to a host interface without creating a Linux bridge first. Use it when you need the VM to use a specific host NIC path and you understand the host network exposure.

Example: use Direct when the VM needs a real address on the physical network through `eth0`, but you do not want to create a host bridge.

Required variables:

```bash
-e NETWORK_MODE=direct
-e NETWORK_DIRECT_DEV=eth0
```

Use the real host interface name from `ip -br link`.

Preview the configuration:

```bash
docker run --rm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e NETWORK_MODE=direct \
  -e NETWORK_DIRECT_DEV=eth0 \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

To boot with Direct mode from Docker, allow Docker to expose the dynamic macvtap devices created for the VM:

```bash
MACVTAP_MAJOR="$(awk '$2 == "macvtap" { print $1 }' /proc/devices)"

docker run -d --name docker-vm-runner-direct \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/kvm \
  --device /dev/vhost-net \
  --device-cgroup-rule "c ${MACVTAP_MAJOR}:* rwm" \
  -v /dev:/dev:ro \
  -e NETWORK_MODE=direct \
  -e NETWORK_DIRECT_DEV=eth0 \
  -e NO_CONSOLE=1 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

`/dev/vhost-net` is used by the default `virtio` NIC. If you set `NETWORK_MODEL=e1000`, Direct mode can run without `/dev/vhost-net`, but the guest NIC model changes.

For day-to-day access, a practical pattern is NAT plus direct:

```bash
docker run --rm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  -e NETWORK_MODE=nat \
  -e NETWORK2_MODE=direct \
  -e NETWORK2_DIRECT_DEV=eth0 \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

The NAT NIC keeps Docker-style port forwarding available; the direct NIC is available for network tests that need the host interface path.

## NIC Details

Use these variables when the guest or network requires fixed interface properties:

| Variable | Purpose |
| --- | --- |
| `NETWORK_MAC` | Set a stable MAC address. |
| `NETWORK_MODEL` | Set the NIC model. The default is `virtio`. |
| `NETWORK_MTU` | Set the NIC MTU. |
| `NETWORK_GUEST_IP` | Set guest IPv4 addresses for NAT mode. |
| `NETWORK_GUEST_IP6` | Set guest IPv6 addresses for NAT mode. |
| `NETWORK_BOOT` | Mark the NIC as bootable for network boot flows. |

See [Reference](reference.md#network-variables) for the complete variable list.
