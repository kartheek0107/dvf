#!/usr/bin/env bash
# =============================================================================
# DVF Bootstrap — One-script setup for a fresh machine
#
# Usage:
#   bash scripts/bootstrap.sh [OPTIONS]
#
# Options:
#   --skip-kernel        Skip Linux kernel clone + build
#   --skip-qemu          Skip custom QEMU build
#   --skip-rootfs        Skip guest rootfs build (requires Packer)
#   --skip-vishwa        Skip Vishwa build (proprietary CDAC source)
#   --skip-packages      Skip system package installation
#   --help               Show this help
#
# This script is idempotent — safe to re-run. It skips steps that are
# already completed (e.g. kernel already built, QEMU already compiled).
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DVF_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ── Source .env if it exists ──────────────────────────────────────────────────
if [ -f "${DVF_ROOT}/.env" ]; then
    # shellcheck disable=SC1091
    source "${DVF_ROOT}/.env"
fi

# ── Defaults ──────────────────────────────────────────────────────────────────
KERNEL_BUILD_DIR="${KERNEL_BUILD_DIR:-$HOME/VirtualMachines/linux}"
KERNEL_VERSION="${KERNEL_VERSION:-v6.6}"
QEMU_VERSION="${QEMU_VERSION:-v8.2.0}"
ROOTFS_PATH="${ROOTFS_PATH:-$HOME/qemu-rootfs/rootfs.ext4}"
SHARE_DIR="${SHARE_DIR:-$HOME/qemu-rootfs/share}"
VISHWA_CODE_DIR="${VISHWA_CODE_DIR:-$HOME/cdac/FW_SW_Milestone_2/code}"

SKIP_KERNEL=false
SKIP_QEMU=false
SKIP_ROOTFS=false
SKIP_VISHWA=false
SKIP_PACKAGES=false

# ── Argument parsing ─────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --skip-kernel)   SKIP_KERNEL=true;   shift ;;
        --skip-qemu)     SKIP_QEMU=true;     shift ;;
        --skip-rootfs)   SKIP_ROOTFS=true;   shift ;;
        --skip-vishwa)   SKIP_VISHWA=true;   shift ;;
        --skip-packages) SKIP_PACKAGES=true; shift ;;
        --help)
            sed -n '/^# ====/,/^# ====/p' "$0" | head -20
            exit 0 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

GREEN="\033[0;32m"
YELLOW="\033[1;33m"
CYAN="\033[0;36m"
RED="\033[0;31m"
RESET="\033[0m"
step()  { echo -e "\n${CYAN}[$(date +%H:%M:%S)] ▶ $*${RESET}"; }
ok()    { echo -e "${GREEN}  ✓ $*${RESET}"; }
warn()  { echo -e "${YELLOW}  ⚠ $*${RESET}"; }
fail()  { echo -e "${RED}  ✗ $*${RESET}"; }

echo -e "${CYAN}================================================================${RESET}"
echo -e "${CYAN}  DVF Driver Validation Suite — Bootstrap                       ${RESET}"
echo -e "${CYAN}  DVF_ROOT         : ${DVF_ROOT}${RESET}"
echo -e "${CYAN}  KERNEL_BUILD_DIR : ${KERNEL_BUILD_DIR}${RESET}"
echo -e "${CYAN}  SHARE_DIR        : ${SHARE_DIR}${RESET}"
echo -e "${CYAN}================================================================${RESET}"

# =============================================================================
# Step 1 — System packages
# =============================================================================
step "1/8  System packages"
if $SKIP_PACKAGES; then
    warn "Skipping (--skip-packages)"
else
    # Detect package manager
    if command -v dnf &>/dev/null; then
        PKG_MGR="dnf"
        echo "  Detected: Fedora / RHEL / CentOS (dnf)"
        sudo dnf install -y \
            gcc gcc-c++ make git python3 python3-pip golang \
            ninja-build meson pkg-config glib2-devel pixman-devel zlib-devel \
            qemu-img rsync curl
    elif command -v apt-get &>/dev/null; then
        PKG_MGR="apt"
        echo "  Detected: Ubuntu / Debian (apt)"
        sudo apt-get update
        sudo apt-get install -y \
            gcc g++ make git python3 python3-pip golang-go \
            ninja-build meson pkg-config libglib2.0-dev libpixman-1-dev \
            zlib1g-dev qemu-utils rsync curl
    else
        warn "Unknown package manager — install manually: gcc, make, go, python3, ninja, meson, qemu-img"
    fi

    # Ensure /dev/kvm is accessible
    if [ -c /dev/kvm ]; then
        ok "KVM available"
        if ! groups | grep -q kvm; then
            warn "Current user is not in 'kvm' group. Run: sudo usermod -aG kvm \$USER && newgrp kvm"
        fi
    else
        warn "/dev/kvm not found — VM tests require KVM. Enable virtualisation in BIOS."
    fi
fi

# =============================================================================
# Step 2 — Docker / Podman (PostgreSQL + Redis)
# =============================================================================
step "2/8  Docker services (PostgreSQL + Redis)"
if command -v docker &>/dev/null || command -v podman &>/dev/null; then
    COMPOSE_CMD=""
    if command -v docker-compose &>/dev/null; then
        COMPOSE_CMD="docker-compose"
    elif command -v docker &>/dev/null && docker compose version &>/dev/null 2>&1; then
        COMPOSE_CMD="docker compose"
    elif command -v podman-compose &>/dev/null; then
        COMPOSE_CMD="podman-compose"
    fi

    if [ -n "$COMPOSE_CMD" ]; then
        $COMPOSE_CMD -f "${DVF_ROOT}/docker-compose.yml" up -d
        ok "Postgres + Redis started via $COMPOSE_CMD"
    else
        warn "docker-compose / podman-compose not found — start Postgres+Redis manually"
    fi
else
    warn "Docker/Podman not installed. Install docker and docker-compose for Postgres+Redis."
fi

# =============================================================================
# Step 3 — Linux Kernel Source
# =============================================================================
step "3/8  Linux kernel source (for driver .ko compilation)"
if $SKIP_KERNEL; then
    warn "Skipping (--skip-kernel)"
elif [ -f "${KERNEL_BUILD_DIR}/arch/x86/boot/bzImage" ]; then
    ok "Kernel already built: ${KERNEL_BUILD_DIR}/arch/x86/boot/bzImage"
else
    echo "  Cloning Linux ${KERNEL_VERSION} (depth=1, ~250 MB) ..."
    mkdir -p "$(dirname "$KERNEL_BUILD_DIR")"
    if [ ! -d "${KERNEL_BUILD_DIR}/.git" ]; then
        git clone --depth=1 --branch "$KERNEL_VERSION" \
            https://github.com/torvalds/linux "$KERNEL_BUILD_DIR"
    fi
    echo "  Building kernel (defconfig, this takes 10-20 min) ..."
    make -C "$KERNEL_BUILD_DIR" defconfig
    make -C "$KERNEL_BUILD_DIR" -j"$(nproc)"
    ok "Kernel built: ${KERNEL_BUILD_DIR}/arch/x86/boot/bzImage"
fi

# =============================================================================
# Step 4 — Custom QEMU with gp_gpu device model
# =============================================================================
step "4/8  Custom QEMU binary (with gp_gpu device)"
if $SKIP_QEMU; then
    warn "Skipping (--skip-qemu)"
elif [ -f "${DVF_ROOT}/builds/qemu/qemu-system-x86_64" ]; then
    ok "QEMU already built: ${DVF_ROOT}/builds/qemu/qemu-system-x86_64"
else
    echo "  Building QEMU ${QEMU_VERSION} with DVF device models ..."
    bash "${DVF_ROOT}/qemu-accelerator-models/scripts/build_qemu_with_models.sh" \
        "${DVF_ROOT}/qemu-accelerator-models" \
        "${DVF_ROOT}/builds/qemu-build" \
        "$QEMU_VERSION" \
        ""
    mkdir -p "${DVF_ROOT}/builds/qemu"
    cp "${DVF_ROOT}/builds/qemu-build/qemu-system-x86_64" \
       "${DVF_ROOT}/builds/qemu/qemu-system-x86_64"
    chmod +x "${DVF_ROOT}/builds/qemu/qemu-system-x86_64"
    ok "QEMU binary installed"
fi

# Verify gp_gpu device is present
if [ -f "${DVF_ROOT}/builds/qemu/qemu-system-x86_64" ]; then
    if "${DVF_ROOT}/builds/qemu/qemu-system-x86_64" -device gp_gpu,help 2>&1 | head -1 | grep -q "gp_gpu"; then
        ok "gp_gpu device model verified"
    else
        warn "QEMU binary exists but gp_gpu device not detected"
    fi
fi

# =============================================================================
# Step 5 — Guest rootfs
# =============================================================================
step "5/8  Guest rootfs (QEMU guest VM image)"
if $SKIP_ROOTFS; then
    warn "Skipping (--skip-rootfs)"
elif [ -f "$ROOTFS_PATH" ]; then
    ok "Rootfs already exists: $ROOTFS_PATH"
else
    if command -v packer &>/dev/null; then
        echo "  Building guest image with Packer (~10-15 min) ..."
        mkdir -p "$(dirname "$ROOTFS_PATH")"
        make -C "${DVF_ROOT}/guest-os" build
        make -C "${DVF_ROOT}/guest-os" install INSTALL_TARGET="$(dirname "$ROOTFS_PATH")"
        ok "Rootfs built: $ROOTFS_PATH"
    else
        fail "Packer not installed — cannot build rootfs."
        echo "     Install: https://developer.hashicorp.com/packer/install"
        echo "     Or copy rootfs.ext4 manually to: $ROOTFS_PATH"
    fi
fi

# =============================================================================
# Step 6 — Build Go orchestrator
# =============================================================================
step "6/8  Go orchestrator"
if [ -f "${DVF_ROOT}/go-orchestrator/orchestrator" ]; then
    ok "Orchestrator binary already exists"
else
    echo "  Building Go orchestrator ..."
    (cd "${DVF_ROOT}/go-orchestrator" && go build -o orchestrator ./cmd/orchestrator/)
    ok "Built: go-orchestrator/orchestrator"
fi

# =============================================================================
# Step 7 — Build C test binaries
# =============================================================================
step "7/8  C test binaries"
echo "  Building C test binaries ..."
make -j"$(nproc)" -C "${DVF_ROOT}/c-test-binaries"
ok "C test binaries built"

# =============================================================================
# Step 8 — Deploy to 9p share
# =============================================================================
step "8/8  Deploy artifacts to 9p share"
DEPLOY_ARGS=()
if $SKIP_VISHWA; then
    DEPLOY_ARGS+=("--skip-vishwa-build")
fi
if $SKIP_KERNEL; then
    DEPLOY_ARGS+=("--skip-driver-build")
fi

export SHARE_DIR KERNEL_BUILD_DIR VISHWA_CODE_DIR
bash "${DVF_ROOT}/scripts/deploy_share.sh" "${DEPLOY_ARGS[@]+"${DEPLOY_ARGS[@]}"}"
ok "Share deployment complete"

# =============================================================================
# Summary
# =============================================================================
echo ""
echo -e "${GREEN}================================================================${RESET}"
echo -e "${GREEN}  DVF Bootstrap Complete!                                       ${RESET}"
echo -e "${GREEN}================================================================${RESET}"
echo ""
echo "  Next steps:"
echo "    1. source .env        (if you created one from .env.example)"
echo "    2. export DVF_ROOT=$(pwd)"
echo "    3. ./go-orchestrator/orchestrator --config go-orchestrator/configs --storage postgres"
echo "    4. curl http://localhost:9080/healthz"
echo ""
