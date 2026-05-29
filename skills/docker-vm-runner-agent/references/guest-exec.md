# guest-exec

`guest-exec` runs commands inside the running VM through the QEMU guest agent.
Use it when the agent needs a real guest OS but does not need an interactive
login.

## Start a VM for command execution

```bash
docker run --rm -dit --name command-vm \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  ghcr.io/munenick/docker-vm-runner:latest
```

This is ephemeral. Add `-e GUEST_NAME=command-vm` and
`-v command-vm-data:/data` only when the user asks to keep VM state.

Follow startup logs when needed:

```bash
docker logs -f command-vm
```

## Run commands

Shell form, for pipes, redirects, variables, package-manager command chains, or
other shell features:

```bash
docker exec command-vm guest-exec --wait "apt-get update && apt-get install -y curl"
docker exec command-vm guest-exec --wait "uname -a"
```

Argv form, when shell features are not needed:

```bash
docker exec command-vm guest-exec --wait id user
docker exec command-vm guest-exec --wait systemctl is-system-running
```

Behavior:

- `--wait` waits for the QEMU guest agent before sending the command.
- stdout and stderr from the guest command are returned to the host terminal.
- `guest-exec` exits with the guest command's exit code.
- A single command string containing spaces runs through `/bin/sh -c`.
- Multiple arguments are sent as argv form.

## Agent practice

- Prefer `guest-exec --wait` for noninteractive OS checks, package operations,
  service probes, tests, and setup commands.
- Use SSH only when the workflow needs interactive login or tooling that expects
  SSH.
- If guest commands fail early after boot, retry with `--wait` and inspect
  `docker logs <container>`.
- For cloud images, cloud-init may still be finishing after the guest agent is
  reachable; package operations may need a retry or `cloud-init status --wait`.
