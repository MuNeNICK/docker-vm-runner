#!/bin/sh
set -eu

ISO_DIR=/shared/html/images/custom
mkdir -p "$ISO_DIR"

ISO_NAME="${TARGET_ISO_FILENAME:-alpine-standard-3.20.2-x86_64.iso}"
ISO_HTTP_PORT="${ISO_HTTP_PORT:-8080}"
ISO_URL="${TARGET_ISO_URL:-https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/x86_64/alpine-standard-3.20.2-x86_64.iso}"

export HTTP_PORT="$ISO_HTTP_PORT"
export PROVISIONING_INTERFACE="${PROVISIONING_INTERFACE:-eth0}"
export IRONIC_AUTOMATED_CLEAN="false"
export IRONIC_FAST_TRACK="true"

TMP="$ISO_DIR/$ISO_NAME.part"
DEST="$ISO_DIR/$ISO_NAME"
if [ ! -f "$DEST" ]; then
  echo "Fetching ISO from $ISO_URL" >&2
  if command -v curl >/dev/null 2>&1; then
    curl -L --fail --progress-bar --output "$TMP" "$ISO_URL"
  elif command -v wget >/dev/null 2>&1; then
    wget --progress=dot:giga -O "$TMP" "$ISO_URL"
  else
    echo "ERROR: curl or wget required" >&2
    exit 1
  fi
  mv "$TMP" "$DEST"
else
  echo "Reusing cached ISO at $DEST" >&2
fi

cleanup() {
  [ -n "${HTTPD_PID:-}" ] && kill "$HTTPD_PID" 2>/dev/null || true
  [ -n "${IRONIC_PID:-}" ] && kill "$IRONIC_PID" 2>/dev/null || true
}
trap cleanup INT TERM

/bin/runhttpd &
HTTPD_PID=$!

/bin/runironic &
IRONIC_PID=$!

wait "$IRONIC_PID"
wait "$HTTPD_PID"
