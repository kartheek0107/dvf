#!/bin/bash
# 02_systemd.sh — Enable dvf-agent as a systemd service on Alpine.
#
# Alpine uses OpenRC by default. This script:
#  1. Installs systemd (Alpine supports it via apk)
#  2. Installs the dvf-agent.service unit
#  3. Enables it to start on boot
#
# Alternatively, if you prefer OpenRC, this script also installs an
# equivalent OpenRC init script.
set -euo pipefail

echo "=== [02_systemd] Configuring agent service ==="

SYSTEMD_UNIT="/tmp/dvf-agent.service"
SERVICE_DEST="/etc/systemd/system/dvf-agent.service"
OPENRC_INIT="/etc/init.d/dvf-agent"

# --- Option A: systemd (preferred for compatibility with the service unit) ---
if command -v systemctl >/dev/null 2>&1; then
    echo "  Using systemd"

    if [ ! -f "$SYSTEMD_UNIT" ]; then
        echo "ERROR: dvf-agent.service not found at $SYSTEMD_UNIT" >&2
        exit 1
    fi

    cp "$SYSTEMD_UNIT" "$SERVICE_DEST"
    systemctl enable dvf-agent.service
    echo "  dvf-agent.service enabled"

# --- Option B: OpenRC (Alpine default) ---
else
    echo "  systemd not available — using OpenRC"
    apk add --no-cache openrc

    cat > "$OPENRC_INIT" <<'OPENRC'
#!/sbin/openrc-run

name="dvf-agent"
description="DVF Guest Agent"
command="/usr/bin/python3"
command_args="/opt/dvf-agent/agent/__main__.py"
command_background=true
pidfile="/run/dvf-agent.pid"
output_log="/var/log/dvf-agent.log"
error_log="/var/log/dvf-agent.log"

depend() {
    after localmount
    need net
}

start_pre() {
    mkdir -p /mnt/share
    mount -t 9p -o trans=virtio,version=9p2000.L hostshare /mnt/share || true
}
OPENRC

    chmod +x "$OPENRC_INIT"
    rc-update add dvf-agent default
    echo "  dvf-agent added to OpenRC default runlevel"
fi

echo "=== [02_systemd] Done ==="
