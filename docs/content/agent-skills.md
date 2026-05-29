# Agent Skills

`docker-vm-runner` includes an Agent Skill for AI coding agents that need a real VM instead of a normal container or the host environment.

Use the skill when an agent should run commands in a VM through `guest-exec`, boot installer media, open a GUI console, expose guest services, or test OS-level behavior without changing the host.

## What it provides

The skill gives agents task-oriented guidance for:

- starting disposable or persistent VMs
- running commands in the guest with `guest-exec`
- using noVNC or VNC for GUI installers
- connecting with SSH when login access is needed
- forwarding guest service ports
- enabling Redfish for BMC-like control
- booting from catalog images, ISO media, custom disks, blank disks, or iPXE
- configuring networking, extra disks, filesystem sharing, and block devices

The skill assumes Docker or a compatible Docker CLI/runtime is available. `/dev/kvm` is recommended for performance, but the examples can fall back to software emulation by removing `--device /dev/kvm`.

## Install with GitHub CLI

Use `gh skill` when GitHub CLI is available.

Preview the skill before installing:

```bash
gh skill preview MuNeNICK/docker-vm-runner skills/docker-vm-runner-agent
```

Install for Codex:

```bash
gh skill install MuNeNICK/docker-vm-runner skills/docker-vm-runner-agent \
  --agent codex \
  --scope user
```

Install for Claude Code:

```bash
gh skill install MuNeNICK/docker-vm-runner skills/docker-vm-runner-agent \
  --agent claude-code \
  --scope user
```

Install for OpenCode:

```bash
gh skill install MuNeNICK/docker-vm-runner skills/docker-vm-runner-agent \
  --agent opencode \
  --scope user
```

Use `--scope project` instead of `--scope user` when the skill should be installed only for the current project.

## Install without GitHub CLI

Clone the repository and copy the skill directory into the agent's skill path.

```bash
git clone https://github.com/MuNeNICK/docker-vm-runner.git
cd docker-vm-runner
```

Codex user install:

```bash
mkdir -p ~/.agents/skills
cp -a skills/docker-vm-runner-agent ~/.agents/skills/
```

Claude Code user install:

```bash
mkdir -p ~/.claude/skills
cp -a skills/docker-vm-runner-agent ~/.claude/skills/
```

OpenCode user install:

```bash
mkdir -p ~/.config/opencode/skills
cp -a skills/docker-vm-runner-agent ~/.config/opencode/skills/
```

For a project-local install, copy the same directory into the project skill path used by the agent, such as `.agents/skills/`, `.claude/skills/`, or `.opencode/skills/`.

## Basic agent workflow

The default agent workflow is a background persistent VM plus `guest-exec`:

```bash
docker run -dit --name command-vm \
  --device /dev/kvm \
  -e DISTRO=ubuntu-24.04-cloud-amd64 \
  -e GUEST_NAME=command-vm \
  -v command-vm-data:/data \
  ghcr.io/munenick/docker-vm-runner:latest

docker exec command-vm guest-exec --wait "uname -a"
```

Use [Access](access.md) for SSH, VNC, noVNC, Redfish, and `guest-exec` details. Use [Use Cases](use-cases.md) for VM startup examples beyond the agent workflow.
