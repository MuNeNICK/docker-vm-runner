#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Log functions
log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $*"; }

# Parse YAML config using Python
parse_distro_config() {
    local distro=$1
    python3 -c "
import yaml
import sys

with open('/config/distros.yaml', 'r') as f:
    config = yaml.safe_load(f)
    
distro = '$distro'
if distro in config['distributions']:
    dist = config['distributions'][distro]
    print(f\"{dist['url']}|{dist['user']}|{dist.get('format', 'qcow2')}|{dist['name']}\")
else:
    sys.exit(1)
"
}

# Main execution
main() {
    # Get configuration from environment
    DISTRO=${DISTRO:-ubuntu-2404}
    VM_MEMORY=${VM_MEMORY:-4096}
    VM_CPUS=${VM_CPUS:-2}
    VM_DISK_SIZE=${VM_DISK_SIZE:-20G}
    VM_DISPLAY=${VM_DISPLAY:-none}
    VM_ARCH=${VM_ARCH:-x86_64}
    QEMU_CPU=${QEMU_CPU:-host}
    EXTRA_ARGS=${EXTRA_ARGS:-}
    VM_PASSWORD=${VM_PASSWORD:-ubuntu}
    
    log_info "Docker-QEMU Starting..."
    log_info "Distribution: $DISTRO"
    
    # Parse distribution configuration
    if ! DISTRO_CONFIG=$(parse_distro_config "$DISTRO"); then
        log_error "Unknown distribution: $DISTRO"
        log_error "Available distributions:"
        python3 -c "
import yaml
with open('/config/distros.yaml', 'r') as f:
    config = yaml.safe_load(f)
    for name, info in config['distributions'].items():
        print(f\"  - {name}: {info['name']}\")
"
        exit 1
    fi
    
    # Extract configuration
    IFS='|' read -r IMAGE_URL LOGIN_USER IMAGE_FORMAT DISTRO_NAME <<< "$DISTRO_CONFIG"
    
    log_info "Loading: $DISTRO_NAME"
    log_info "Default user: $LOGIN_USER"
    
    # Image file path
    IMAGE_FILE="/images/${DISTRO}.${IMAGE_FORMAT}"
    WORK_IMAGE="/images/${DISTRO}-work.${IMAGE_FORMAT}"
    
    # Check if image exists or download
    if [ -f "$IMAGE_FILE" ]; then
        FILE_SIZE=$(stat -c%s "$IMAGE_FILE" 2>/dev/null || echo "0")
        if [ "$FILE_SIZE" -gt 104857600 ]; then  # More than 100MB
            log_info "Using cached image: $IMAGE_FILE ($((FILE_SIZE/1024/1024))MB)"
        else
            log_warn "Cached image too small, re-downloading..."
            rm -f "$IMAGE_FILE"
        fi
    fi
    
    # Download if needed
    if [ ! -f "$IMAGE_FILE" ]; then
        log_info "Downloading image from: $IMAGE_URL"
        log_info "This may take a few minutes..."
        
        if wget --progress=bar:force:noscroll -O "$IMAGE_FILE" "$IMAGE_URL"; then
            FILE_SIZE=$(stat -c%s "$IMAGE_FILE")
            log_success "Downloaded: $((FILE_SIZE/1024/1024))MB"
        else
            log_error "Failed to download image"
            rm -f "$IMAGE_FILE"
            exit 1
        fi
    fi
    
    # Create working copy of image
    log_info "Creating working copy of image..."
    cp "$IMAGE_FILE" "$WORK_IMAGE"
    
    # Resize image if needed
    if [ -n "$VM_DISK_SIZE" ] && [ "$VM_DISK_SIZE" != "0" ]; then
        log_info "Resizing disk to $VM_DISK_SIZE..."
        qemu-img resize "$WORK_IMAGE" "$VM_DISK_SIZE"
    fi
    
    # Build QEMU command
    QEMU_CMD="qemu-system-${VM_ARCH}"
    
    # Basic VM configuration
    QEMU_ARGS=(
        "-name" "$DISTRO"
        "-machine" "pc,accel=kvm:tcg"
        "-cpu" "$QEMU_CPU"
        "-m" "$VM_MEMORY"
        "-smp" "$VM_CPUS"
    )
    
    SEED_ISO="/images/${DISTRO}-seed.iso"
    SEED_DIR=$(mktemp -d)
    PASS_HASH=$(python3 - <<'PY'
import crypt, os
pw = os.environ.get('VM_PASSWORD', 'ubuntu')
salt = "$6$" + os.urandom(8).hex()
print(crypt.crypt(pw, salt))
PY
)
    cat >"${SEED_DIR}/user-data" <<EOF
#cloud-config
users:
  - name: ${LOGIN_USER}
    lock_passwd: false
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    passwd: '${PASS_HASH}'
chpasswd:
  expire: False
EOF
    cat >"${SEED_DIR}/meta-data" <<EOF
instance-id: iid-${DISTRO}
local-hostname: ${DISTRO}
EOF
    genisoimage -output "$SEED_ISO" -volid cidata -joliet -rock "${SEED_DIR}/user-data" "${SEED_DIR}/meta-data" >/dev/null 2>&1 || true
    rm -rf "$SEED_DIR"

    # Storage
    QEMU_ARGS+=("-drive" "file=$WORK_IMAGE,if=virtio,format=$IMAGE_FORMAT")
    if [ -n "$SEED_ISO" ] && [ -f "$SEED_ISO" ]; then
        QEMU_ARGS+=("-cdrom" "$SEED_ISO")
    fi
    
    # Network
    QEMU_ARGS+=("-netdev" "user,id=net0,hostfwd=tcp::2222-:22")
    QEMU_ARGS+=("-device" "virtio-net,netdev=net0")
    
    # Display + console configuration (classic behavior)
    if [ "$VM_DISPLAY" = "none" ]; then
        QEMU_ARGS+=("-nographic")
        QEMU_ARGS+=("-serial" "mon:stdio")
    else
        QEMU_ARGS+=("-display" "$VM_DISPLAY")
    fi
    
    # Add any extra arguments
    if [ -n "$EXTRA_ARGS" ]; then
        QEMU_ARGS+=($EXTRA_ARGS)
    fi
    
    # Start QEMU
    log_info "Starting QEMU VM..."
    log_info "Configuration:"
    log_info "  Memory: ${VM_MEMORY}MB"
    log_info "  CPUs: $VM_CPUS"
    log_info "  Disk: $VM_DISK_SIZE"
    log_info "Press Ctrl+A X to exit QEMU"
    log_info ""
    # In classic mode, Ctrl+C is handled by QEMU console (raw)
    log_info "=========================================="
    
    # Replace shell with QEMU (PID 1), so Ctrl+C goes to QEMU
    exec $QEMU_CMD "${QEMU_ARGS[@]}"
}

# Run main function (QEMU becomes PID 1 via exec)
main "$@"
