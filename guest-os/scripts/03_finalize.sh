#!/bin/bash
# 03_finalize.sh — Final cleanup and image preparation.
# Minimizes the image size before Packer converts it to ext4.
set -euo pipefail

echo "=== [03_finalize] Finalizing guest image ==="

# Remove package caches and temp files
rm -rf /var/cache/apk/*
rm -rf /tmp/*
rm -rf /var/tmp/*

# Clear pip cache
pip3 cache purge 2>/dev/null || true
rm -rf /root/.cache

# Remove documentation (not needed in a test VM)
rm -rf /usr/share/doc
rm -rf /usr/share/man

# Clear log files (they'll be regenerated on first boot)
find /var/log -type f -exec truncate -s 0 {} \;

# Zero out free space so the image compresses better
echo "  Zeroing free space (this may take a minute)..."
dd if=/dev/zero of=/zero.fill bs=1M 2>/dev/null || true
sync
rm -f /zero.fill
sync

# Print final disk usage
echo "  Disk usage:"
df -h /

echo "=== [03_finalize] Done ==="
