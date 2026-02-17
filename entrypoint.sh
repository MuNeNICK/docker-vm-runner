#!/bin/bash
set -euo pipefail

args=("$@")
NO_CONSOLE=${NO_CONSOLE:-0}
NO_CONSOLE_LOWER=$(echo "${NO_CONSOLE}" | tr '[:upper:]' '[:lower:]')
if [[ "${NO_CONSOLE_LOWER}" =~ ^(1|true|yes|on)$ ]]; then
  args=(--no-console "${args[@]}")
fi

PYTHON_BIN=${PYTHON_BIN:-/opt/docker-vm-runner/.venv/bin/python3}
if [[ ! -x "${PYTHON_BIN}" ]]; then
  PYTHON_BIN=$(command -v python3)
fi

exec "${PYTHON_BIN}" -m app "${args[@]}"
