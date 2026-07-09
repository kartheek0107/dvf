#!/bin/bash
# bundle_agent.sh — Deploy the DVF guest agent to the 9p share.
# Run on the HOST before booting VMs.
# The agent is pure Python stdlib — no pip install needed inside the guest.
#
# Usage: bash python-agent/agent/bundle_agent.sh [SHARE_DIR]
#   SHARE_DIR defaults to /home/kartheekbudime/qemu-rootfs/share

set -e

SHARE_DIR="${1:-/home/kartheekbudime/qemu-rootfs/share}"
AGENT_SRC="$(cd "$(dirname "$0")"/.. && pwd)/agent"
DEST="$SHARE_DIR/dvf_agent"

echo "[bundle] Deploying DVF guest agent to $DEST"
mkdir -p "$DEST"

# Copy agent module (stdlib only — no venv needed)
cp -r "$AGENT_SRC"/__main__.py "$DEST/agent.py"

# Write startup wrapper the VM runs from its init script
cat > "$DEST/start_agent.sh" << 'EOF'
#!/bin/sh
# DVF agent startup — called by guest init or rc.local
# Waits for the virtio port, then launches the agent.
AGENT_DIR=/mnt/share/dvf_agent
for i in $(seq 1 30); do
    test -e /dev/virtio-ports/dvf.agent.0 && break
    sleep 1
done
exec python3 "$AGENT_DIR/agent.py"
EOF
chmod +x "$DEST/start_agent.sh"

echo "[bundle] Agent deployed. Files in $DEST:"
ls -1 "$DEST/"
echo ""
echo "[bundle] Add this to your guest init script:"
echo "  /mnt/share/dvf_agent/start_agent.sh &"
echo ""
echo "[bundle] Or for init=/bin/bash, run manually after mount:"
echo "  mount -t 9p -o trans=virtio hostshare /mnt/share"
echo "  /mnt/share/dvf_agent/start_agent.sh &"
