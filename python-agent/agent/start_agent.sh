#!/bin/sh
# DVF Agent Bootstrap — deployed to the 9p share by CI.
#
# This script is started inside the QEMU guest (e.g. from an init script or
# via QEMU's -append initrd= parameter) to launch the Python agent.
#
# The VM ID is read automatically from /proc/cmdline (dvf_vm_id=<id>)
# so no side-channel is needed.
#
# The orchestrator is reachable at 10.0.2.2 via QEMU user-mode NAT.
# Port 50051 is the default gRPC port configured in global_config.json.
#
# Usage (from inside the guest):
#   /mnt/share/start_agent.sh
#
# Or, if Python is in the 9p share venv:
#   AGENT_VENV=/mnt/share/agent-venv /mnt/share/start_agent.sh

set -e

SHARE_DIR="${SHARE_DIR:-/mnt/share}"
ORCHESTRATOR_HOST="${ORCHESTRATOR_HOST:-10.0.2.2:50051}"

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
echo "[start_agent] Orchestrator: ${ORCHESTRATOR_HOST}"
echo "[start_agent] Share dir:    ${SHARE_DIR}"

# ---------------------------------------------------------------------------
# 3. Add agent source (and proto stubs) to PYTHONPATH
# ---------------------------------------------------------------------------
AGENT_SRC="${SHARE_DIR}/python-agent"
PROTO_GEN="${AGENT_SRC}/agent/proto_gen"

export PYTHONPATH="${AGENT_SRC}:${PROTO_GEN}:${PYTHONPATH:-}"

# ---------------------------------------------------------------------------
# 4. Start the agent
#    --skip-mount: mount_guest_filesystems() inside the agent is a no-op
#                  because we already mounted above; pass --skip-mount to
#                  avoid a duplicate mount warning.
# ---------------------------------------------------------------------------
exec "${PYTHON}" -m agent \
    --host "${ORCHESTRATOR_HOST}" \
    --mount "${SHARE_DIR}" \
    --skip-mount \
    "$@"
