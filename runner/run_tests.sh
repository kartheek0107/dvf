#!/bin/bash
# DVF Test Runner — Runs all test binaries inside the guest VM.
#
# This script is meant to be run INSIDE the QEMU guest, not on the host.
# It assumes:
#   - The 9p share is mounted (e.g., at /mnt/share)
#   - The gpgpu driver is loaded (insmod gpgpu_driver.ko)
#
# Usage (inside guest):
#   /mnt/share/dvf_tests/run_tests.sh
#   /mnt/share/dvf_tests/run_tests.sh --suite read_write
#   /mnt/share/dvf_tests/run_tests.sh --suite concurrency

set -euo pipefail

# --- Config ---
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEVICE="/dev/gpgpu"
DRIVER_KO="$SCRIPT_DIR/../gpgpu_driver.ko"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
RESET='\033[0m'

# --- Parse args ---
SUITE_FILTER="${1:-all}"
if [ "$SUITE_FILTER" = "--suite" ]; then
    SUITE_FILTER="${2:-all}"
fi

# --- Functions ---
header() {
    echo ""
    echo -e "${BOLD}${CYAN}============================================${RESET}"
    echo -e "${BOLD}${CYAN}  $1${RESET}"
    echo -e "${BOLD}${CYAN}============================================${RESET}"
}

info()    { echo -e "${CYAN}[DVF]${RESET} $1"; }
ok()      { echo -e "${GREEN}  ✓${RESET} $1"; }
fail()    { echo -e "${RED}  ✗${RESET} $1"; }
warn()    { echo -e "${YELLOW}[WARN]${RESET} $1"; }

# --- Main ---
header "DVF Driver Validation Suite"

# Step 1: Check/load driver
if [ ! -c "$DEVICE" ]; then
    info "Device $DEVICE not found — loading driver..."
    if [ -f "$DRIVER_KO" ]; then
        insmod "$DRIVER_KO"
        sleep 0.5
        if [ -c "$DEVICE" ]; then
            ok "Driver loaded, $DEVICE is ready"
        else
            fail "Driver loaded but $DEVICE not created"
            exit 1
        fi
    else
        fail "Driver not found at $DRIVER_KO"
        fail "Please load it manually: insmod /path/to/gpgpu_driver.ko"
        exit 1
    fi
else
    ok "Device $DEVICE already present"
fi

# Step 2: Discover test binaries
ALL_TESTS=(
    "read_write/test_register_rw"
    "read_write/test_offset_sweep"
    "data_integrity/test_patterns"
    "data_integrity/test_persistence"
    "concurrency/test_concurrent_rw"
    "stress_performance/test_throughput"
    "error_injection/test_boundaries"
)

# Filter if requested
TESTS=()
for t in "${ALL_TESTS[@]}"; do
    if [ "$SUITE_FILTER" = "all" ] || echo "$t" | grep -q "$SUITE_FILTER"; then
        TESTS+=("$t")
    fi
done

if [ ${#TESTS[@]} -eq 0 ]; then
    fail "No tests matching filter: $SUITE_FILTER"
    exit 1
fi

info "Running ${#TESTS[@]} test suites (filter: $SUITE_FILTER)"
echo ""

# Step 3: Run each test
TOTAL_PASS=0
TOTAL_FAIL=0
TOTAL_TESTS=0
RESULTS_DIR="$SCRIPT_DIR/results"
mkdir -p "$RESULTS_DIR"

for test_path in "${TESTS[@]}"; do
    binary="$SCRIPT_DIR/$test_path"
    suite_name=$(echo "$test_path" | tr '/' '::')

    echo -e "${BOLD}[SUITE] $suite_name${RESET}"

    if [ ! -x "$binary" ]; then
        fail "Binary not found or not executable: $binary"
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
        continue
    fi

    # Run the test — stderr goes to terminal (human output), stdout is JSON
    json_file="$RESULTS_DIR/${suite_name}.json"
    if "$binary" > "$json_file" 2>&1; then
        : # test passed (exit 0)
    fi

    # The test framework writes human output to stderr and JSON to stdout.
    # Since we're in a simple terminal, let's just run it directly:
    echo ""
    "$binary" 2>&1 || true
    echo ""

    # Parse results from JSON output
    if [ -f "$json_file" ]; then
        passed=$(grep -o '"passed": [0-9]*' "$json_file" 2>/dev/null | head -1 | grep -o '[0-9]*' || echo 0)
        failed=$(grep -o '"failed": [0-9]*' "$json_file" 2>/dev/null | head -1 | grep -o '[0-9]*' || echo 0)
        TOTAL_PASS=$((TOTAL_PASS + passed))
        TOTAL_FAIL=$((TOTAL_FAIL + failed))
        TOTAL_TESTS=$((TOTAL_TESTS + passed + failed))
    fi
done

# Step 4: Summary
header "Test Run Complete"
echo ""
echo -e "  Total:   $TOTAL_TESTS"
echo -e "  ${GREEN}Passed:  $TOTAL_PASS${RESET}"
if [ "$TOTAL_FAIL" -gt 0 ]; then
    echo -e "  ${RED}Failed:  $TOTAL_FAIL${RESET}"
    echo ""
    echo -e "  ${BOLD}${RED}=== FAILED ===${RESET}"
    exit 1
else
    echo ""
    echo -e "  ${BOLD}${GREEN}=== ALL PASSED ===${RESET}"
    exit 0
fi
