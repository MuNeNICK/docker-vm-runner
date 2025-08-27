#!/usr/bin/env bash
set -euo pipefail

# Simple one-shot runner for docker (no compose)
# - Defaults to ephemeral use (no volume mounts)
# - Optional --persist mounts ./images for caching/persistence
# - Optional --use-local-config mounts ./distros.yaml into /config/distros.yaml

IMAGE_NAME=${IMAGE_NAME:-ghcr.io/munenick/docker-qemu:latest}
# Docker references must be lowercase; normalize defensively
IMAGE_NAME_LC=$(printf '%s' "$IMAGE_NAME" | tr '[:upper:]' '[:lower:]')
if [[ "$IMAGE_NAME" != "$IMAGE_NAME_LC" ]]; then
  echo "[info] Normalizing IMAGE_NAME to lowercase: $IMAGE_NAME_LC"
  IMAGE_NAME="$IMAGE_NAME_LC"
fi
CONTAINER_NAME=${CONTAINER_NAME:-docker-qemu-vm}
PERSIST=0
USE_LOCAL_CONFIG=0
BUILD_LOCAL=0

usage() {
  cat <<EOF
Usage: bash scripts/run-vm.sh [--persist] [--use-local-config] [--build-local] [--] [extra docker args...]

Environment (forwarded if set):
  DISTRO, VM_MEMORY, VM_CPUS, VM_DISK_SIZE, VM_DISPLAY, VM_ARCH, QEMU_CPU,
  VM_PASSWORD, VM_SSH_PUBKEY, EXTRA_ARGS

Flags:
  --persist           Mount ./images to /images for caching/persistence
  --use-local-config  Mount ./distros.yaml into /config/distros.yaml (read-only)
  --build-local       Build the image locally if pull fails or image missing
  --help              Show this help

Examples:
  # Pull from GHCR and run Ubuntu (one-shot, ephemeral)
  bash scripts/run-vm.sh

  # Run Debian with 2GB RAM, 4 vCPUs
  DISTRO=debian-12 VM_MEMORY=2048 VM_CPUS=4 bash scripts/run-vm.sh

  # Persist images across runs and use local distros.yaml
  bash scripts/run-vm.sh --persist --use-local-config
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --persist)
      PERSIST=1; shift ;;
    --use-local-config)
      USE_LOCAL_CONFIG=1; shift ;;
    --build-local)
      BUILD_LOCAL=1; shift ;;
    --help|-h)
      usage; exit 0 ;;
    --)
      shift; break ;;
    *)
      break ;;
  esac
done

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

# Ensure image available: prefer pulling from registry; optional local build
ensure_image() {
  if docker image inspect "$IMAGE_NAME" >/dev/null 2>&1; then
    return
  fi
  echo "[info] Image not found locally. Attempting to pull: $IMAGE_NAME"
  if docker pull "$IMAGE_NAME"; then
    return
  fi
  echo "[warn] Failed to pull $IMAGE_NAME"
  if [[ $BUILD_LOCAL -eq 1 ]]; then
    echo "[info] Building locally as fallback: $IMAGE_NAME"
    docker build -t "$IMAGE_NAME" .
  else
    echo "[error] Image unavailable and local build not requested."
    echo "        Pass --build-local to build from the local Dockerfile, or set IMAGE_NAME to an available image."
    exit 1
  fi
}

ensure_image

DOCKER_ARGS=(
  --rm
  -it
  --name "$CONTAINER_NAME"
  --privileged
  -p 2222:2222
)

# Pass KVM device if available
if [[ -e /dev/kvm ]]; then
  DOCKER_ARGS+=(--device /dev/kvm:/dev/kvm)
else
  echo "[warn] /dev/kvm not found. VM will run without KVM (slower)."
fi

# Persist images if requested
if [[ $PERSIST -eq 1 ]]; then
  mkdir -p images
  DOCKER_ARGS+=( -v "$(pwd)/images:/images" )
fi

# Mount local config if requested
if [[ $USE_LOCAL_CONFIG -eq 1 ]]; then
  if [[ -f distros.yaml ]]; then
    DOCKER_ARGS+=( -v "$(pwd)/distros.yaml:/config/distros.yaml:ro" )
  else
    echo "[warn] distros.yaml not found in repo root; skipping mount."
  fi
fi

# Forward known env vars only if set (entrypoint has defaults)
forward_env() {
  local var="$1"
  if [[ -n "${!var-}" ]]; then
    DOCKER_ARGS+=( -e "$var=${!var}" )
  fi
}

for v in DISTRO VM_MEMORY VM_CPUS VM_DISK_SIZE VM_DISPLAY VM_ARCH QEMU_CPU VM_PASSWORD VM_SSH_PUBKEY EXTRA_ARGS; do
  forward_env "$v"
done

# Allow extra docker args after --
if [[ $# -gt 0 ]]; then
  DOCKER_ARGS+=( "$@" )
fi

set -x
exec docker run "${DOCKER_ARGS[@]}" "$IMAGE_NAME"
