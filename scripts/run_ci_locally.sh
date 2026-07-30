#!/bin/bash
# ==============================================================================
# run_ci_locally.sh
#
# Helper script to run the exact DVF CI validation flow locally.
# ==============================================================================

set -e

# Cleanup trap to ensure orchestrator is killed on exit
cleanup() {
    if [ -n "$ORCHESTRATOR_PID" ]; then
        echo "Shutting down orchestrator (PID: $ORCHESTRATOR_PID)..."
        kill "$ORCHESTRATOR_PID" || true
    fi
}
trap cleanup EXIT

# 1. Start the orchestrator in the background using memory storage
echo "Starting orchestrator in the background..."
./orchestrator --config configs --storage memory > orchestrator_local_ci.log 2>&1 &
ORCHESTRATOR_PID=$!

# 2. Wait for the orchestrator REST API to become ready
echo "Waiting for orchestrator to start..."
for i in {1..30}; do
    if curl -s http://localhost:9080/healthz > /dev/null; then
        echo "Orchestrator is ready!"
        break
    fi
    sleep 1
done

# 3. Check if the orchestrator process is still running
if ! kill -0 "$ORCHESTRATOR_PID" 2>/dev/null; then
    echo "Orchestrator failed to start. Logs:"
    cat orchestrator_local_ci.log
    exit 1
fi

# 4. Run the CI impact analyzer
echo "Running DVF CI Impact Analyzer..."
python3 scripts/ci_impact_analyzer.py "$@"

echo "CI validation flow completed successfully."
