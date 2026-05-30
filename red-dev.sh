#!/bin/bash
# red-dev – RED Engine development launcher with IP display

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# --- Function to get local IP address ---
get_local_ip() {
    # Try to find the interface that has a default route and is not loopback
    # Works on Linux, macOS, and WSL
    ip -4 route get 1 2>/dev/null | awk '{print $7; exit}' 2>/dev/null || \
    ifconfig | grep -E "inet (10\.|172\.1[6-9]|172\.2[0-9]|172\.3[0-1]|192\.168\.)" | awk '{print $2}' | head -1
}

echo -e "${GREEN}🚀 Starting RED Engine development environment...${NC}"

# --- Check prerequisites ---
command -v go >/dev/null 2>&1 || { echo -e "${RED}❌ Go not found. Please install Go.${NC}"; exit 1; }
command -v npm >/dev/null 2>&1 || { echo -e "${RED}❌ npm not found. Please install Node.js.${NC}"; exit 1; }

if ! command -v air &>/dev/null; then
    echo -e "${YELLOW}⚠️  air not found. Installing...${NC}"
    go install github.com/air-verse/air@latest
    export PATH=$PATH:$(go env GOPATH)/bin
fi

# --- Install dependencies ---
echo -e "${GREEN}📦 Installing Go dependencies...${NC}"
go mod download

if [ ! -d "node_modules" ]; then
    echo -e "${GREEN}📦 Installing npm dependencies...${NC}"
    npm install
fi

# --- Setup cleanup ---
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

# --- Wait for server to be ready (polling) ---
echo -n "Waiting for server to start"
for i in {1..10}; do
    if curl -s -o /dev/null http://localhost:8080/; then
        echo -e " ${GREEN}✓${NC}"
        break
    fi
    echo -n "."
    sleep 0.5
done

# --- Display URLs ---
LOCAL_IP=$(get_local_ip)
echo -e "\n${GREEN}✅ RED Engine is running!${NC}"
echo -e "   📡 Local:    ${YELLOW}http://localhost:8080${NC}"
if [ -n "$LOCAL_IP" ]; then
    echo -e "   🌐 Network:  ${YELLOW}http://${LOCAL_IP}:8080${NC}"
else
    echo -e "   ⚠️  Could not detect local IP. Use 'ifconfig' or 'ip addr' to find your LAN address."
fi
echo -e "${GREEN}   Press Ctrl+C to stop.${NC}\n"

# Wait for processes (they run forever)
wait