#!/usr/bin/env bash
# red-dev.sh — Start RED Engine development environment.
#
# Starts both the Go backend (with live reload via air) and the
# Vite frontend dev server (with HMR) in parallel.
#
# Prerequisites: Go, Node.js, npm
#
# Usage:
#   ./red-dev.sh

set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"

command -v go  >/dev/null 2>&1 || { echo "Go is required. Install from https://golang.org/"; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "npm is required. Install from https://nodejs.org/"; exit 1; }

# Install air if missing
if ! command -v air >/dev/null 2>&1; then
  echo "air not found — installing..."
  go install github.com/air-verse/air@latest
  export PATH="$PATH:$(go env GOPATH)/bin"
fi

# Install frontend deps if missing
if [ ! -d "$ROOT/internal/router/red-engine-frontend/node_modules" ]; then
  echo "Installing frontend dependencies..."
  (cd "$ROOT/internal/router/red-engine-frontend" && npm install)
fi

# Start Vite dev server in background
export NODE_OPTIONS="--disable-warning=DEP0205"
echo "Starting Vite dev server on :5173..."
(cd "$ROOT/internal/router/red-engine-frontend" && npx vite) &
VITE_PID=$!

echo "Starting Go backend with live reload..."
export DEV_MODE=true

cleanup() {
  kill "$VITE_PID" 2>/dev/null || true
  echo "Development environment stopped."
}
trap cleanup EXIT

cd "$ROOT"
if [ -f ".air.dev.toml" ]; then
  air -c .air.dev.toml -- "-config=config.json"
else
  go run ./cmd/red/main.go "-config=config.json"
fi
