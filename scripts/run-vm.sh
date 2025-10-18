#!/usr/bin/env bash
set -euo pipefail

# Simple one-shot runner for Docker
# - Defaults to ephemeral use
# - Optional --persist mounts ./images for caching/persistence
# - Optional --use-local-config mounts ./distros.yaml into /config/distros.yaml

IMAGE_NAME="ghcr.io/munenick/docker-qemu:latest"
CONTAINER_NAME=${CONTAINER_NAME:-docker-qemu-vm}
PERSIST=0
USE_LOCAL_CONFIG=0
CUSTOM_IMAGE=""
NO_CONSOLE=0
REDFISH_PORT=${REDFISH_PORT:-8443}

usage() {
  cat <<EOF
Usage: bash scripts/run-vm.sh [--cpus <n>] [--memory <MB|GB>] [--persist] [--use-local-config] [--no-console] [--] [extra docker args...]

Environment (forwarded if set):
  DISTRO, VM_MEMORY, VM_CPUS, VM_DISK_SIZE, VM_DISPLAY, VM_VNC_PORT, VM_NOVNC_PORT, VM_ARCH, VM_CPU_MODEL,
  VM_PASSWORD, VM_SSH_PUBKEY, EXTRA_ARGS, REDFISH_USERNAME, REDFISH_PASSWORD,
  REDFISH_PORT

 Flags:
  --cpus, -c <n>      Number of vCPUs (e.g., 4)
  --memory, -m <sz>   Memory size (e.g., 2048, 2g, 512m)
  --persist           Mount ./images to /images for caching/persistence
  --use-local-config  Mount ./distros.yaml into /config/distros.yaml (read-only)
  --no-console        Do not attach the local terminal to the VM console
  --help              Show this help

Examples:
  # Pull from GHCR and run Ubuntu (one-shot, ephemeral)
  bash scripts/run-vm.sh

  # Run Debian with 2GB RAM, 4 vCPUs
  DISTRO=debian-12 bash scripts/run-vm.sh --memory 2g --cpus 4

  # Persist images across runs and use local distros.yaml
  bash scripts/run-vm.sh --persist --use-local-config
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cpus|-c)
      VM_CPUS=${2-}
      if [[ -z "${VM_CPUS:-}" ]]; then echo "[error] --cpus requires a number" >&2; exit 1; fi
      shift 2 ;;
    --memory|-m)
      MEM_IN=${2-}
      if [[ -z "${MEM_IN:-}" ]]; then echo "[error] --memory requires a value (e.g., 2048, 2g)" >&2; exit 1; fi
      # Normalize to MB
      case "${MEM_IN,,}" in
        *g|*gb)
          NUM=${MEM_IN//[!0-9.]/}
          # Support integer or decimal GB -> MB
          if [[ "$NUM" == *.* ]]; then
            VM_MEMORY=$(awk -v n="$NUM" 'BEGIN{printf "%d", n*1024}')
          else
            VM_MEMORY=$(( NUM * 1024 ))
          fi
          ;;
        *m|*mb)
          NUM=${MEM_IN//[!0-9]/}
          VM_MEMORY=$NUM
          ;;
        *)
          # Assume MB when unit omitted
          VM_MEMORY=$MEM_IN
          ;;
      esac
      shift 2 ;;
    --persist)
      PERSIST=1; shift ;;
    --use-local-config)
      USE_LOCAL_CONFIG=1; shift ;;
    --no-console)
      NO_CONSOLE=1; shift ;;
    --help|-h)
      usage; exit 0 ;;
    --)
      shift; break ;;
    *)
      break ;;
  esac
done

# Docker references must be lowercase; normalize defensively
IMAGE_NAME_LC=$(printf '%s' "$IMAGE_NAME" | tr '[:upper:]' '[:lower:]')
IMAGE_NAME="$IMAGE_NAME_LC"

# Preflight checks
require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "[error] Required command not found: $1" >&2
    exit 1
  fi
}

check_docker_daemon() {
  if ! docker info >/dev/null 2>&1; then
    echo "[error] Cannot talk to Docker daemon. Is Docker running and do you have permissions?" >&2
    echo "        Try starting Docker, or add your user to the 'docker' group and re-login:" >&2
    echo "        sudo usermod -aG docker $USER" >&2
    exit 1
  fi
}

check_kvm_access() {
  if [[ -e /dev/kvm ]]; then
    if [[ ! -r /dev/kvm ]]; then
      echo "[warn] /dev/kvm exists but is not readable by $(whoami)." >&2
      echo "       KVM acceleration may not work. Consider adding your user to the 'kvm' group:" >&2
      echo "       sudo usermod -aG kvm $USER && newgrp kvm" >&2
    fi
  else
    echo "[warn] /dev/kvm not found. VM will run without KVM (slower)." >&2
  fi
}

require_cmd docker
check_docker_daemon
check_kvm_access

echo "[info] Using image: $IMAGE_NAME"

# Ensure image available: pull from registry only
ensure_image() {
  if docker image inspect "$IMAGE_NAME" >/dev/null 2>&1; then
    return
  fi
  echo "[info] Image not found locally. Attempting to pull: $IMAGE_NAME"
  if docker pull "$IMAGE_NAME"; then
    return
  fi
  echo "[error] Failed to pull $IMAGE_NAME"
  echo "        Check network connectivity or authenticate to GHCR:"
  echo "        echo \$GITHUB_TOKEN | docker login ghcr.io -u munenick --password-stdin" 
  exit 1
}

ensure_image

DOCKER_ARGS=(
  --rm
  --name "$CONTAINER_NAME"
)

# TTY handling
if [[ -t 0 && -t 1 ]]; then
  DOCKER_ARGS+=( -it )
  RUN_INTERACTIVE_DIRECT=1
else
  RUN_INTERACTIVE_DIRECT=0
fi

# Pass KVM device if available
if [[ -e /dev/kvm ]]; then
  DOCKER_ARGS+=(--device /dev/kvm:/dev/kvm)
else
  echo "[warn] /dev/kvm not found. VM will run without KVM (slower)."
fi

# Persist images if requested
if [[ $PERSIST -eq 1 ]]; then
  mkdir -p images
  mkdir -p images/base
  mkdir -p images/vms
  mkdir -p images/state
  DOCKER_ARGS+=( -v "$(pwd)/images:/images" )
  DOCKER_ARGS+=( -e VM_PERSIST=1 )
  DOCKER_ARGS+=( -v "$(pwd)/images/state:/var/lib/docker-qemu" )
fi

# Mount local config if requested
if [[ $USE_LOCAL_CONFIG -eq 1 ]]; then
  if [[ -f distros.yaml ]]; then
    DOCKER_ARGS+=( -v "$(pwd)/distros.yaml:/config/distros.yaml:ro" )
  else
    echo "[warn] distros.yaml not found in repo root; skipping mount."
  fi
fi

# Forward known env vars only if set
forward_env() {
  local var="$1"
  if [[ -n "${!var-}" ]]; then
    DOCKER_ARGS+=( -e "$var=${!var}" )
  fi
}

for v in DISTRO VM_MEMORY VM_CPUS VM_DISK_SIZE VM_DISPLAY VM_VNC_PORT VM_NOVNC_PORT VM_ARCH VM_CPU_MODEL VM_PASSWORD VM_SSH_PUBKEY EXTRA_ARGS; do
  forward_env "$v"
done
for v in REDFISH_USERNAME REDFISH_PASSWORD REDFISH_PORT; do
  forward_env "$v"
done
if [[ $NO_CONSOLE -eq 1 ]]; then
  DOCKER_ARGS+=( -e VM_NO_CONSOLE=1 )
fi

# SSH port mapping (container and host use the same port)
SSH_PORT=${VM_SSH_PORT:-2222}
DOCKER_ARGS+=( -p ${SSH_PORT}:${SSH_PORT} )

# Redfish port mapping
DOCKER_ARGS+=( -p ${REDFISH_PORT}:${REDFISH_PORT} )

# Graphics/noVNC port mapping when requested
DISPLAY_MODE=${VM_DISPLAY:-none}
DISPLAY_MODE=${DISPLAY_MODE,,}
if [[ "$DISPLAY_MODE" == "vnc" || "$DISPLAY_MODE" == "novnc" ]]; then
  VNC_PORT=${VM_VNC_PORT:-5900}
  DOCKER_ARGS+=( -p ${VNC_PORT}:${VNC_PORT} )
fi
if [[ "$DISPLAY_MODE" == "novnc" ]]; then
  NOVNC_PORT=${VM_NOVNC_PORT:-6080}
  DOCKER_ARGS+=( -p ${NOVNC_PORT}:${NOVNC_PORT} )
fi

# Allow extra docker args after --
if [[ $# -gt 0 ]]; then
  DOCKER_ARGS+=( "$@" )
fi

set -x
if [[ $RUN_INTERACTIVE_DIRECT -eq 1 ]]; then
  exec docker run "${DOCKER_ARGS[@]}" "$IMAGE_NAME"
else
  if [[ -r /dev/tty ]]; then
    echo "[info] No TTY on stdin; reattaching stdio to /dev/tty for interactive session." >&2
    DOCKER_ARGS+=( -it )
    # Reattach stdin, stdout, stderr to the controlling terminal
    exec </dev/tty >/dev/tty 2>&1
    exec docker run "${DOCKER_ARGS[@]}" "$IMAGE_NAME"
  else
    echo "[info] No TTY available; starting without -t (non-interactive)." >&2
    exec docker run "${DOCKER_ARGS[@]}" -i "$IMAGE_NAME"
  fi
fi
