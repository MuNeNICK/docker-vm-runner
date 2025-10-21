#!/bin/sh
set -eu

# Fan-out support ----------------------------------------------------------------
if [ -n "${TARGET_REDFISH_NODES:-}" ] && [ -z "${RUN_CLI_FANOUT:-}" ]; then
  nodes_raw="${TARGET_REDFISH_NODES}"
  nodes_raw=$(printf '%s\n' "$nodes_raw" | tr ',\n' '  ')
  rc=0
  pids=""
  idx=0
  for spec in $nodes_raw; do
    [ -z "$spec" ] && continue
    host_spec="$spec"
    ironic_name=""
    port_override=""
    system_override=""
    case "$host_spec" in
      *#*)
        system_override="${host_spec#*#}"
        host_spec="${host_spec%%#*}"
        ;;
    esac
    case "$host_spec" in
      *@*)
        ironic_name="${host_spec%%@*}"
        host_spec="${host_spec#*@}"
        ;;
    esac
    case "$host_spec" in
      *:*)
        port_override="${host_spec##*:}"
        host_spec="${host_spec%%:*}"
        ;;
    esac
    idx=$((idx + 1))
    (
      export RUN_CLI_FANOUT=1
      export TARGET_REDFISH_NODE="$host_spec"
      if [ -n "$ironic_name" ]; then
        export IRONIC_NODE_NAME="$ironic_name"
      fi
      if [ -n "$port_override" ]; then
        export TARGET_REDFISH_PORT="$port_override"
      fi
      if [ -n "$system_override" ]; then
        export TARGET_REDFISH_SYSTEM_ID="$system_override"
      else
        unset TARGET_REDFISH_SYSTEM_ID || true
      fi
      exec "$0" "$@"
    ) &
    pids="$pids $!"
  done
  for pid in $pids; do
    if ! wait "$pid"; then
      rc=1
    fi
  done
  exit "$rc"
fi

# Environment preparation --------------------------------------------------
NODE_NAME="${IRONIC_NODE_NAME:-${TARGET_REDFISH_SYSTEM_ID:-${TARGET_REDFISH_NODE:-redfish-client-1}}}"
REDFISH_ADDR="https://${TARGET_REDFISH_NODE:-redfish-client-1}:${TARGET_REDFISH_PORT:-8443}"
REDFISH_USER="${TARGET_REDFISH_USERNAME:-admin}"
REDFISH_PASS="${TARGET_REDFISH_PASSWORD:-password}"
ISO_NAME="${TARGET_ISO_FILENAME:-alpine-standard-3.20.2-x86_64.iso}"
ISO_HTTP_PORT="${ISO_HTTP_PORT:-8080}"
BOOT_ISO="http://redfish-controler:${ISO_HTTP_PORT}/images/custom/${ISO_NAME}"
KEEP_ALIVE="${KEEP_REDFISH_PROVISIONER_ALIVE:-${KEEP_REDFISH_CLIENT_ALIVE:-1}}"
TARGET_REDFISH_SYSTEM_ID="${TARGET_REDFISH_SYSTEM_ID:-auto}"

export OS_AUTH_TYPE="${OS_AUTH_TYPE:-none}"
export OS_ENDPOINT="${OS_ENDPOINT:-http://redfish-controler:6385}"
export OS_BAREMETAL_API_VERSION="${OS_BAREMETAL_API_VERSION:-latest}"

CLI="baremetal"

log() {
  printf '%s\n' "$*" >&2
}

# Helpers -------------------------------------------------------------------
wait_for_api() {
  for _ in $(seq 1 90); do
    if $CLI node list >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

discover_system_path() {
  python3 <<PY
import json
import os
import ssl
import sys
import time
from urllib import request, error

addr = "${REDFISH_ADDR}"
user = "${REDFISH_USER}"
password = "${REDFISH_PASS}"
hint = "${TARGET_REDFISH_SYSTEM_ID:-}".strip()

if hint and hint.lower() != "auto":
    if not hint.startswith('/'):
        hint = '/redfish/v1/Systems/' + hint.lstrip('/')
    print(hint)
    sys.exit(0)

ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE
password_mgr = request.HTTPPasswordMgrWithDefaultRealm()
password_mgr.add_password(None, addr, user, password)
opener = request.build_opener(
    request.HTTPSHandler(context=ctx),
    request.HTTPBasicAuthHandler(password_mgr),
)

for _ in range(60):
    with opener.open(f"{addr}/redfish/v1/Systems") as resp:
        systems = json.loads(resp.read().decode("utf-8"))
    members = systems.get("Members", [])
    if members:
        raw = members[0].get("@odata.id")
        if raw:
            if not raw.startswith("/"):
                raw = "/" + raw.lstrip("/")
            print(raw)
            sys.exit(0)
    time.sleep(1)

sys.exit(1)
PY
}

insert_virtual_media() {
  python3 - "$1" <<PY
import json
import ssl
import sys
import time
from urllib import request, error
from urllib.parse import urlsplit

addr = "${REDFISH_ADDR}"
user = "${REDFISH_USER}"
password = "${REDFISH_PASS}"
system_path = sys.argv[1]
boot_iso = "${BOOT_ISO}"

ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE
password_mgr = request.HTTPPasswordMgrWithDefaultRealm()
password_mgr.add_password(None, addr, user, password)
opener = request.build_opener(
    request.HTTPSHandler(context=ctx),
    request.HTTPBasicAuthHandler(password_mgr),
)

def resolve_redirect(current, location):
    if not location:
        return current
    parsed = urlsplit(location)
    if parsed.scheme:
        return parsed.path or current
    if location.startswith('/'):
        return location
    base = current.rsplit('/', 1)[0]
    return f"{base}/{location}"

def post(path, payload, allow_retry):
    current = path
    for attempt in range(15):
        req = request.Request(
            f"{addr}{current}",
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        try:
            with opener.open(req) as resp:
                resp.read()
            return
        except error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="ignore")
            if exc.code in (301, 302, 303, 307, 308):
                current = resolve_redirect(current, exc.headers.get('Location'))
                continue
            if allow_retry and exc.code in (404, 409, 503) and attempt < 14:
                time.sleep(2)
                continue
            raise RuntimeError(
                f"Virtual media request failed ({exc.code}): {body or exc.reason}"
            ) from exc
    raise RuntimeError("Virtual media request failed after retries")

try:
    post(f"{system_path}/VirtualMedia/Cd/Actions/VirtualMedia.EjectMedia", {}, True)
except RuntimeError:
    pass

post(
    f"{system_path}/VirtualMedia/Cd/Actions/VirtualMedia.InsertMedia",
    {"Image": boot_iso, "Inserted": True, "WriteProtected": True},
    True,
)
PY
}

set_boot_device_once() {
  python3 - "$1" <<PY
import json
import ssl
import sys
import time
from urllib import request, error
from urllib.parse import urlsplit

addr = "${REDFISH_ADDR}"
user = "${REDFISH_USER}"
password = "${REDFISH_PASS}"
system_path = sys.argv[1]

ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE
password_mgr = request.HTTPPasswordMgrWithDefaultRealm()
password_mgr.add_password(None, addr, user, password)
opener = request.build_opener(
    request.HTTPSHandler(context=ctx),
    request.HTTPBasicAuthHandler(password_mgr),
)

payload = {
    "Boot": {
        "BootSourceOverrideTarget": "Cd",
        "BootSourceOverrideEnabled": "Once",
    }
}

def resolve_redirect(current, location):
    if not location:
        return current
    parsed = urlsplit(location)
    if parsed.scheme:
        return parsed.path or current
    if location.startswith('/'):
        return location
    base = current.rsplit('/', 1)[0]
    return f"{base}/{location}"

current = system_path
for attempt in range(15):
    req = request.Request(
        f"{addr}{current}",
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="PATCH",
    )
    try:
        with opener.open(req) as resp:
            resp.read()
        break
    except error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="ignore")
        if exc.code in (301, 302, 303, 307, 308):
            current = resolve_redirect(current, exc.headers.get('Location'))
            continue
        if exc.code in (404, 409, 503) and attempt < 14:
            time.sleep(2)
            continue
        raise RuntimeError(
            f"Boot override request failed ({exc.code}): {body or exc.reason}"
        ) from exc
else:
    raise RuntimeError("Boot override request failed after retries")
PY
}

request_system_reboot() {
  python3 - "$1" <<PY
import json
import ssl
import sys
import time
from urllib import request, error
from urllib.parse import urlsplit

addr = "${REDFISH_ADDR}"
user = "${REDFISH_USER}"
password = "${REDFISH_PASS}"
system_path = sys.argv[1]

ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE
password_mgr = request.HTTPPasswordMgrWithDefaultRealm()
password_mgr.add_password(None, addr, user, password)
opener = request.build_opener(
    request.HTTPSHandler(context=ctx),
    request.HTTPBasicAuthHandler(password_mgr),
)

def resolve_redirect(current, location):
    if not location:
        return current
    parsed = urlsplit(location)
    if parsed.scheme:
        return parsed.path or current
    if location.startswith('/'):
        return location
    base = current.rsplit('/', 1)[0]
    return f"{base}/{location}"

def post_reset(path, reset_type):
    current = path
    for attempt in range(15):
        req = request.Request(
            f"{addr}{current}",
            data=json.dumps({"ResetType": reset_type}).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        try:
            with opener.open(req) as resp:
                resp.read()
            return True
        except error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="ignore")
            if exc.code in (301, 302, 303, 307, 308):
                current = resolve_redirect(current, exc.headers.get('Location'))
                continue
            if exc.code in (404, 409, 503) and attempt < 14:
                time.sleep(2)
                continue
            if exc.code == 400:
                return False
            raise RuntimeError(
                f"Reset request failed ({exc.code}): {body or exc.reason}"
            ) from exc
    raise RuntimeError("Reset request failed after retries")

reset_sequence = ("ForceRestart", "PowerCycle", "GracefulRestart", "On")
target = f"{system_path}/Actions/ComputerSystem.Reset"
for attempt in range(15):
    for reset_type in reset_sequence:
        try:
            if post_reset(target, reset_type):
                sys.exit(0)
        except RuntimeError as exc:
            if "404" in str(exc) and attempt < 14:
                time.sleep(2)
                break
            raise
    else:
        time.sleep(2)

raise RuntimeError("None of the requested reset actions were accepted")
PY
}

apply_driver_info() {
  path="$1"
  log "Applying driver info with redfish_system_id=$path"
  $CLI node set "$NODE_NAME" \
    --driver-info "redfish_address=${REDFISH_ADDR}" \
    --driver-info "redfish_system_id=${path}" \
    --driver-info "redfish_username=${REDFISH_USER}" \
    --driver-info "redfish_password=${REDFISH_PASS}" \
    --driver-info "redfish_verify_ca=false" \
    --driver-info "redfish_auth_type=basic" \
    --property "cpu_arch=x86_64" \
    --property "cpus=2" \
    --property "memory_mb=4096" \
    --property "local_gb=20" \
    --property "capabilities=boot_mode:uefi"
}

refresh_system_path() {
  if new_path=$(discover_system_path); then
    if [ "$new_path" != "$SYSTEM_PATH" ]; then
      log "Redfish system path changed from $SYSTEM_PATH to $new_path"
      SYSTEM_PATH="$new_path"
      apply_driver_info "$SYSTEM_PATH"
    fi
  else
    log "WARNING: Unable to rediscover Redfish system path"
  fi
}

log_state() {
  node="$1"
  $CLI node show "$node"
}

wait_provision_state() {
  node="$1"
  target="$2"
  timeout="${3:-180}"
  elapsed=0
  while [ "$elapsed" -lt "$timeout" ]; do
    state="$(get_provision_state "$node" 2>/dev/null || true)"
    log "Provision state for $node: ${state:-unknown} (expecting $target)"
    if [ "$state" = "$target" ]; then
      return 0
    fi
    if [ "$target" = "active" ] && [ "$state" = "deploy failed" ]; then
      log "Detected deploy failure for $node while waiting for $target"
      log_state "$node"
      return 2
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  log "Timeout waiting for $node to reach $target"
  log_state "$node"
  return 1
}

get_provision_state() {
  $CLI node show "$1" -f value -c provision_state
}

# Main ----------------------------------------------------------------------
log "Waiting for Ironic API via CLI"
if ! wait_for_api; then
  log "ERROR: baremetal CLI failed to reach Ironic API"
  exit 1
fi

SYSTEM_PATH="$(discover_system_path)"
if [ -z "$SYSTEM_PATH" ]; then
  log "ERROR: unable to determine Redfish system path"
  exit 1
fi
log "Discovered Redfish system path: $SYSTEM_PATH"

if $CLI node show "$NODE_NAME" >/dev/null 2>&1; then
  log "Updating existing node $NODE_NAME"
else
  log "Creating new node $NODE_NAME"
  $CLI node create \
    --name "$NODE_NAME" \
    --driver redfish \
    --boot-interface redfish-virtual-media \
    --deploy-interface ramdisk \
    --management-interface redfish \
    --power-interface redfish \
    --vendor-interface redfish >/dev/null
fi

apply_driver_info "$SYSTEM_PATH"

log "Setting instance info boot_iso $BOOT_ISO"
$CLI node set "$NODE_NAME" --instance-info "boot_iso=${BOOT_ISO}"

log "Current Redfish path after instance info: $SYSTEM_PATH"

refresh_system_path

log "Path after refresh: $SYSTEM_PATH"

log "Moving node to manageable"
$CLI node manage "$NODE_NAME" >/dev/null 2>&1 || log "Node already manageable; continuing"
if ! wait_provision_state "$NODE_NAME" "manageable" 180; then
  log "WARNING: failed to confirm manageable state; continuing anyway"
fi

refresh_system_path

log "Moving node to available"
$CLI node provide "$NODE_NAME" >/dev/null 2>&1 || log "Node already available; continuing"
if ! wait_provision_state "$NODE_NAME" "available" 180; then
  log "WARNING: failed to confirm available state; continuing anyway"
fi

refresh_system_path

log "Requesting deployment to active"
$CLI node deploy "$NODE_NAME"
rc=0
wait_provision_state "$NODE_NAME" "active" 300 || rc=$?
if [ "$rc" -ne 0 ]; then
  log "Provisioning via deploy failed (rc=$rc); attempting manual virtual media boot"
  refresh_system_path
  if insert_virtual_media "$SYSTEM_PATH"; then
    log "Virtual media inserted via Redfish"
  else
    log "WARNING: Virtual media insert failed; continuing"
  fi
  if set_boot_device_once "$SYSTEM_PATH"; then
    log "Boot override applied via Redfish"
  else
    log "WARNING: failed to apply boot override via Redfish"
  fi
  if request_system_reboot "$SYSTEM_PATH"; then
    log "Manual reboot requested via Redfish"
  else
    log "WARNING: failed to request reboot via Redfish"
  fi
else
  log "Deployment completed via CLI"
fi

log "CLI provisioning finished"

if [ "$KEEP_ALIVE" != "0" ]; then
  log "KEEP_REDFISH_CLIENT_ALIVE=$KEEP_ALIVE; keeping container alive for log inspection"
  while :; do
    sleep 60
  done
fi
