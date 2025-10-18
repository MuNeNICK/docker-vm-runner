#!/bin/bash
set -euo pipefail

args=("$@")
NO_CONSOLE=${NO_CONSOLE:-0}
if [[ "${NO_CONSOLE}" =~ ^(1|true|TRUE|yes|YES)$ ]]; then
  args=(--no-console "${args[@]}")
fi

exec python3 /opt/docker-qemu/manager.py "${args[@]}"
