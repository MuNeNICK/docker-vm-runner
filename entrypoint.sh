#!/bin/bash
set -euo pipefail

args=("$@")
NO_CONSOLE=${NO_CONSOLE:-0}
if [[ "${NO_CONSOLE}" =~ ^(1|true|TRUE|yes|YES)$ ]]; then
  args=(--no-console "${args[@]}")
fi

PYTHON_BIN=${PYTHON_BIN:-/opt/docker-vm-runner/.venv/bin/python3}
if [[ ! -x "${PYTHON_BIN}" ]]; then
  PYTHON_BIN=$(command -v python3)
fi

exec "${PYTHON_BIN}" /opt/docker-vm-runner/manager.py "${args[@]}"
