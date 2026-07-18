#!/usr/bin/env bash
# setup.sh — RED Engine unified setup entry point
#
# Usage:
#   ./setup.sh              Interactive first-time wizard
#   ./setup.sh test         Run test suite
#   ./setup.sh dev          Start dev server with live reload
#   ./setup.sh install      Install dependencies + build
#   ./setup.sh token        Rotate admin token
#   ./setup.sh build        Build Go binary + frontend
#   ./setup.sh status       Show container status
#
# This script delegates to red-engine.sh for most commands.
# See INSTALL.md for full setup instructions.

set -euo pipefail
cd "$(dirname "$0")"

cmd="${1:-help}"

case "$cmd" in
  test)
    echo "==> Running test suite..."
    go test ./...
    ;;
  dev)
    echo "==> Starting development environment..."
    exec ./red-dev.sh
    ;;
  install)
    echo "==> Installing npm dependencies..."
    cd internal/router/red-engine-frontend && npm install && cd ../..
    echo "==> Building frontend..."
    cd internal/router/red-engine-frontend && npm run build && cd ../..
    echo "==> Building Go binary..."
    go build -ldflags "-X github.com/RED-Collective/red-engine/internal/router.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o red ./cmd/red
    echo "==> Done. Run ./red to start, or use Podman: ./red-engine.sh build && ./red-engine.sh start"
    ;;
  build)
    echo "==> Building frontend..."
    cd internal/router/red-engine-frontend && npm run build && cd ../..
    echo "==> Building Go binary..."
    go build -ldflags "-X github.com/RED-Collective/red-engine/internal/router.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o red ./cmd/red
    echo "==> Build complete: ./red"
    ;;
  token)
    if [ -f .env ]; then
      echo "Your admin token is:"
      grep RED_ADMIN_TOKEN .env | head -1
    elif [ -f config.json ]; then
      python3 -c "import json; print(json.load(open('config.json')).get('adminToken',''))" 2>/dev/null || true
    else
      echo "No config.json or .env found."
    fi
    ;;
  status)
    if command -v podman &>/dev/null; then
      podman ps --filter name=red_engine --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
    elif command -v docker &>/dev/null; then
      docker ps --filter name=red_engine --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
    else
      echo "Neither Podman nor Docker found."
    fi
    ;;
  help|"")
    cat << 'EOF'
RED Engine — Setup

  ./setup.sh              Interactive first-time wizard (via red-engine.sh)
  ./setup.sh test         Run Go test suite
  ./setup.sh dev          Start dev server (Vite + Go live reload)
  ./setup.sh install      Install dependencies + build binary + frontend
  ./setup.sh build        Build Go binary + frontend
  ./setup.sh token        Show/admin token
  ./setup.sh status       Show container status

For full setup instructions, see INSTALL.md.
EOF
    ;;
  *)
    echo "Unknown command: $cmd"
    echo "Run ./setup.sh help for usage."
    exit 1
    ;;
esac
