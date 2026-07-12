#!/bin/bash
# 01_agent.sh — Install the DVF Python agent into the guest image.
# Run inside the Packer QEMU VM as root after 00_base.sh.
set -euo pipefail

echo "=== [01_agent] Installing DVF Python agent ==="

SHARE_DIR="${DVF_SHARE_DIR:-/mnt/share}"
AGENT_DEST="/opt/dvf-agent"

# The agent source is embedded in the Packer build via file provisioner.
# The packer template copies python-agent/ to /tmp/python-agent/ inside
# the VM before running this script.
AGENT_SRC="/tmp/python-agent"

if [ ! -d "$AGENT_SRC" ]; then
    echo "ERROR: Agent source not found at $AGENT_SRC" >&2
    echo "       Make sure the Packer file provisioner ran first." >&2
    exit 1
fi

# Install agent to /opt/dvf-agent
mkdir -p "$AGENT_DEST"
cp -r "$AGENT_SRC/." "$AGENT_DEST/"

echo "  Agent installed to $AGENT_DEST"

# Install Python dependencies (if requirements.txt exists)
if [ -f "$AGENT_DEST/requirements.txt" ]; then
    pip3 install --no-cache-dir -r "$AGENT_DEST/requirements.txt"
    echo "  Python deps installed"
fi

# Create a stable entry-point symlink
ln -sf "$AGENT_DEST/agent/__main__.py" /usr/local/bin/dvf-agent
chmod +x /usr/local/bin/dvf-agent

# Pre-create the share mount point
mkdir -p "${SHARE_DIR}"

# Create a udev rule so virtio-serial ports get consistent symlinks
# as soon as QEMU creates them
cat > /etc/udev/rules.d/99-dvf-virtio.rules <<'UDEV'
# DVF virtio-serial agent channel
SUBSYSTEM=="virtio-ports", ATTR{name}=="dvf.agent.0", SYMLINK+="virtio-ports/dvf.agent.0"
UDEV

echo "  udev rule installed"

echo "=== [01_agent] Done ==="
