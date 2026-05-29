# Workflows

Use these command templates as starting points. Replace names, images, ports,
disk sizes, and host paths for the task.

## Agent command VM

Best default for AI agents that need a real VM:

```bash
docker run -dit --name command-vm \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e GUEST_NAME=command-vm \
  -v command-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest

docker exec command-vm guest-exec --wait "uname -a"
```

## Temporary VM

```bash
docker run --rm -it \
  --device /dev/kvm \
  -e DISTRO=alpine-3.22-cloud-amd64 \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Persistent VM

```bash
docker run --rm -it \
  --name ubuntu-vm \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e GUEST_NAME=ubuntu-vm \
  -v ubuntu-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Sized cloud VM

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

## GUI installer with noVNC

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

Open `https://localhost:6080/`.

## SSH VM

```bash
docker run -dit --name ubuntu-ssh \
  --device /dev/kvm \
  -p 2222:2222 \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e GUEST_NAME=ubuntu-ssh \
  -e SSH_PUBKEY="$(cat ~/.ssh/id_ed25519.pub)" \
  -v ubuntu-ssh-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest

ssh user@localhost -p 2222
```

## Server VM with an exposed guest service

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 8080:8080 \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e PORT_FWD=8080:80 \
  -v web-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

This forwards host `localhost:8080` to guest port `80`.

## ISO install to disk

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

After installation, the next persistent run boots from disk. Set `FORCE_ISO=1`
for rescue or repeated ISO boot.

## Bridge VM

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

## Redfish-controlled VM

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 8443:8443 \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e REDFISH_ENABLE=1 \
  -e REDFISH_PASSWORD='change-me' \
  -v redfish-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest

curl -k -u admin:change-me https://localhost:8443/redfish/v1/
```

## Preview complex configuration

```bash
docker run --rm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e CPUS=4 \
  -e MEMORY=8192 \
  -e NETWORK_MODE=nat \
  -e PORT_FWD=8080:80 \
  ghcr.io/munenick/docker-vm-runner:latest --show-config
```
