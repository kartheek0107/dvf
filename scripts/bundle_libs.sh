#!/usr/bin/env bash
# bundle_libs.sh — Copy all shared library dependencies of a test binary
#                  into a lib/ directory next to it.
#
# Usage:
#   ./scripts/bundle_libs.sh <binary> [output_lib_dir]
#
# Examples:
#   ./scripts/bundle_libs.sh test-suites/opencl/vecadd/vecadd
#   ./scripts/bundle_libs.sh test-suites/opencl/vecadd/vecadd /custom/lib/path
#
# After running this script, the lib/ directory can be committed alongside
# the test binary.  The DVF runner will automatically pick it up at runtime.
# No changes to .gitlab-ci.yml, Packer, or the VM image are needed.
#
# What it does:
#   1. Runs ldd on the binary to find all .so dependencies
#   2. Copies each .so into the lib/ directory
#   3. Creates any necessary .so symlinks for versioned libraries
#
# Prerequisites:
#   - ldd (part of glibc-utils / libc-bin — available on any Linux host)
#   - The binary must be compiled and exist on this host

set -euo pipefail

BINARY="${1:-}"
if [[ -z "$BINARY" ]]; then
    echo "Usage: $0 <binary> [output_lib_dir]"
    echo ""
    echo "Example:"
    echo "  $0 test-suites/opencl/vecadd/vecadd"
    exit 1
fi

if [[ ! -f "$BINARY" ]]; then
    echo "ERROR: Binary not found: $BINARY" >&2
    exit 1
fi

BINARY_DIR="$(dirname "$(realpath "$BINARY")")"
LIB_DIR="${2:-$BINARY_DIR/lib}"

echo "=== DVF Library Bundler ==="
echo "Binary   : $BINARY"
echo "Lib dir  : $LIB_DIR"
echo ""

mkdir -p "$LIB_DIR"

# ── System library blocklist ───────────────────────────────────────────────────
# These libraries are tightly coupled to the dynamic linker (ld-linux) on the
# TARGET machine. Bundling them from the host causes GLIBC_PRIVATE symbol
# mismatches at runtime when the host and guest have different glibc versions
# (e.g. Fedora host → Alpine or Debian guest).
#
# Rule: only bundle NON-SYSTEM libs (e.g. libopenblas, libvishwa, libgpurt).
# Let the guest OS supply its own glibc, libm, libpthread, and libgcc_s.
SYSTEM_LIBS=(
    "libc.so"
    "libm.so"
    "libpthread.so"
    "libgcc_s.so"
    "libdl.so"
    "librt.so"
    "libresolv.so"
    "libnss"
    "libutil.so"
    "libcrypt.so"
)

is_system_lib() {
    local name="$1"
    for pat in "${SYSTEM_LIBS[@]}"; do
        if [[ "$name" == ${pat}* ]]; then
            return 0
        fi
    done
    return 1
}

# Run ldd and parse output
BUNDLED=0
SKIPPED=0

while IFS= read -r line; do
    # ldd output formats:
    #   libfoo.so.1 => /usr/lib/x86_64-linux-gnu/libfoo.so.1 (0x...)
    #   /lib64/ld-linux-x86-64.so.2 (0x...)
    #   linux-vdso.so.1 (0x...) => skip (virtual)
    #   not found => warn

    if echo "$line" | grep -q "linux-vdso\|ld-linux"; then
        continue  # skip the dynamic linker / vDSO itself
    fi

    if echo "$line" | grep -q "not found"; then
        libname=$(echo "$line" | awk '{print $1}')
        echo "  WARN: $libname not found on this host — cannot bundle it"
        continue
    fi

    # Extract the resolved path (the part after => )
    sopath=$(echo "$line" | awk '{print $3}')
    if [[ -z "$sopath" || "$sopath" == "(0x"* ]]; then
        continue
    fi

    if [[ ! -f "$sopath" ]]; then
        continue
    fi

    soname="$(basename "$sopath")"
    dest="$LIB_DIR/$soname"

    # Skip system libraries — they must come from the guest OS, not the host
    if is_system_lib "$soname"; then
        echo "  SYSLIB: $soname — skipped (must come from guest OS, not host)"
        ((SKIPPED++)) || true
        continue
    fi

    if [[ -e "$dest" ]]; then
        echo "  SKIP : $soname (already in lib/)"
        ((SKIPPED++)) || true
        continue
    fi

    cp -L "$sopath" "$dest"
    echo "  COPY : $soname  ← $sopath"
    ((BUNDLED++)) || true

done < <(ldd "$BINARY" 2>/dev/null)

echo ""
echo "=== Done ==="
echo "  Bundled : $BUNDLED libraries → $LIB_DIR/"
echo "  Skipped : $SKIPPED (already present)"
echo ""

if [[ $BUNDLED -gt 0 ]]; then
    echo "Next steps:"
    echo "  1. Commit the lib/ directory alongside your test binary"
    echo "  2. The DVF runner will automatically use it — no other changes needed"
    echo ""
    echo "Files in $LIB_DIR/:"
    ls -lh "$LIB_DIR/"
fi
