#!/bin/sh
# DVF Agent Bootstrap — deployed to the 9p share by CI.
#
# This script is started inside the QEMU guest (from /etc/bash.bashrc
# autostart hook) to launch the Python agent.
#
# The VM ID is read automatically from /proc/cmdline (dvf_vm_id=<id>)
# so no side-channel is needed.
#
# Communication is via virtio-serial (/dev/virtio-ports/dvf.agent.0),
# not networking. No gRPC, no protobuf, no pip dependencies.
#
# Usage (from inside the guest):
#   /mnt/share/start_agent.sh

set -e

SHARE_DIR="${SHARE_DIR:-/mnt/share}"

# ---------------------------------------------------------------------------
# 1. Ensure the 9p share is mounted
# ---------------------------------------------------------------------------
if ! mountpoint -q "${SHARE_DIR}" 2>/dev/null; then
    mkdir -p "${SHARE_DIR}"
    mount -t 9p -o trans=virtio,version=9p2000.L hostshare "${SHARE_DIR}" 2>/dev/null \
        || mount -t 9p -o trans=virtio hostshare "${SHARE_DIR}"
fi

# ---------------------------------------------------------------------------
# 2. Locate Python interpreter
#    Priority: venv in 9p share → system python3
# ---------------------------------------------------------------------------
if [ -n "${AGENT_VENV}" ] && [ -x "${AGENT_VENV}/bin/python3" ]; then
    PYTHON="${AGENT_VENV}/bin/python3"
elif [ -x "${SHARE_DIR}/agent-venv/bin/python3" ]; then
    PYTHON="${SHARE_DIR}/agent-venv/bin/python3"
elif command -v python3 >/dev/null 2>&1; then
    PYTHON="python3"
else
    echo "[start_agent] ERROR: python3 not found. Deploy a venv to ${SHARE_DIR}/agent-venv/"
    exit 1
fi

echo "[start_agent] Using Python: ${PYTHON}"
echo "[start_agent] Share dir:    ${SHARE_DIR}"

# ---------------------------------------------------------------------------
# 3. Add agent source to PYTHONPATH
# ---------------------------------------------------------------------------
AGENT_SRC="${SHARE_DIR}/python-agent"

export PYTHONPATH="${AGENT_SRC}:${PYTHONPATH:-}"

# ---------------------------------------------------------------------------
# 4. Start the agent
#    The agent auto-detects vm_id from /proc/cmdline, mounts filesystems,
#    and communicates over /dev/virtio-ports/dvf.agent.0 (virtio-serial).
#    No --host flag needed — no networking required.
# ---------------------------------------------------------------------------
exec "${PYTHON}" -m agent "$@"
