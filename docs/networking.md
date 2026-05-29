# Networking and Access

## Console

The default mode attaches to the VM serial console.

```bash
docker run --rm -it --privileged \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

Use `--no-console` or `NO_CONSOLE=1` for headless operation.

## SSH forwarding

With the default user-mode NIC, host port `2222` is forwarded to guest port `22`.

```bash
docker run --rm -it --privileged \
  -p 2222:2222 \
  -e SSH_PORT=2222 \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

The generated default login user is `user` unless the catalog provides a different value. The default password is `password`.

## Additional port forwarding

Use `PORT_FWD` for extra forwards:

```bash
docker run --rm -it --privileged \
  -p 8080:8080 \
  -e PORT_FWD=8080:80 \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

## noVNC

Enable noVNC with `GRAPHICS=novnc`:

```bash
docker run --rm -it --privileged \
  -p 6080:6080 \
  -e GRAPHICS=novnc \
  -v docker-vm-runner-data:/data \
  docker-vm-runner:dev
```

Open:

```text
https://localhost:6080/
```

noVNC static assets are provided by the container image. They are not stored in this repository.

## VNC

Use `GRAPHICS=vnc` to expose the VNC server without noVNC.

| Variable | Default | Description |
| --- | --- | --- |
| `VNC_PORT` | `5900` | VNC port inside the container. |
| `NOVNC_PORT` | `6080` | noVNC HTTPS port. |
| `VNC_KEYMAP` | empty | Optional VNC keymap. |
