# PXE Lab Demo

iPXE boot demo for Docker-VM-Runner.

Before starting, make sure no other interface on the host already uses the subnet set in `.env` (default `192.0.2.0/24`).

## Quick Start
```bash
cd examples/pxe-lab

# Bring up the isolated bridge (adjust the env if you need different values)
source .env
sudo ip link add "$PXE_BRIDGE_NAME" type bridge
sudo ip addr add "${NETBOOT_ROUTER}/${NETBOOT_SUBNET#*/}" dev "$PXE_BRIDGE_NAME"
sudo ip link set "$PXE_BRIDGE_NAME" up
sudo sysctl -w net.ipv4.ip_forward=1
sudo iptables -t nat -A POSTROUTING -s "$NETBOOT_SUBNET" -o "$UPLINK_IF" -j MASQUERADE
sudo iptables -A FORWARD -i "$PXE_BRIDGE_NAME" -o "$UPLINK_IF" -j ACCEPT
sudo iptables -A FORWARD -o "$PXE_BRIDGE_NAME" -i "$UPLINK_IF" -m state --state RELATED,ESTABLISHED -j ACCEPT

# Boot the stack and watch the guests through noVNC
docker compose up -d
# Open https://localhost:6081/ and https://localhost:6082/ in your browser to view the guest consoles.

# Optional teardown once you are done
source .env
docker compose down
sudo iptables -t nat -D POSTROUTING -s "$NETBOOT_SUBNET" -o "$UPLINK_IF" -j MASQUERADE
sudo iptables -D FORWARD -i "$PXE_BRIDGE_NAME" -o "$UPLINK_IF" -j ACCEPT
sudo iptables -D FORWARD -o "$PXE_BRIDGE_NAME" -i "$UPLINK_IF" -m state --state RELATED,ESTABLISHED -j ACCEPT
sudo ip link set "$PXE_BRIDGE_NAME" down
sudo ip link delete "$PXE_BRIDGE_NAME" type bridge
```

If you change values in `.env`, tear down the existing rules with the old values before reapplying the updated configuration so the delete commands continue to match.

## What’s running
- **pxe-gateway** provides DHCP/TFTP/HTTP on the bridge address (`NETBOOT_IP`). In this sample it uses the netboot.xyz image to serve iPXE menus.
- **pxe-client / pxe-client-2** are docker-vm-runner guests that boot with an iPXE ROM on the bridged NIC, chain to the gateway, and expose noVNC consoles on ports 6081 / 6082 for monitoring installers.

Adjust `.env` if you need different addressing or forward a different uplink device; otherwise the defaults are ready for a quick smoke test.

[netboot.xyz](https://netboot.xyz/) provides the PXE menus leveraged in this example.
