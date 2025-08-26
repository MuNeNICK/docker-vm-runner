FROM ubuntu:22.04

# Run apt in non-interactive mode
ENV DEBIAN_FRONTEND=noninteractive

# Install QEMU and necessary tools
RUN apt-get update && apt-get install -y \
    qemu-kvm \
    qemu-utils \
    genisoimage \
    wget \
    curl \
    python3 \
    python3-yaml \
    && rm -rf /var/lib/apt/lists/*

# Create directories for images and configuration
RUN mkdir -p /images /config

# Copy configuration and entrypoint
COPY distros.yaml /config/distros.yaml
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Set working directory
WORKDIR /

# Use entrypoint to handle image download and QEMU startup
ENTRYPOINT ["/entrypoint.sh"]
