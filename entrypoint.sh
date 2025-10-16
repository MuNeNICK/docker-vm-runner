#!/bin/bash
set -euo pipefail

args=("$@")
if [[ "${VM_NO_CONSOLE:-0}" =~ ^(1|true|TRUE|yes|YES)$ ]]; then
  args=(--no-console "${args[@]}")
fi

exec python3 /opt/docker-qemu/manager.py "${args[@]}"
