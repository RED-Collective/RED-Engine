#!/bin/bash
# red-dev – RED Engine development launcher (Linux/macOS)

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}🚀 Starting RED Engine development environment...${NC}"

# --- Check prerequisites ---
command -v go >/dev/null 2>&1 || { echo -e "${RED}❌ Go not found. Please install Go.${NC}"; exit 1; }
command -v npm >/dev/null 2>&1 || { echo -e "${RED}❌ npm not found. Please install Node.js.${NC}"; exit 1; }

if ! command -v air &>/dev/null; then
    echo -e "${YELLOW}⚠️  air not found. Installing...${NC}"
    go install github.com/air-verse/air@latest
    # Ensure ~/go/bin is in PATH
    export PATH=$PATH:$(go env GOPATH)/bin
fi

# --- Install dependencies (if needed) ---
echo -e "${GREEN}📦 Installing Go dependencies...${NC}"
go mod download

if [ ! -d "node_modules" ]; then
    echo -e "${GREEN}📦 Installing npm dependencies...${NC}"
    npm install
fi

# --- Setup cleanup on exit ---
cleanup() {
    echo -e "\n${YELLOW}🛑 Shutting down processes...${NC}"
    kill $TAILWIND_PID $AIR_PID 2>/dev/null
    wait $TAILWIND_PID $AIR_PID 2>/dev/null
    echo -e "${GREEN}✅ Development environment stopped.${NC}"
    exit
}
trap cleanup INT TERM

# --- Start Tailwind watcher ---
echo -e "${GREEN}🎨 Starting Tailwind CSS watcher...${NC}"
npm run watch:tailwind &
TAILWIND_PID=$!

# --- Start Air with DEV_MODE=true ---
echo -e "${GREEN}🏃 Starting Go server with live reload (DEV_MODE=true)...${NC}"
DEV_MODE=true air &
AIR_PID=$!

echo -e "${GREEN}✅ Both processes running. Press Ctrl+C to stop.${NC}"

# Wait for either process to exit (normally they run forever)
wait