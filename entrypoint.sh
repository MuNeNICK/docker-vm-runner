#!/bin/sh
set -eu

args="$*"
case "${NO_CONSOLE:-0}" in
  1|true|TRUE|yes|YES|on|ON)
    exec /usr/local/bin/docker-vm-runner --no-console "$@"
    ;;
  *)
    exec /usr/local/bin/docker-vm-runner "$@"
    ;;
esac
