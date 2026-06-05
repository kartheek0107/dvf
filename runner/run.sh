#!/bin/bash
# DVF Local Test Runner — Entry point
#
# Usage:
#   ./runner/run.sh                   # run all test suites
#   ./runner/run.sh --suite smoke     # run only smoke-related tests
#   ./runner/run.sh --build-only      # just build test binaries
#   ./runner/run.sh --skip-build      # skip build, run tests only
#   ./runner/run.sh --no-kvm          # disable KVM (slower but works without VT-x)
#
# Prerequisites:
#   - Python 3.6+ with PyYAML (pip install pyyaml)
#   - GCC for building C test binaries
#   - Your custom QEMU build with the gpgpu device

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Check for Python
if ! command -v python3 &> /dev/null; then
    echo "ERROR: python3 not found. Please install Python 3."
    exit 1
fi

# Check for PyYAML
python3 -c "import yaml" 2>/dev/null || {
    echo "Installing PyYAML..."
    pip3 install --user pyyaml
}

# Run the local runner
exec python3 "$SCRIPT_DIR/local_runner.py" "$@"
