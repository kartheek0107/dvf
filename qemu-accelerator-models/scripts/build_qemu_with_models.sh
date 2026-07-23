#!/usr/bin/env bash
# =============================================================================
# scripts/build_qemu_with_models.sh
#
# Builds qemu-system-x86_64 with DVF device models baked in.
# Called automatically by:  ninja -C builds/qemu-build
#
# Arguments (passed by Meson's custom_target — do not call directly):
#   $1  MODELS_ROOT  — absolute path to qemu-accelerator-models/
#   $2  OUT_DIR      — Meson @OUTDIR@ (build output directory)
#   $3  QEMU_VERSION — QEMU git tag to clone if no source exists (e.g. v8.2.0)
#   $4  QEMU_SRC     — optional: path to a pre-existing QEMU source tree
#                      (skips git clone; use if you already have QEMU checked out)
#
# Output:
#   $OUT_DIR/qemu-system-x86_64  ← the self-contained binary Ninja tracks
#   $OUT_DIR/installed/bin/      ← full install tree
#
# To use the binary, set DVF_ROOT to the repo root — global_config.json reads:
#   "binary_path": "$DVF_ROOT/builds/qemu/qemu-system-x86_64"
# =============================================================================

set -euo pipefail

MODELS_ROOT="$(realpath "${1:?ARG1 (models root) is required}")"
OUT_DIR="$(realpath "${2:?ARG2 (output dir) is required}")"
QEMU_VERSION="${3:-v8.2.0}"
QEMU_SRC="${4:-}"

# All intermediate artefacts live inside OUT_DIR so they are isolated from any
# pre-existing QEMU source tree the user may have on their machine.
QEMU_CLONE_DIR="${OUT_DIR}/qemu-src"
QEMU_BUILD_DIR="${OUT_DIR}/qemu-build-inner"
QEMU_INSTALL_DIR="${OUT_DIR}/installed"
QEMU_BINARY="${QEMU_INSTALL_DIR}/bin/qemu-system-x86_64"

log() { echo "[dvf-qemu] $*"; }

# ── Step 1: Get QEMU source ───────────────────────────────────────────────────
# Priority: explicit QEMU_SRC arg > cached clone in OUT_DIR > fresh clone.
# When QEMU_SRC points at an existing tree (e.g. ~/VirtualMachines/qemu) the
# script uses that tree READ-ONLY for source but always writes build artefacts
# into QEMU_BUILD_DIR (inside OUT_DIR), so it never pollutes external trees.

if [[ -n "$QEMU_SRC" && -d "$QEMU_SRC" ]]; then
    log "Using existing QEMU source tree: $QEMU_SRC"
    QEMU_CLONE_DIR="$(realpath "$QEMU_SRC")"
elif [[ -d "${QEMU_CLONE_DIR}/.git" ]]; then
    log "Reusing cached QEMU source: $QEMU_CLONE_DIR"
else
    log "Cloning QEMU $QEMU_VERSION (one-time, ~300 MB) ..."
    git clone --depth=1 --branch "$QEMU_VERSION" \
        https://gitlab.com/qemu-project/qemu.git \
        "$QEMU_CLONE_DIR"
    cd "$QEMU_CLONE_DIR"
    git submodule update --init --recursive --depth=1
    cd - > /dev/null
fi

# ── Step 2: Inject DVF device models ─────────────────────────────────────────
# gp_gpu.c is a PCI device — it uses pci_ss (not softmmu_ss) and must live
# under hw/pci/.  We use hw/pci-dvf/ to avoid touching QEMU's own hw/pci/.

DVF_SRC="${MODELS_ROOT}/hw/misc"
DVF_DST="${QEMU_CLONE_DIR}/hw/pci/pci-dvf"   # subdir() resolves relative to hw/pci/

log "Injecting device models → $DVF_DST"
mkdir -p "$DVF_DST"

# Sync all .c files from this repo into the injection directory
find "$DVF_SRC" -maxdepth 1 -name '*.c' -exec cp -f {} "$DVF_DST/" \;

# Copy the meson.build fragment (uses pci_ss — correct for hw/pci/ context)
cp -f "${DVF_SRC}/meson.build" "${DVF_DST}/meson.build"

# Wire pci-dvf into QEMU's hw/pci/meson.build — insert BEFORE the line that
# calls system_ss.add_all() which finalises pci_ss.  Appending after that line
# causes "Tried to use 'add' after querying the source set".
PCI_MESON="${QEMU_CLONE_DIR}/hw/pci/meson.build"

# Dedup: if gpgpu.c was already registered directly in hw/pci/meson.build
# (common when the source tree was previously patched manually), remove it so
# the linker doesn't see two object files defining the same global symbols.
if grep -q "gpgpu\.c" "$PCI_MESON"; then
    sed -i "s/'gpgpu\.c',\?//g; s/,\s*'gpgpu\.c'//g" "$PCI_MESON"
    log "Removed pre-existing 'gpgpu.c' entry from hw/pci/meson.build (dedup)"
fi

if ! grep -q "pci-dvf" "$PCI_MESON"; then
    # Insert subdir('pci-dvf') immediately before the system_ss.add_all line
    sed -i "/system_ss\.add_all/i subdir('pci-dvf')" "$PCI_MESON"
    log "Inserted subdir('pci-dvf') before system_ss.add_all in hw/pci/meson.build"
else
    log "hw/pci/pci-dvf already registered (skipping)"
fi

# ── Step 3: Configure ────────────────────────────────────────────────────────
# QEMU 8.x ./configure always writes artefacts into a 'build/' subdir relative
# to the WORKING DIRECTORY from which it is invoked.  So we:
#   1. Create our desired build dir (QEMU_BUILD_DIR)
#   2. Run ./configure FROM there — QEMU writes 'build/' inside it
# The actual ninja-runnable dir is then QEMU_BUILD_DIR/build/.

QEMU_NINJA_DIR="${QEMU_BUILD_DIR}"   # configure writes build.ninja here directly

if [[ ! -f "${QEMU_NINJA_DIR}/build.ninja" ]]; then
    log "Configuring QEMU ..."
    mkdir -p "$QEMU_BUILD_DIR"
    cd "$QEMU_BUILD_DIR"
    "${QEMU_CLONE_DIR}/configure" \
        --prefix="$QEMU_INSTALL_DIR" \
        --target-list=x86_64-softmmu \
        --enable-kvm \
        --disable-docs \
        --disable-werror \
        --extra-cflags="-O2"
    cd - > /dev/null
else
    log "Already configured — skipping (device model .c files refreshed in Step 2)"
fi

# ── Step 4: Compile ──────────────────────────────────────────────────────────

JOBS=$(nproc 2>/dev/null || echo 4)
log "Compiling QEMU with $JOBS parallel jobs ..."
ninja -C "$QEMU_NINJA_DIR" -j"$JOBS"

# ── Step 5: Install and expose ───────────────────────────────────────────────

log "Installing to $QEMU_INSTALL_DIR ..."
ninja -C "$QEMU_NINJA_DIR" install

# Copy to @OUTDIR@ so Meson's custom_target can track it as a build output.
cp "$QEMU_BINARY" "${OUT_DIR}/qemu-system-x86_64"
chmod +x "${OUT_DIR}/qemu-system-x86_64"

log ""
log "  ✓ Binary ready: ${OUT_DIR}/qemu-system-x86_64"
log "  ✓ Verify:       ${OUT_DIR}/qemu-system-x86_64 -device gp_gpu,help"
log ""
log "  Set in global_config.json:"
log "    \"binary_path\": \"\${DVF_ROOT}/builds/qemu/qemu-system-x86_64\""
log ""
log "  Tip: set DVF_ROOT=\$(pwd) before starting the orchestrator."
