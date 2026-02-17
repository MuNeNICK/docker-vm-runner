# Documentation

This directory collects task-specific guides and reference material for Docker-VM-Runner.

- [Quick Start](quick-start.md) — core `docker run` invocations, persistence, UEFI/Secure Boot, Windows guests, and console usage.
- [Compose Management](compose-management.md) — declarative multi-VM setups, macvtap networking, and Redfish tips.
- [GUI & Installation Media](gui-and-media.md) — enabling noVNC and booting from local ISO/blank disks.
- [iPXE Boot](ipxe.md) — enabling network boot via injected iPXE ROMs.
- [Networking Guide](networking.md) — choosing between NAT, bridge, and direct/macvtap modes.
- [Filesystem Sharing](filesystem-sharing.md) — sharing host directories via virtiofs or 9p.
- [Configuration Reference](reference.md) — all environment variables (compute, boot/firmware, performance, devices, networking, graphics, Redfish), supported distros, runtime detection, and guest command execution.
- [Security Guide](security.md) — default credentials, TLS, container security, and production hardening.
- [Troubleshooting & Operations](troubleshooting.md) — common issues, UEFI/TPM, filesystem warnings, Podman rootless, Redfish tips, and compose notes.
- [Redfish Guide](redfish.md) — enabling the Redfish API and performing power/boot actions.
