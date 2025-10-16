FROM ubuntu:22.04

# Run apt in non-interactive mode
ENV DEBIAN_FRONTEND=noninteractive

# Install QEMU, libvirt, and helper tools
RUN apt-get update && apt-get install -y \
    qemu-kvm \
    qemu-utils \
    genisoimage \
    wget \
    curl \
    openssl \
    python3 \
    python3-yaml \
    python3-pip \
    python3-libvirt \
    libvirt-daemon-system \
    libvirt-daemon-driver-qemu \
    libvirt-clients \
    tini \
    && rm -rf /var/lib/apt/lists/*

# Install sushy-tools from PyPI
RUN pip3 install --no-cache-dir sushy-tools

# Replace libvirt qemu.conf with container-friendly settings
RUN cat <<'EOF' >/etc/libvirt/qemu.conf
# docker-qemu libvirt configuration for rootful containers
user = "root"
group = "root"
dynamic_ownership = 0
remember_owner = 0
security_driver = "none"
cgroup_manager = "cgroupfs"
cgroup_controllers = []
clear_emulator_capabilities = 0
cgroup_device_acl = [
    "/dev/null", "/dev/full", "/dev/zero", "/dev/random", "/dev/urandom",
    "/dev/ptmx", "/dev/kvm", "/dev/kqemu", "/dev/hpet", "/dev/net/tun"
]
EOF

# Create directories for images and configuration
RUN mkdir -p /images /config
RUN mkdir -p /opt/docker-qemu

# Copy configuration, manager, and entrypoint
COPY distros.yaml /config/distros.yaml
COPY app/manager.py /opt/docker-qemu/manager.py
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /opt/docker-qemu/manager.py

# Set working directory
WORKDIR /

# Configure libvirt defaults
ENV LIBVIRT_DEFAULT_URI=qemu:///system

# Use entrypoint to handle image download and QEMU startup
ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
