# Use Cases

These examples start from common VM workflows. Replace image IDs, ports, disk sizes, and interface names for your environment.

## Temporary VM

Use this when you want to try a VM and discard it when the container exits.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Persistent VM

Use `/data` when you want to keep the VM disk and image cache.

```bash
docker run --rm -it \
  --name ubuntu-vm \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e GUEST_NAME=ubuntu-vm \
  -v ubuntu-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Sized Cloud VM

Use this when the default CPU, memory, or disk size is too small.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e CPUS=4 \
  -e MEMORY=8192 \
  -e DISK_SIZE=40G \
  -v ubuntu-dev-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## ISO Install To Disk

Use this when you want to install from an ISO and boot the installed VM later.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=https://example.com/installer.iso \
  -e GUEST_NAME=installed-vm \
  -e CPUS=4 \
  -e MEMORY=8192 \
  -e DISK_SIZE=80G \
  -v installed-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

After the persistent VM stops, the next run boots from the installed disk. Set `FORCE_ISO=1` when you need to attach the ISO again.

## Rescue Or Repeat ISO Boot

Use `FORCE_ISO=1` for rescue media, live images, or repeated installer boots.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e BOOT_FROM=https://example.com/rescue.iso \
  -e FORCE_ISO=1 \
  -e GUEST_NAME=rescue-vm \
  -v rescue-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Background SSH VM

Use `-dit` when you want the VM to keep running and connect with SSH.

```bash
docker run -dit --name ubuntu-ssh \
  --device /dev/kvm \
  -p 2222:2222 \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e GUEST_NAME=ubuntu-ssh \
  -e SSH_PUBKEY="$(cat ~/.ssh/id_ed25519.pub)" \
  -v ubuntu-ssh-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Connect with:

```bash
ssh user@localhost -p 2222
```

Use `docker attach ubuntu-ssh` when you need the serial console. Press `Ctrl+P` then `Ctrl+Q` to detach without stopping the VM.

## Browser Console For GUI Installers

Use noVNC when the installer or desktop workflow needs a graphical console.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 6080:6080 \
  -e GRAPHICS=novnc \
  -e BOOT_FROM=https://example.com/desktop-installer.iso \
  -e GUEST_NAME=desktop-install \
  -e MEMORY=8192 \
  -e DISK_SIZE=80G \
  -v desktop-install-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Open:

```text
https://localhost:6080/
```

## Server VM With Extra Ports

Use `PORT_FWD` when a service inside the VM should be reachable from the host.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 8080:8080 \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e PORT_FWD=8080:80 \
  -v web-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

This forwards host `localhost:8080` to guest port `80` through the default NAT network.

## LAN VM With Bridge

Use Bridge when the host already has a bridge such as `br0` and the VM should receive an address from that network.

```bash
docker run -d --name bridge-vm \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/kvm \
  --device /dev/net/tun \
  --device /dev/vhost-net \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e NETWORK_MODE=bridge \
  -e NETWORK_BRIDGE=br0 \
  -e NO_CONSOLE=1 \
  -v bridge-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Connect to the guest through the address it receives on the bridged network.

## Physical Network VM With Direct

Use Direct when the VM needs a real address through a physical NIC but you do not want to create a host bridge.

```bash
MACVTAP_MAJOR="$(awk '$2 == "macvtap" { print $1 }' /proc/devices)"
if [ -z "$MACVTAP_MAJOR" ]; then
  echo "macvtap is not available on this host" >&2
  exit 1
fi

docker run -d --name direct-vm \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/kvm \
  --device /dev/vhost-net \
  --device-cgroup-rule "c ${MACVTAP_MAJOR}:* rwm" \
  -v /dev:/dev:ro \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e NETWORK_MODE=direct \
  -e NETWORK_DIRECT_DEV=eth0 \
  -e NO_CONSOLE=1 \
  -v direct-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Replace `eth0` with the host interface from `ip -br link`.

## Network Boot With iPXE

Use iPXE when the VM should boot from your network boot infrastructure.

```bash
docker run --rm -it \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/kvm \
  --device /dev/net/tun \
  --device /dev/vhost-net \
  -e IPXE_ENABLE=1 \
  -e BOOT_ORDER=network,hd \
  -e NETWORK_MODE=bridge \
  -e NETWORK_BRIDGE=br0 \
  -e GUEST_NAME=ipxe-vm \
  -v ipxe-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Use the bridge or direct network mode that reaches your DHCP/TFTP/HTTP boot services.

## Share A Host Directory

Use filesystem sharing when the VM should read or write a host directory.

```bash
mkdir -p share

docker run --rm -it \
  --device /dev/kvm \
  -v "$PWD/share:/shared" \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e FILESYSTEM_SOURCE=/shared \
  -e FILESYSTEM_TARGET=shared \
  -e FILESYSTEM_DRIVER=9p \
  -e FILESYSTEM_ACCESSMODE=mapped \
  -v shared-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Inside the guest:

```bash
sudo mkdir -p /mnt/shared
sudo mount -t 9p -o trans=virtio,version=9p2000.L shared /mnt/shared
```

## VM With Extra Disk

Use an extra disk when application data or test data should be separate from the boot disk.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e DISK_SIZE=40G \
  -e DISK_TYPE=scsi \
  -e DISK2_SIZE=100G \
  -v storage-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

The guest sees the extra disk as another block device.

## Redfish-Controlled VM

Use Redfish when another tool should control VM power or boot behavior through a BMC-like API.

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 8443:8443 \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e REDFISH_ENABLE=1 \
  -e REDFISH_PASSWORD='change-me' \
  -v redfish-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Check the endpoint:

```bash
curl -k -u admin:change-me https://localhost:8443/redfish/v1/
```

## Preview Before Starting

Use `--show-config` or `--dry-run` before starting a complex VM.

```bash
docker run --rm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e CPUS=4 \
  -e MEMORY=8192 \
  -e NETWORK_MODE=nat \
  -e PORT_FWD=8080:80 \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```

Validate without starting:

```bash
docker run --rm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e CPUS=4 \
  -e MEMORY=8192 \
  ghcr.io/munenick/docker-vm-runner:latest --dry-run
```
