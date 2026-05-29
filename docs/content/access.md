# Access

`docker-vm-runner` can expose the VM through the serial console, SSH, VNC, noVNC, and Redfish.

## Serial console

This command opens the VM console in your terminal:

```bash
docker run --rm -it \
  --name docker-vm-runner \
  --device /dev/kvm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Close the VM console session with `Ctrl+]`.

Use `Ctrl+P` then `Ctrl+Q` instead when you want to leave the VM running.

For a background container that you can reattach to later, start it with `-dit`:

```bash
docker run -dit --name docker-vm-runner \
  --device /dev/kvm \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Reconnect with:

```bash
docker attach docker-vm-runner
```

Use `Ctrl+P` then `Ctrl+Q` to leave the VM running.

## SSH

With the default NAT network, host port `2222` is forwarded to guest port `22`.

Publish the port from the container:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 2222:2222 \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Then connect from the host:

```bash
ssh user@localhost -p 2222
```

The default login is `user` with password `password`.

Set a different password:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 2222:2222 \
  -e GUEST_PASSWORD='change-me' \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Inject an SSH public key:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 2222:2222 \
  -e SSH_PUBKEY="$(cat ~/.ssh/id_ed25519.pub)" \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

## Extra port forwarding

Use `PORT_FWD` to forward additional ports through the default NAT network:

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

## noVNC

Enable browser access with noVNC:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 6080:6080 \
  -e GRAPHICS=novnc \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Open:

```text
https://localhost:6080/
```

The noVNC endpoint uses a self-signed certificate.

When `GRAPHICS=novnc` is set, the serial console is skipped by default. Set `NO_CONSOLE=0` if you want the container terminal to attach to the serial console as well.

## VNC

Use VNC without noVNC:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 5900:5900 \
  -e GRAPHICS=vnc \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

Connect your VNC client to `localhost:5900`.

## Redfish

Enable Redfish with a non-default password:

```bash
docker run --rm -it \
  --device /dev/kvm \
  -p 8443:8443 \
  -e REDFISH_ENABLE=1 \
  -e REDFISH_PASSWORD='change-me' \
  -v docker-vm-runner-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest
```

The Redfish endpoint listens on port `8443` by default.
