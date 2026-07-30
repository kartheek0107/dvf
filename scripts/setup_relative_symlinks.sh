#!/usr/bin/env bash
# =============================================================================
# setup_relative_symlinks.sh — Auto-configure relative symlinks for 9p share
#
# Usage:
#   bash scripts/setup_relative_symlinks.sh [SHARE_DIR]
#
# This script ensures that all OpenCL/PoCL symlinks inside the share directory
# use relative paths (e.g. ../../../lib/pocl) instead of host absolute paths.
# =============================================================================

set -euo pipefail

SHARE_DIR="${1:-$HOME/qemu-rootfs/share}"
VISHWA_DIR="${SHARE_DIR}/vishwa_tests"

GREEN="\033[0;32m"
CYAN="\033[0;36m"
RESET="\033[0m"

echo -e "${CYAN}=== Auto-Configuring Relative Symlinks in ${VISHWA_DIR} ===${RESET}"

if [ ! -d "$VISHWA_DIR" ]; then
    echo "Directory ${VISHWA_DIR} not found. Skipping symlinks setup."
    exit 0
fi

# 1. Global PoCL include and device symlinks if host directories exist
GLOBAL_LIB="${VISHWA_DIR}/lib"
mkdir -p "${GLOBAL_LIB}/pocl"
mkdir -p "${VISHWA_DIR}/share/pocl/include"

if [ -d "/usr/lib64/pocl" ]; then
    cp -rL /usr/lib64/pocl/* "${GLOBAL_LIB}/pocl/" 2>/dev/null || true
fi
if [ -d "/usr/share/pocl/include" ]; then
    cp -rL /usr/share/pocl/include/* "${VISHWA_DIR}/share/pocl/include/" 2>/dev/null || true
fi

# 2. Fix per-test relative symlinks
find "${VISHWA_DIR}/opencl" -mindepth 1 -maxdepth 1 -type d | while read -r test_dir; do
    test_name=$(basename "$test_dir")
    lib_dir="${test_dir}/lib"
    mkdir -p "$lib_dir"

    # Fix per-test lib/pocl symlink -> ../../../lib/pocl
    (cd "$lib_dir" && ln -sf ../../../lib/pocl pocl)

    # Fix per-test lib/ld-linux-x86-64.so.2 -> ../../../lib/ld-linux-x86-64.so.2
    (cd "$lib_dir" && ln -sf ../../../lib/ld-linux-x86-64.so.2 ld-linux-x86-64.so.2)

    # Fix per-test share symlink -> ../../share
    (cd "$test_dir" && ln -sf ../../share share)

    echo -e "${GREEN}  ✓ Relative symlinks configured for ${test_name}${RESET}"
done

echo -e "${GREEN}=== Relative symlink setup complete ===${RESET}"
