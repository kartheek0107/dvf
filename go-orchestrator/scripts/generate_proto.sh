#!/bin/bash
# generate_proto.sh — Generates Go code from proto definitions.
#
# Prerequisites:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#   go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
#   go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest
#
# Usage:
#   cd go-orchestrator/
#   ./scripts/generate_proto.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
PROTO_DIR="$PROJECT_DIR/proto"

# Output directories for each proto package
ORCH_OUT="$PROTO_DIR/orchestratorpb"
TELEM_OUT="$PROTO_DIR/telemetrypb"
AGENT_OUT="$PROTO_DIR/agentpb"

# Ensure output dirs exist
mkdir -p "$ORCH_OUT" "$TELEM_OUT" "$AGENT_OUT"

# Ensure PATH includes Go bin
export PATH="$PATH:$(go env GOPATH)/bin"

echo "=== Generating orchestrator.proto ==="
protoc \
  --proto_path="$PROTO_DIR" \
  --proto_path="$(go env GOPATH)/pkg/mod/github.com/grpc-ecosystem/grpc-gateway/v2@$(go list -m -f '{{.Version}}' github.com/grpc-ecosystem/grpc-gateway/v2 2>/dev/null || echo 'v2.26.3')" \
  --proto_path="$(go env GOPATH)/pkg/mod/github.com/googleapis/googleapis@$(go list -m -f '{{.Version}}' github.com/googleapis/googleapis 2>/dev/null || echo 'v0.0.0-20250528174142-8ec13e190672')" \
  --go_out="$ORCH_OUT" --go_opt=paths=source_relative \
  --go-grpc_out="$ORCH_OUT" --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out="$ORCH_OUT" --grpc-gateway_opt=paths=source_relative \
  "$PROTO_DIR/orchestrator.proto"

echo "=== Generating telemetry.proto ==="
protoc \
  --proto_path="$PROTO_DIR" \
  --go_out="$TELEM_OUT" --go_opt=paths=source_relative \
  --go-grpc_out="$TELEM_OUT" --go-grpc_opt=paths=source_relative \
  "$PROTO_DIR/telemetry.proto"

echo "=== Generating agent.proto ==="
protoc \
  --proto_path="$PROTO_DIR" \
  --go_out="$AGENT_OUT" --go_opt=paths=source_relative \
  --go-grpc_out="$AGENT_OUT" --go-grpc_opt=paths=source_relative \
  "$PROTO_DIR/agent.proto"

echo "=== Proto generation complete ==="
echo "Generated files:"
find "$ORCH_OUT" "$TELEM_OUT" "$AGENT_OUT" -name "*.go" -type f | sort
