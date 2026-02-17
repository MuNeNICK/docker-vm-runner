FROM debian:trixie-slim AS builder

ENV DEBIAN_FRONTEND=noninteractive
ARG NOVNC_VERSION=1.4.0
ARG VERSION_PASST="2026_01_20"

# Builder: install only sushy-tools via pip (all other deps via apt in runtime),
#          download noVNC and QEMU .deb files
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        python3 \
        python3-venv \
        wget \
    && python3 -m venv --system-site-packages /opt/docker-vm-runner/.venv \
    && /opt/docker-vm-runner/.venv/bin/pip install --no-cache-dir sushy-tools \
    && find /opt/docker-vm-runner/.venv/lib/*/site-packages/ \
        -mindepth 1 -maxdepth 1 \
        ! -name 'sushy_tools' ! -name 'sushy_tools-*' \
        ! -name '_distutils_hack' ! -name 'distutils-precedence.pth' \
        -exec rm -rf {} + \
    && mkdir -p /usr/share/novnc \
    && wget -qO- "https://github.com/novnc/noVNC/archive/refs/tags/v${NOVNC_VERSION}.tar.gz" \
        | tar -xz --strip-components=1 -C /usr/share/novnc \
    && rm -rf /usr/share/novnc/docs /usr/share/novnc/tests /usr/share/novnc/snap \
              /usr/share/novnc/utils /usr/share/novnc/.github \
    && apt-get download \
        qemu-efi-aarch64 \
        qemu-system-x86 \
        qemu-system-arm \
        qemu-system-ppc \
        qemu-system-s390x \
        qemu-system-riscv \
    && mv qemu-efi-aarch64_*.deb /opt/aavmf.deb \
    && mv qemu-system-x86_*.deb /opt/qemu-x86.deb \
    && mv qemu-system-arm_*.deb /opt/qemu-arm.deb \
    && mv qemu-system-ppc_*.deb /opt/qemu-ppc.deb \
    && mv qemu-system-s390x_*.deb /opt/qemu-s390x.deb \
    && mv qemu-system-riscv_*.deb /opt/qemu-riscv.deb \
    && DPKG_ARCH="$(dpkg --print-architecture)" \
    && wget -q "https://github.com/qemus/passt/releases/download/v${VERSION_PASST}/passt_${VERSION_PASST}_${DPKG_ARCH}.deb" -O /opt/passt.deb \
    && rm -rf /var/lib/apt/lists/* /root/.cache

# ── Runtime stage ─────────────────────────────────────────────
FROM debian:trixie-slim

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    PATH="/opt/docker-vm-runner/.venv/bin:${PATH}"

# Exclude docs, man pages, locale, info to reduce installed size
RUN echo 'path-exclude /usr/share/doc/*' > /etc/dpkg/dpkg.cfg.d/excludes \
    && echo 'path-exclude /usr/share/man/*' >> /etc/dpkg/dpkg.cfg.d/excludes \
    && echo 'path-exclude /usr/share/locale/*' >> /etc/dpkg/dpkg.cfg.d/excludes \
    && echo 'path-exclude /usr/share/info/*' >> /etc/dpkg/dpkg.cfg.d/excludes

# Install runtime packages (shared libs resolved via apt), then remove QEMU binaries
# Actual QEMU binaries are extracted on demand from .deb files at runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bridge-utils \
        ca-certificates \
        dbus \
        dnsmasq-base \
        dmidecode \
        e2fsprogs \
        genisoimage \
        iproute2 \
        iptables \
        ipxe-qemu \
        libvirt-clients \
        libvirt-daemon \
        libvirt-daemon-config-network \
        libvirt-daemon-config-nwfilter \
        libvirt-daemon-driver-qemu \
        libvirt-daemon-system \
        openssl \
        ovmf \
        p7zip-full \
        passt \
        python3 \
        python3-bcrypt \
        python3-cryptography \
        python3-flask \
        python3-libvirt \
        python3-pbr \
        python3-tenacity \
        python3-webob \
        python3-websockify \
        python3-yaml \
        qemu-system-x86 \
        qemu-system-arm \
        qemu-system-ppc \
        qemu-system-s390x \
        qemu-system-riscv \
        qemu-utils \
        skopeo \
        swtpm \
        swtpm-tools \
        tini \
        xz-utils \
    && rm -rf /usr/lib/python3/dist-packages/setuptools \
              /usr/lib/python3/dist-packages/pkg_resources \
    && rm -f /usr/bin/qemu-system-x86_64 \
             /usr/bin/qemu-system-i386 \
             /usr/bin/qemu-system-x86_64-microvm \
             /usr/bin/qemu-system-aarch64 \
             /usr/bin/qemu-system-arm \
             /usr/bin/qemu-system-ppc \
             /usr/bin/qemu-system-ppc64 \
             /usr/bin/qemu-system-s390x \
             /usr/bin/qemu-system-riscv32 \
             /usr/bin/qemu-system-riscv64 \
    && rm -rf /usr/lib/cni \
    && rm -rf /var/lib/apt/lists/*

# Copy pre-built venv, noVNC, and AAVMF .deb from builder
COPY --from=builder /opt/docker-vm-runner/.venv /opt/docker-vm-runner/.venv
COPY --from=builder /usr/share/novnc /usr/share/novnc
COPY --from=builder /opt/aavmf.deb /opt/aavmf.deb
COPY --from=builder /opt/qemu-x86.deb /opt/qemu-x86.deb
COPY --from=builder /opt/qemu-arm.deb /opt/qemu-arm.deb
COPY --from=builder /opt/qemu-ppc.deb /opt/qemu-ppc.deb
COPY --from=builder /opt/qemu-s390x.deb /opt/qemu-s390x.deb
COPY --from=builder /opt/qemu-riscv.deb /opt/qemu-riscv.deb

# Replace passt with Docker-compatible build (isolation features removed,
# as they conflict with Docker's default seccomp policy; the container
# itself provides the isolation layer).  See: https://github.com/qemus/passt
COPY --from=builder /opt/passt.deb /tmp/passt.deb
RUN dpkg -i /tmp/passt.deb && rm -f /tmp/passt.deb

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
RUN mkdir -p /images /config /opt/docker-vm-runner

# Copy configuration, application package, and entrypoint
COPY distros.yaml /config/distros.yaml
COPY app/ /opt/docker-vm-runner/app/
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /opt/docker-vm-runner/app/guest-exec \
    && ln -s /opt/docker-vm-runner/app/guest-exec /usr/local/bin/guest-exec
ENV PYTHONPATH="/opt/docker-vm-runner"

# Set working directory
WORKDIR /

# Configure libvirt defaults
ENV LIBVIRT_DEFAULT_URI=qemu:///system

# Health check: VM is healthy when domain is running (guest-agent or domain state)
HEALTHCHECK --interval=10s --timeout=5s --start-period=120s --retries=3 \
    CMD virsh domstate $(virsh list --name | head -1) 2>/dev/null | grep -q running || exit 1

# Use entrypoint to handle image download and QEMU startup
ENTRYPOINT ["/usr/bin/tini", "--", "/entrypoint.sh"]
