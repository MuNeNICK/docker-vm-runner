FROM debian:bookworm-slim

# Run apt in non-interactive mode and ensure deterministic locale
ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    PATH="/opt/docker-vm-runner/.venv/bin:${PATH}"

ARG NOVNC_VERSION=1.4.0

# Install virtualization stack, Python runtime, and helper tools
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bridge-utils \
        ca-certificates \
        genisoimage \
        iproute2 \
        libvirt-clients \
        libvirt-daemon \
        libvirt-daemon-config-network \
        libvirt-daemon-config-nwfilter \
        libvirt-daemon-driver-qemu \
        openssl \
        python3 \
        python3-libvirt \
        python3-venv \
        qemu-system-x86 \
        qemu-utils \
        tini \
        wget \
    && python3 -m venv /opt/docker-vm-runner/.venv \
    && /opt/docker-vm-runner/.venv/bin/pip install --upgrade --no-cache-dir pip \
    && /opt/docker-vm-runner/.venv/bin/pip install --no-cache-dir \
        bcrypt \
        PyYAML \
        sushy-tools \
        websockify \
    && mkdir -p /usr/share/novnc \
    && wget -qO- "https://github.com/novnc/noVNC/archive/refs/tags/v${NOVNC_VERSION}.tar.gz" \
        | tar -xz --strip-components=1 -C /usr/share/novnc \
    && ln -sf vnc.html /usr/share/novnc/index.html \
    && rm -rf /var/lib/apt/lists/* /root/.cache

# Replace libvirt qemu.conf with container-friendly settings
RUN mkdir -p /etc/libvirt /var/log/libvirt /run/libvirt /var/lib/libvirt/images \
    && cat <<'EOF' >/etc/libvirt/qemu.conf
# docker-vm-runner libvirt configuration for rootful containers
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
RUN mkdir -p /opt/docker-vm-runner

# Copy configuration, manager, and entrypoint
COPY distros.yaml /config/distros.yaml
COPY app/manager.py /opt/docker-vm-runner/manager.py
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /opt/docker-vm-runner/manager.py

# Set working directory
WORKDIR /

# Configure libvirt defaults
ENV LIBVIRT_DEFAULT_URI=qemu:///system

# Use entrypoint to handle image download and QEMU startup
ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
