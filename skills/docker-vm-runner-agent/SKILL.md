---
name: docker-vm-runner-agent
description: Use docker-vm-runner as a Docker-started real VM sandbox for AI agent work, including guest-exec command execution, persistent or disposable VMs, GUI/noVNC installers, SSH access, port forwarding, Redfish, ISO boot, networking, storage, and cleanup.
license: MIT
---

# docker-vm-runner Agent

Use this skill when work should happen inside a real VM started by
`docker-vm-runner` instead of directly on the host or in an ordinary container.
This is the right tool for OS-level behavior, systemd, package managers, kernel
or firmware behavior, installer media, disk layout tests, GUI installers, and
other workflows where container isolation is not enough.

## Requirements

Assume Docker or a compatible Docker CLI/runtime is available unless the user
says otherwise. Do not install Docker or change the host container runtime unless
explicitly asked.

The host must be able to run the `ghcr.io/munenick/docker-vm-runner:latest`
container. `/dev/kvm` is recommended for performance but optional unless the
workflow sets `REQUIRE_KVM=1`. Advanced workflows may also require Docker device
access, capabilities, host networking, bind mounts, or published ports.

## Default approach

For AI agent command execution, prefer a background ephemeral VM and
`guest-exec`. Add `/data`, `GUEST_NAME`, or other persistence settings only
when the user asks to keep VM state across runs:

```bash
docker run --rm -dit --name command-vm \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  ghcr.io/munenick/docker-vm-runner:latest

docker exec command-vm guest-exec --wait "uname -a"
```

It is safe to run `guest-exec --wait` immediately after `docker run -dit`;
`--wait` waits for the VM domain to appear and for the QEMU guest agent to
connect before executing the command. Use `docker logs -f command-vm` when you
want to watch startup progress.

If `/dev/kvm` is unavailable, remove `--device /dev/kvm`. If the task requires
hardware acceleration, set `-e REQUIRE_KVM=1` and fail early when KVM is not
available.

Use `--show-config`, `--show-xml`, or `--dry-run` before starting a complex VM:

```bash
docker run --rm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e CPUS=4 \
  -e MEMORY=8192 \
  ghcr.io/munenick/docker-vm-runner:latest --dry-run
```

## Workflow selection

- Need to run shell commands in a real OS without SSH: use `guest-exec`.
- Need one-off agent work: use an ephemeral background VM with `--rm -dit`.
- Need repeatable agent work or state retention: use `-dit`, `--name`,
  `GUEST_NAME`, and `/data` when the user asks for persistence.
- Need a GUI installer or desktop: use `GRAPHICS=novnc` and publish `6080`.
- Need network service testing: use `PORT_FWD` plus matching Docker `-p`.
- Need direct login: publish `SSH_PORT` and use the default `user` account,
  `GUEST_PASSWORD`, or `SSH_PUBKEY`.
- Need BMC-like control: use Redfish with a non-default `REDFISH_PASSWORD`.
- Need installer media, rescue media, blank disk, or iPXE: choose the boot
  workflow before starting the VM.
- Need host files, extra disks, or block devices: choose the storage workflow
  and expose the required host path or device to the container.

## References

Load only the needed reference file:

- `references/workflows.md`: task-oriented Docker command templates.
- `references/guest-exec.md`: command execution rules and examples.
- `references/access-gui.md`: serial console, SSH, VNC, noVNC, and Redfish.
- `references/boot-network-storage.md`: boot sources, ISO install, iPXE,
  networking, filesystem sharing, disks, and block devices.
- `references/options.md`: public flags and environment variables used by
  supported workflows.
- `references/operations.md`: readiness checks, logs, stop behavior, and
  cleanup.

## Rules

- Do not run host-mutating commands directly when the user asked for VM
  isolation; run them through `guest-exec` or another VM access path.
- Do not invent environment variables. Use `references/options.md` when adding
  docker-vm-runner options.
- Publish Docker ports with `-p` whenever the workflow expects host access to
  SSH, VNC, noVNC, Redfish, or forwarded guest services.
- Use `GUEST_NAME` with persistent `/data` volumes only when the VM identity
  must be stable across runs.
- For bridge, direct, host block devices, GPU, USB, TPM, and filesystem sharing,
  include the matching Docker device, mount, capability, or network option.
