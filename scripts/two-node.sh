#!/usr/bin/env bash
# two-node.sh — launch a local two-node RED federation for beta testing.
#
# Node A: http://localhost:8080  (data/ , state .red-demo/stateA)
# Node B: http://localhost:8081  (data1/, state .red-demo/stateB)
#
# Each node gets its OWN state dir (identity key + registry.db) via RED_STATE_DIR
# so they are distinct peers. Content dirs (data/, data1/) are reused as-is.
# Ctrl-C stops both.
set -euo pipefail
cd "$(dirname "$0")/.."

TOKEN_A="${RED_ADMIN_TOKEN_A:-dev-token-A}"
TOKEN_B="${RED_ADMIN_TOKEN_B:-dev-token-B}"

echo "==> Building frontend"
(cd internal/router/red-engine-frontend && npm install --silent && npm run build) || echo "Warning: frontend build failed, continuing..."

echo "==> Building red-engine binary"
go build -o ./red ./cmd/red

mkdir -p .red-demo/stateA .red-demo/stateB data1

echo "==> Starting Node A on :8080 (data/, admin token: $TOKEN_A)"
RED_ADDR=":8080" RED_DATA_DIR="data" RED_STATE_DIR=".red-demo/stateA" \
  RED_ADMIN_TOKEN="$TOKEN_A" RED_NODE_NAME="node-A" ./red &
PID_A=$!

echo "==> Starting Node B on :8081 (data1/, admin token: $TOKEN_B)"
RED_ADDR=":8081" RED_DATA_DIR="data1" RED_STATE_DIR=".red-demo/stateB" \
  RED_ADMIN_TOKEN="$TOKEN_B" RED_NODE_NAME="node-B" ./red &
PID_B=$!

cleanup() { echo; echo "==> Stopping nodes"; kill "$PID_A" "$PID_B" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

cat <<EOF

  ┌─────────────────────────────────────────────────────────────┐
  │  Two-node RED federation is up.                             │
  │    Node A: http://localhost:8080   admin token: $TOKEN_A
  │    Node B: http://localhost:8081   admin token: $TOKEN_B
  │                                                             │
  │  Try it:                                                    │
  │   1. On A: /-/admin → Import a RED-Feather vault (git URL). │
  │      It files under data/<vault_type>/<branch chain>/.      │
  │   2. On B: /-/admin → Peers → add  http://localhost:8080    │
  │   3. On B: Import → peer sync path "library"  (or a branch).│
  │      B reproduces the same branch→leaf tree.               │
  │   4. To verify content: copy the note's branch_author key   │
  │      into B's /-/admin → Contributors → Add.                │
  │                                                             │
  │  See BETA_TESTING.md.  Ctrl-C to stop.                      │
  └─────────────────────────────────────────────────────────────┘

EOF

wait
