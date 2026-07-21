#!/usr/bin/env bash
# =============================================================================
# deploy_share.sh — Deploy all DVF artifacts to the QEMU 9p share directory.
#
# This is the local equivalent of the "deploy-share" CI stage.
# Run this after any change to driver source, Vishwa source, or test binaries.
#
# Usage:
#   ./scripts/deploy_share.sh [OPTIONS]
#
# Options:
#   --share-dir PATH      Override share directory (default: $HOME/qemu-rootfs/share)
#   --vishwa-code PATH    Override Vishwa code directory
#   --skip-vishwa-build   Skip the Vishwa build step (use pre-built artifacts)
#   --skip-driver-build   Skip the driver build step
#   --help                Show this help
# =============================================================================

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DVF_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

SHARE_DIR="${SHARE_DIR:-$HOME/qemu-rootfs/share}"
VISHWA_CODE_DIR="${VISHWA_CODE_DIR:-$HOME/Downloads/CDAC/FW_SW_Milestone_2/code}"
KERNEL_BUILD_DIR="${KERNEL_BUILD_DIR:-$HOME/VirtualMachines/linux}"

SKIP_VISHWA_BUILD=false
SKIP_DRIVER_BUILD=false

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --share-dir)       SHARE_DIR="$2";       shift 2 ;;
    --vishwa-code)     VISHWA_CODE_DIR="$2"; shift 2 ;;
    --skip-vishwa-build) SKIP_VISHWA_BUILD=true; shift ;;
    --skip-driver-build) SKIP_DRIVER_BUILD=true; shift ;;
    --help)
      sed -n '/^# ====/,/^# ====/p' "$0" | head -20
      exit 0 ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

GREEN="\033[0;32m"
YELLOW="\033[1;33m"
CYAN="\033[0;36m"
RESET="\033[0m"
step() { echo -e "\n${CYAN}[$(date +%H:%M:%S)] $*${RESET}"; }
ok()   { echo -e "${GREEN}  ✓ $*${RESET}"; }
warn() { echo -e "${YELLOW}  ⚠ $*${RESET}"; }

echo -e "${CYAN}======================================================${RESET}"
echo -e "${CYAN}  DVF Share Deployer                                  ${RESET}"
echo -e "${CYAN}  Share  : ${SHARE_DIR}${RESET}"
echo -e "${CYAN}  Vishwa : ${VISHWA_CODE_DIR}${RESET}"
echo -e "${CYAN}======================================================${RESET}"

# ── Step 0: Create directory tree ─────────────────────────────────────────────
step "0/10 Preparing share directory tree"
mkdir -p "${SHARE_DIR}"
mkdir -p "${SHARE_DIR}/vishwa_tests/lib/OpenCL/vendors"
for suite in regression/vecaddx opencl/vecadd opencl/vecadd2 \
             opencl/ai_predict opencl/blur opencl/gray opencl/sgemm; do
  mkdir -p "${SHARE_DIR}/vishwa_tests/${suite}"
done
mkdir -p "${SHARE_DIR}/vishwa_tests/bin"
mkdir -p "${SHARE_DIR}/python-agent"
mkdir -p "${SHARE_DIR}/dvf_tests"
ok "Directory tree ready"

# ── Step 1: Build the GPGPU driver ────────────────────────────────────────────
step "1/10 Building GPGPU driver"
if $SKIP_DRIVER_BUILD; then
  warn "Skipping driver build (--skip-driver-build)"
else
  if [ ! -d "$KERNEL_BUILD_DIR" ]; then
    echo "ERROR: Kernel build directory not found: $KERNEL_BUILD_DIR"
    exit 1
  fi
  make -C "${DVF_ROOT}/driver-source/gpgpu_driver" KDIR="$KERNEL_BUILD_DIR" -j4 2>&1 | tail -5
  ok "Driver built: $(ls -lh ${DVF_ROOT}/driver-source/gpgpu_driver/gpgpu_pcie_ep_driver.ko | awk '{print $5, $9}')"
fi

# ── Step 2: Deploy driver .ko ─────────────────────────────────────────────────
step "2/10 Deploying driver .ko"
cp "${DVF_ROOT}/driver-source/gpgpu_driver/gpgpu_pcie_ep_driver.ko" "${SHARE_DIR}/"
ok "Deployed gpgpu_pcie_ep_driver.ko"

# ── Step 3: Deploy the DVF agent ──────────────────────────────────────────────
step "3/10 Deploying DVF agent"
# Clean up only files we own (root-owned __pycache__ from VM run are skipped)
find "${SHARE_DIR}/python-agent" -maxdepth 5 \
  \( -name '*.pyc' -o -name '__pycache__' \) \
  -user "$USER" -exec rm -rf {} + 2>/dev/null || true
mkdir -p "${SHARE_DIR}/python-agent"
# Sync agent source, excluding pycache
rsync -a --exclude='__pycache__/' --exclude='*.pyc' \
  "${DVF_ROOT}/python-agent/" "${SHARE_DIR}/python-agent/" 2>/dev/null || \
  cp -r --no-preserve=ownership "${DVF_ROOT}/python-agent/." "${SHARE_DIR}/python-agent/"
cp "${DVF_ROOT}/python-agent/agent/start_agent.sh" "${SHARE_DIR}/start_agent.sh"
chmod +x "${SHARE_DIR}/start_agent.sh"
ok "Agent deployed"

# ── Step 4: Build and deploy DVF C test binaries ──────────────────────────────
step "4/10 Building and deploying DVF C test binaries"
C_TESTS_DIR="${DVF_ROOT}/c-test-binaries"

echo "  Building C test binaries (make -j4)..."
make -j4 -C "${C_TESTS_DIR}" 2>&1 | tail -5

# Wipe destination clean before install so stale system libs (e.g. libc.so.6
# bundled before the system-lib blocklist existed) never persist across deploys.
echo "  Cleaning stale dvf_tests/ before install..."
rm -rf "${SHARE_DIR}/dvf_tests"
mkdir -p "${SHARE_DIR}/dvf_tests"

echo "  Installing binaries to ${SHARE_DIR}/dvf_tests/..."
make -C "${C_TESTS_DIR}" install SHARE_DIR="${SHARE_DIR}" 2>&1 | tail -5

# Purge any system libs that snuck in via committed source lib/ dirs.
# These must always come from the guest OS — never from the host.
echo "  Purging system libs from dvf_tests lib/ dirs..."
for pat in "libc.so*" "libm.so*" "libpthread.so*" "libgcc_s.so*" \
           "libdl.so*" "librt.so*" "libresolv.so*" "libcrypt.so*"; do
  find "${SHARE_DIR}/dvf_tests" -name "$pat" -delete 2>/dev/null || true
done

# Bundle .so dependencies for each installed C test binary.
# Exclude files inside lib/ dirs (they are already bundled .so files, not test binaries).
# Also exclude .sh scripts since bundle_libs only makes sense for ELF binaries.
echo "  Bundling .so dependencies for C test binaries..."
find "${SHARE_DIR}/dvf_tests" -type f -executable \
  -not -path "*/lib/*" \
  -not -name "*.sh" | while read binary; do
  rel=$(realpath --relative-to="${SHARE_DIR}/dvf_tests" "$binary")
  echo "  → dvf_tests/${rel}"
  bash "${DVF_ROOT}/scripts/bundle_libs.sh" \
    "$binary" "$(dirname $binary)/lib" 2>&1 | \
    grep -E "^  (COPY|WARN|SKIP|===|Done)" | sed 's/^/    /' || true
done

ok "C test binaries deployed to ${SHARE_DIR}/dvf_tests/"

# ── Step 5: Build Vishwa ──────────────────────────────────────────────────────
step "5/10 Building Vishwa runtime and tests"
if $SKIP_VISHWA_BUILD; then
  warn "Skipping Vishwa build (--skip-vishwa-build)"
else
  VISHWA_ENV_DIR="${VISHWA_CODE_DIR}/vishwa_hw_testing_env"
  if [ -f "${VISHWA_ENV_DIR}/vishwa_run.sh" ]; then
    (cd "${VISHWA_ENV_DIR}" && bash vishwa_run.sh 2>&1 | tail -10)
    ok "Vishwa built"
  else
    warn "vishwa_run.sh not found at ${VISHWA_ENV_DIR}/ — skipping build"
  fi
fi

# ── Step 5: Deploy Vishwa libraries and binaries ──────────────────────────────
step "6/10 Deploying Vishwa libraries and test binaries"
VISHWA_BUILD="${VISHWA_CODE_DIR}/vishwa_hw_testing_env/build"
cp_if() {
  local src="$1" dst="$2"
  if [ -f "$src" ]; then cp "$src" "$dst" && ok "$(basename $src)"; \
  else warn "Not found: $src"; fi
}

# Runtime .so files
cp_if "${VISHWA_BUILD}/runtime/xrt/libvishwa.so"  "${SHARE_DIR}/vishwa_tests/lib/"
cp_if "${VISHWA_BUILD}/runtime/gpurt/libgpurt.so" "${SHARE_DIR}/vishwa_tests/lib/"

# Test binaries
cp_if "${VISHWA_BUILD}/tests/regression/vecaddx/vecaddx"      "${SHARE_DIR}/vishwa_tests/regression/vecaddx/"
cp_if "${VISHWA_BUILD}/tests/opencl/vecadd/vecadd"            "${SHARE_DIR}/vishwa_tests/opencl/vecadd/"
cp_if "${VISHWA_BUILD}/tests/opencl/vecadd2/vecadd2"          "${SHARE_DIR}/vishwa_tests/opencl/vecadd2/"
cp_if "${VISHWA_BUILD}/tests/opencl/ai_predict/ai_predict"    "${SHARE_DIR}/vishwa_tests/opencl/ai_predict/"
cp_if "${VISHWA_BUILD}/tests/opencl/blur/blur"                "${SHARE_DIR}/vishwa_tests/opencl/blur/"
cp_if "${VISHWA_BUILD}/tests/opencl/gray/gray"                "${SHARE_DIR}/vishwa_tests/opencl/gray/"
cp_if "${VISHWA_BUILD}/tests/opencl/sgemm/sgemm"              "${SHARE_DIR}/vishwa_tests/opencl/sgemm/"

# Per-test assets (OpenCL kernels, images, weights)
TESTS_SRC="${VISHWA_CODE_DIR}/vishwa_hw_testing_env/tests"
cp_if "${TESTS_SRC}/opencl/vecadd/kernel.cl"           "${SHARE_DIR}/vishwa_tests/opencl/vecadd/"
cp_if "${TESTS_SRC}/opencl/vecadd2/kernel.cl"          "${SHARE_DIR}/vishwa_tests/opencl/vecadd2/"
cp_if "${TESTS_SRC}/opencl/sgemm/kernel.cl"            "${SHARE_DIR}/vishwa_tests/opencl/sgemm/"
cp_if "${TESTS_SRC}/opencl/blur/kernel.cl"             "${SHARE_DIR}/vishwa_tests/opencl/blur/"
cp_if "${TESTS_SRC}/opencl/blur/input.jpg"             "${SHARE_DIR}/vishwa_tests/opencl/blur/"
cp_if "${TESTS_SRC}/opencl/gray/kernel.cl"             "${SHARE_DIR}/vishwa_tests/opencl/gray/"
cp_if "${TESTS_SRC}/opencl/gray/input.jpg"             "${SHARE_DIR}/vishwa_tests/opencl/gray/"
cp_if "${TESTS_SRC}/opencl/ai_predict/kernel.cl"       "${SHARE_DIR}/vishwa_tests/opencl/ai_predict/"
cp_if "${TESTS_SRC}/opencl/ai_predict/input.png"       "${SHARE_DIR}/vishwa_tests/opencl/ai_predict/"
cp_if "${TESTS_SRC}/opencl/ai_predict/bias.txt"        "${SHARE_DIR}/vishwa_tests/opencl/ai_predict/"
cp_if "${TESTS_SRC}/opencl/ai_predict/weights.txt"     "${SHARE_DIR}/vishwa_tests/opencl/ai_predict/"

# ── Step 6: Deploy host linker + standard libs ────────────────────────────────
step "7/10 Deploying host dynamic linker and standard libraries"
LIB="${SHARE_DIR}/vishwa_tests/lib"
cp_l() {
  local src="$1" name="$2"
  if [ -f "$src" ]; then cp -L "$src" "${LIB}/${name}" && ok "${name}"; \
  else warn "Not found: $src"; fi
}
cp_l /lib64/ld-linux-x86-64.so.2  ld-linux-x86-64.so.2
cp_l /lib64/libc.so.6             libc.so.6
cp_l /lib64/libm.so.6             libm.so.6
cp_l /lib64/libpthread.so.0       libpthread.so.0
cp_l /lib64/libgcc_s.so.1         libgcc_s.so.1
cp_l /lib64/libstdc++.so.6        libstdc++.so.6

# ── Step 7: Strategy 1 — Bundle per-test lib/ directories ────────────────────
step "8/10 Bundling per-test .so dependencies (Strategy 1)"
bundle_test() {
  local binary="$1"
  if [ -f "$binary" ]; then
    echo "  \u2192 $(basename $(dirname $binary))/$(basename $binary)"
    # Suppress WARN for libvishwa.so and libgpurt.so — these are Vishwa-internal
    # libs not present on the host; step 7b copies them explicitly from the share.
    bash "${DVF_ROOT}/scripts/bundle_libs.sh" "$binary" "$(dirname $binary)/lib" 2>&1 | \
      grep -E "^  (COPY|===|Done)" | sed 's/^/    /'
  fi
}
bundle_test "${SHARE_DIR}/vishwa_tests/regression/vecaddx/vecaddx"
bundle_test "${SHARE_DIR}/vishwa_tests/opencl/vecadd/vecadd"
bundle_test "${SHARE_DIR}/vishwa_tests/opencl/vecadd2/vecadd2"
bundle_test "${SHARE_DIR}/vishwa_tests/opencl/ai_predict/ai_predict"
bundle_test "${SHARE_DIR}/vishwa_tests/opencl/blur/blur"
bundle_test "${SHARE_DIR}/vishwa_tests/opencl/gray/gray"
bundle_test "${SHARE_DIR}/vishwa_tests/opencl/sgemm/sgemm"

# ── Step 7b: Vishwa-internal libs (ldd-invisible, must be copied manually) ────
# libgpurt.so and libvishwa.so are not installed on the host system — they only
# exist inside the share itself.  ldd (used by bundle_libs.sh) cannot find them,
# so bundle_libs.sh warns and skips them.  We copy them explicitly here.
#
# Similarly, the OpenCL ICD loader (libOpenCL.so) and PoCL runtime are needed
# by every OpenCL test but may not resolve correctly with ldd on all machines.
# Copying them from the already-deployed global lib/ guarantees every test's
# lib/ directory is completely self-contained.
echo ""
echo "  → Copying Vishwa-internal + OpenCL libs into every test lib/"
GLOBAL_LIB="${SHARE_DIR}/vishwa_tests/lib"
for test_dir in \
    "${SHARE_DIR}/vishwa_tests/regression/vecaddx" \
    "${SHARE_DIR}/vishwa_tests/opencl/vecadd" \
    "${SHARE_DIR}/vishwa_tests/opencl/vecadd2" \
    "${SHARE_DIR}/vishwa_tests/opencl/ai_predict" \
    "${SHARE_DIR}/vishwa_tests/opencl/blur" \
    "${SHARE_DIR}/vishwa_tests/opencl/gray" \
    "${SHARE_DIR}/vishwa_tests/opencl/sgemm"; do

    [ -d "$test_dir" ] || continue
    mkdir -p "${test_dir}/lib"

    # Vishwa runtime (only present in the share, not on the host)
    for so in libgpurt.so libvishwa.so; do
        [ -f "${GLOBAL_LIB}/${so}" ] && \
            cp "${GLOBAL_LIB}/${so}" "${test_dir}/lib/${so}" 2>/dev/null || true
    done

    # OpenCL ICD loader + PoCL (needed by all OpenCL tests)
    for so in libOpenCL.so libOpenCL.so.1 libOpenCL.so.1.0.0 libpocl.so.2; do
        [ -f "${GLOBAL_LIB}/${so}" ] && \
            cp "${GLOBAL_LIB}/${so}" "${test_dir}/lib/${so}" 2>/dev/null || true
    done

    echo "    ✓ $(basename $test_dir)/lib/ ($(ls ${test_dir}/lib/ | wc -l) files)"
done

# ── Step 8: OpenCL ICD loader + PoCL ─────────────────────────────────────────
step "9/10 Deploying OpenCL / PoCL runtime"
if [ -f /usr/lib64/libOpenCL.so.1.0.0 ]; then
  cp -L /usr/lib64/libOpenCL.so.1.0.0 "${LIB}/libOpenCL.so.1.0.0"
  cp -L /usr/lib64/libOpenCL.so.1.0.0 "${LIB}/libOpenCL.so.1"
  cp -L /usr/lib64/libOpenCL.so.1.0.0 "${LIB}/libOpenCL.so"
  ok "OpenCL ICD loader"
else
  warn "libOpenCL.so not found"
fi

POCL=$(find /usr/lib64 -name "libpocl.so.*.*.0" 2>/dev/null | head -1)
if [ -n "$POCL" ]; then
  cp -L "$POCL" "${LIB}/$(basename $POCL)"
  POCL_SHORT=$(basename "$POCL" | sed 's/\.[0-9]*\.0$//')
  cp -L "$POCL" "${LIB}/${POCL_SHORT}" 2>/dev/null || true
  echo "libpocl.so.2" > "${LIB}/OpenCL/vendors/pocl.icd"
  ok "PoCL: $(basename $POCL)"
else
  warn "PoCL not installed — OpenCL tests will fail. Install with: sudo dnf install -y pocl"
fi

# ── Step 9: Summary ───────────────────────────────────────────────────────────
step "10/10 Deployment summary"
echo ""
echo "  Share root:"
ls -lh "${SHARE_DIR}/" | tail -10
echo ""
echo "  Per-test lib/ directories:"
find "${SHARE_DIR}/vishwa_tests" "${SHARE_DIR}/dvf_tests" -type d -name lib 2>/dev/null | while read d; do
  count=$(ls "$d" 2>/dev/null | wc -l)
  echo "    $d  (${count} files)"
done

echo ""
echo -e "${GREEN}======================================================${RESET}"
echo -e "${GREEN}  Deployment complete — SHARE_DIR: ${SHARE_DIR}${RESET}"
echo -e "${GREEN}======================================================${RESET}"
