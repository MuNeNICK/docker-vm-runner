# Access and GUI

`docker-vm-runner` supports serial console, SSH, `guest-exec`, VNC, noVNC, and
Redfish. Always publish the matching Docker port when accessing the VM from the
host.

## Serial console

Attached console:

```bash
docker run --rm -it \
  --name docker-vm-runner \
  --device /dev/kvm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Background with later attach:

```bash
docker run -dit --name docker-vm-runner \
  --device /dev/kvm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest

docker attach docker-vm-runner
```

Use `Ctrl+P` then `Ctrl+Q` to detach from Docker while leaving the VM running.
`Ctrl+]` exits the VM console session.

## SSH

Default NAT forwards container port `2222` to guest port `22`. Publish it:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 2222:2222 \
  -e SSH_PUBKEY="$(cat ~/.ssh/id_ed25519.pub)" \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest

ssh user@localhost -p 2222
```

The default login is `user` with password `password`. Set
`GUEST_PASSWORD='change-me'` to change the password.

## noVNC

Use noVNC for GUI installers and desktop workflows:

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

The endpoint uses a self-signed certificate. `GRAPHICS=novnc` skips serial
console attachment by default; set `NO_CONSOLE=0` if the terminal should attach
to the serial console too.

## VNC

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 5900:5900 \
  -e GRAPHICS=vnc \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Connect a VNC client to `localhost:5900`.

## Extra port forwarding

Use `PORT_FWD` with NAT and publish the same container port with Docker:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 8080:8080 \
  -e PORT_FWD=8080:80 \
  -v web-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Multiple forwards are comma-separated:

```bash
-e PORT_FWD=8080:80,8443:443
```

`PORT_FWD` applies to NAT NICs only.

## Redfish

Use Redfish when another tool should control VM power or boot behavior through
a BMC-like API. Change the default password when enabling it:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 8443:8443 \
  -e REDFISH_ENABLE=1 \
  -e REDFISH_PASSWORD='change-me' \
  -v redfish-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest

curl -k -u admin:change-me https://localhost:8443/redfish/v1/
```
