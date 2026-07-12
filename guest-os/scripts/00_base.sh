#!/bin/bash
# 00_base.sh — Base Alpine Linux setup for the DVF guest OS
# Run inside the Packer QEMU VM as root.
set -euo pipefail

echo "=== [00_base] Setting up Alpine base ==="

# Enable community repo and update
cat > /etc/apk/repositories <<EOF
https://dl-cdn.alpinelinux.org/alpine/v3.20/main
https://dl-cdn.alpinelinux.org/alpine/v3.20/community
EOF

apk update
apk upgrade

# Kernel modules & hardware support
apk add --no-cache \
    linux-virt \
    linux-firmware-none \
    util-linux \
    kmod \
    pciutils \
    usbutils

# 9p filesystem support (for QEMU virtfs shares)
# Modules are included in linux-virt; just ensure the modules.dep is up to date
depmod -a 2>/dev/null || true

# Networking tools (minimal)
apk add --no-cache \
    iproute2 \
    ethtool

# Python 3 and essential tools for the agent
apk add --no-cache \
    python3 \
    py3-pip \
    bash \
    curl \
    sudo

# Create dvf user (non-root for future use)
addgroup -S dvf 2>/dev/null || true
adduser  -S -G dvf -s /bin/bash dvf 2>/dev/null || true

# /dev/virtio-ports symlink (udev rule creates this on a real running kernel;
# for the static image we prepare the directory)
mkdir -p /dev/virtio-ports

echo "=== [00_base] Done ==="
