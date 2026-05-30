FROM golang:1.26-trixie AS go-builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/docker-vm-runner ./cmd/docker-vm-runner \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/guest-exec ./cmd/guest-exec

FROM debian:trixie-slim AS assets

ENV DEBIAN_FRONTEND=noninteractive
ARG NOVNC_VERSION=1.4.0
ARG VERSION_PASST=2026_01_20

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        gcc \
        libvirt-dev \
        pkg-config \
        python3 \
        python3-dev \
        python3-venv \
        tar \
        wget \
    && python3 -m venv --system-site-packages /opt/docker-vm-runner/.venv \
    && /opt/docker-vm-runner/.venv/bin/pip install --no-cache-dir sushy-tools virtualbmc \
    && mkdir -p /usr/share/docker-vm-runner/novnc \
    && wget -qO- "https://github.com/novnc/noVNC/archive/refs/tags/v${NOVNC_VERSION}.tar.gz" \
        | tar -xz --strip-components=1 -C /usr/share/docker-vm-runner/novnc \
    && rm -rf /usr/share/docker-vm-runner/novnc/docs \
              /usr/share/docker-vm-runner/novnc/tests \
              /usr/share/docker-vm-runner/novnc/snap \
              /usr/share/docker-vm-runner/novnc/utils \
              /usr/share/docker-vm-runner/novnc/.github \
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

FROM debian:trixie-slim

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    PATH="/opt/docker-vm-runner/.venv/bin:${PATH}" \
    LIBVIRT_DEFAULT_URI=qemu:///system

RUN echo 'path-exclude /usr/share/doc/*' > /etc/dpkg/dpkg.cfg.d/excludes \
    && echo 'path-exclude /usr/share/man/*' >> /etc/dpkg/dpkg.cfg.d/excludes \
    && echo 'path-exclude /usr/share/locale/*' >> /etc/dpkg/dpkg.cfg.d/excludes \
    && echo 'path-exclude /usr/share/info/*' >> /etc/dpkg/dpkg.cfg.d/excludes

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
        ipmitool \
        ipxe-qemu \
        libvirt-clients \
        libvirt-daemon \
        libvirt-daemon-config-network \
        libvirt-daemon-config-nwfilter \
        libvirt-daemon-driver-qemu \
        libvirt-daemon-system \
        ovmf \
        passt \
        python3 \
        python3-flask \
        python3-libvirt \
        python3-pbr \
        python3-webob \
        qemu-system-x86 \
        qemu-system-arm \
        qemu-system-ppc \
        qemu-system-s390x \
        qemu-system-riscv \
        qemu-utils \
        swtpm \
        swtpm-tools \
        tini \
        xz-utils \
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
    && rm -rf /usr/lib/cni /var/lib/apt/lists/*

COPY --from=go-builder /out/docker-vm-runner /usr/local/bin/docker-vm-runner
COPY --from=go-builder /out/guest-exec /usr/local/bin/guest-exec
COPY --from=assets /opt/docker-vm-runner/.venv /opt/docker-vm-runner/.venv
COPY --from=assets /usr/share/docker-vm-runner/novnc /usr/share/docker-vm-runner/novnc
COPY --from=assets /opt/aavmf.deb /opt/aavmf.deb
COPY --from=assets /opt/qemu-x86.deb /opt/qemu-x86.deb
COPY --from=assets /opt/qemu-arm.deb /opt/qemu-arm.deb
COPY --from=assets /opt/qemu-ppc.deb /opt/qemu-ppc.deb
COPY --from=assets /opt/qemu-s390x.deb /opt/qemu-s390x.deb
COPY --from=assets /opt/qemu-riscv.deb /opt/qemu-riscv.deb
COPY --from=assets /opt/passt.deb /tmp/passt.deb

RUN dpkg -i /tmp/passt.deb \
    && rm -f /tmp/passt.deb \
    && mkdir -p /etc/libvirt /var/log/libvirt /run/libvirt /var/lib/libvirt/images /images /config /opt/docker-vm-runner \
    && chmod +x /usr/local/bin/docker-vm-runner /usr/local/bin/guest-exec

RUN cat <<'EOF' >/etc/libvirt/qemu.conf
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

WORKDIR /

HEALTHCHECK --interval=10s --timeout=5s --start-period=120s --retries=3 \
    CMD virsh domstate "$(virsh list --name | head -1)" 2>/dev/null | grep -q running || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-vm-runner"]
