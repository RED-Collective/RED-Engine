#!/usr/bin/env bash
# RED Engine — unified setup, install, test, and management script.
#
# Usage: ./setup.sh [command]
#
# Commands:
#   (default)   Interactive first-time setup wizard, or show status if installed
#   test        Run Go test suite
#   dev         Start development server with live reload
#   install     Build container image and start production node
#   update      Rebuild image and restart running node (preserves data)
#   restart     Restart the running node (no rebuild)
#   token       Rotate the admin token
#   backup      Back up the data directory
#   status      Show container status and node health
#   help        Show this message

set -euo pipefail

# ─── colours ────────────────────────────────────────────────────────────────
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()    { echo -e "${CYAN}[*]${NC} $*"; }
success() { echo -e "${GREEN}[✓]${NC} $*"; }
warn()    { echo -e "${YELLOW}[!]${NC} $*"; }
die()     { echo -e "${RED}[✗]${NC} $*" >&2; exit 1; }
header()  { echo -e "\n${BOLD}${CYAN}$*${NC}"; }

# ─── env / compose detection ────────────────────────────────────────────────
ENV_FILE=".env"
CONFIG_FILE="config.json"

compose_cmd() {
    if command -v podman-compose &>/dev/null; then
        echo "podman-compose"
    elif command -v docker-compose &>/dev/null; then
        echo "docker-compose"
    elif command -v docker &>/dev/null && docker compose version &>/dev/null 2>&1; then
        echo "docker compose"
    else
        die "No container engine found. Install Podman or Docker to continue."
    fi
}

require_go() {
    command -v go &>/dev/null || die "Go is not installed. Please install Go first."
}

data_dir() {
    # Resolve the configured data directory from .env, then config.json, then default.
    local d
    d=$(env_get RED_DATA_DIR 2>/dev/null)
    if [ -z "$d" ] && [ -f "$CONFIG_FILE" ]; then
        d=$(grep '"dataDir"' "$CONFIG_FILE" 2>/dev/null | sed -E 's/.*"dataDir"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')
    fi
    echo "${d:-data}"
}

token_gen() {
    # Temporarily disable pipefail: tr exits with SIGPIPE when head closes the
    # read end early, which would kill the script under set -o pipefail.
    set +o pipefail
    LC_ALL=C tr -dc 'a-zA-Z0-9' < /dev/urandom | head -c 32
    echo
    set -o pipefail
}

env_get() {
    # Read a variable from .env without sourcing the file
    # Returns empty string (exit 0) when key is absent — grep returning 1 must not kill the caller.
    local key="$1"
    grep -E "^${key}=" "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2- || true
}

env_set() {
    # Write or replace a key in .env
    local key="$1" val="$2"
    if grep -qE "^${key}=" "$ENV_FILE" 2>/dev/null; then
        sed -i.bak -E "s|^${key}=.*|${key}=${val}|" "$ENV_FILE"
        rm -f "${ENV_FILE}.bak"
    else
        echo "${key}=${val}" >> "$ENV_FILE"
    fi
}

# ─── ensure .env exists and is gitignored ───────────────────────────────────
init_env_file() {
    if [ ! -f "$ENV_FILE" ]; then
        touch "$ENV_FILE"
        info "Created $ENV_FILE"
    fi
    # Add .env to .gitignore if not already there
    if [ -f ".gitignore" ] && ! grep -qxF ".env" ".gitignore"; then
        echo ".env" >> .gitignore
        info "Added .env to .gitignore"
    fi
}

# ─── commands ───────────────────────────────────────────────────────────────

cmd_help() {
    grep '^#' "$0" | grep -v '#!/' | sed 's/^# \?//'
}

cmd_test() {
    require_go
    header "Running test suite"
    go test ./... && success "All tests passed."
}

cmd_dev() {
    require_go
    command -v npm &>/dev/null || die "npm not found. Please install Node.js from https://nodejs.org/"

    local config_path="${1:-$CONFIG_FILE}"
    header "Starting development server"

    if ! command -v air &>/dev/null; then
        warn "air not found. Installing..."
        go install github.com/air-verse/air@latest
        export PATH=$PATH:$(go env GOPATH)/bin
    fi

    info "Installing Go dependencies..."
    go mod download

    if [ ! -d "node_modules" ]; then
        info "Installing npm dependencies..."
        npm install --legacy-peer-deps
    fi

    cleanup_dev() {
        echo -e "\n${YELLOW}Shutting down processes...${NC}"
        kill "$VITE_PID" 2>/dev/null || true
        success "Development environment stopped."
    }
    trap cleanup_dev EXIT INT TERM

    info "Starting Vite dev server on :5173 (Vue + Tailwind + HMR)..."
    NODE_OPTIONS='--disable-warning=DEP0205' npx vite &
    VITE_PID=$!

    warn "Open http://localhost:5173 in your browser."
    info "Starting Go server with live reload (DEV_MODE=true, config=${config_path})..."
    if [ -f ".air.dev.toml" ]; then
        DEV_MODE=true air -c .air.dev.toml -- -config="${config_path}"
    else
        warn "No Air config found. Starting with go run..."
        DEV_MODE=true go run ./cmd/red/main.go -config="${config_path}"
    fi
}

cmd_status() {
    header "Node status"
    local compose
    compose=$(compose_cmd)

    info "Container status:"
    $compose ps 2>/dev/null || warn "No containers running."

    # Determine port from .env or config.json
    local port
    port=$(env_get RED_ADDR | sed 's/.*://')
    [ -z "$port" ] && port=$(grep '"addr"' "$CONFIG_FILE" 2>/dev/null | sed -E 's/.*:([0-9]+).*/\1/')
    [ -z "$port" ] && port="8080"

    echo ""
    if curl -sf "http://localhost:${port}/-/health" &>/dev/null; then
        success "Node health: UP  (http://localhost:${port})"
    else
        warn "Node health: NOT REACHABLE on port ${port}"
    fi
}

cmd_backup() {
    header "Backing up data"
    local src
    src=$(data_dir)
    if [ ! -d "$src" ]; then
        die "Data directory '$src' not found."
    fi
    local stamp
    stamp=$(date +%Y%m%d_%H%M%S)
    local dest="./backups/data_${stamp}.tar.gz"
    mkdir -p ./backups
    tar czf "$dest" "$src"
    success "Backup created: $dest"
}

cmd_token() {
    header "Rotate admin token"
    init_env_file

    local current
    current=$(env_get RED_ADMIN_TOKEN)
    if [ -n "$current" ]; then
        echo -e "  Current token: ${YELLOW}${current}${NC}"
    else
        warn "No token currently set in .env"
    fi

    echo ""
    read -rp "Generate a new secure token and apply it? [y/N] " choice
    case "$choice" in
        y|Y)
            local new_token
            new_token=$(token_gen)
            env_set "RED_ADMIN_TOKEN" "$new_token"

            # Also update config.json if it exists and contains adminToken
            if [ -f "$CONFIG_FILE" ] && grep -q '"adminToken"' "$CONFIG_FILE"; then
                sed -i.bak -E "s/(\"adminToken\"[[:space:]]*:[[:space:]]*\")[^\"]*(\")/\1${new_token}\2/" "$CONFIG_FILE"
                rm -f "${CONFIG_FILE}.bak"
                info "Updated token in $CONFIG_FILE"
            fi

            echo ""
            success "New admin token: ${BOLD}${new_token}${NC}"
            warn "Restart the node for the new token to take effect."
            echo "  • Dev server:        ./setup.sh dev"
            echo "  • Docker/Podman:     $(compose_cmd 2>/dev/null || echo 'docker compose') restart red_engine"
            ;;
        *)
            info "Token unchanged."
            ;;
    esac
}

cmd_restart() {
    header "Restarting RED Engine node"
    local compose
    compose=$(compose_cmd)

    info "Restarting container (no rebuild)..."
    $compose restart red_engine || die "Failed to restart container."

    success "Node restarted."
    sleep 2
    cmd_status
}

cmd_update() {
    header "Updating RED Engine node"
    local compose
    compose=$(compose_cmd)

    info "Pulling latest source..."
    git pull || warn "git pull failed — continuing with current code."

    info "Running tests before rebuild..."
    require_go
    go test ./... || die "Tests failed. Aborting update to protect the running node."

    info "Rebuilding container image..."
    $compose build red_engine

    info "Restarting node..."
    $compose up -d red_engine

    success "Update complete."
    cmd_status
}

cmd_install() {
    header "Installing RED Engine"
    local compose
    compose=$(compose_cmd)

    # Ensure data dir exists and is owned by UID 1000 (container user)
    local configured_data_dir
    configured_data_dir=$(data_dir)
    if [ ! -d "$configured_data_dir" ]; then
        mkdir -p "$configured_data_dir"
        info "Created data directory: $configured_data_dir"
    fi
    if [ "$(stat -c '%u' "$configured_data_dir" 2>/dev/null || stat -f '%u' "$configured_data_dir" 2>/dev/null)" != "1000" ]; then
        info "Setting $configured_data_dir ownership to UID 1000 (container user)..."
        sudo chown -R 1000:1000 "$configured_data_dir" || warn "Could not chown $configured_data_dir — you may hit permission errors."
    fi

    # Set up unprivileged port binding on Linux
    if [[ "$OSTYPE" == "linux"* ]]; then
        local conf="/etc/sysctl.d/80-unprivileged-ports.conf"
        if [ ! -f "$conf" ] || ! grep -q "ip_unprivileged_port_start=80" "$conf"; then
            info "Configuring unprivileged port binding (requires sudo)..."
            echo "net.ipv4.ip_unprivileged_port_start=80" | sudo tee "$conf" > /dev/null
            sudo sysctl --system > /dev/null
            success "Ports 80+ now bindable by non-root users."
        fi
    fi

    # Run tests
    if command -v go &>/dev/null; then
        info "Running test suite..."
        go test ./... || die "Tests failed. Fix them before installing."
        success "Tests passed."
    else
        warn "Go not found — skipping tests."
    fi

    info "Building container image..."
    $compose build red_engine

    info "Starting node..."
    $compose up -d

    echo ""
    cmd_status

    # Automated backup prompt
    echo ""
    read -rp "Set up daily automatic backups at 03:00? [Y/n] " resp
    case "${resp:-y}" in
        [yY]*|"")
            local project_path
            project_path=$(pwd)
            local cron_job="0 3 * * * ${project_path}/setup.sh backup >> ${project_path}/backups/cron.log 2>&1"
            if crontab -l 2>/dev/null | grep -qF "${project_path}/setup.sh backup"; then
                info "Backup cron job already configured."
            else
                (crontab -l 2>/dev/null; echo "$cron_job") | crontab -
                success "Backup cron job installed."
            fi
            ;;
        *)
            info "Backups skipped."
            ;;
    esac
}

cmd_setup() {
    # ── Already installed? ──────────────────────────────────────────────────
    if [ -f "$ENV_FILE" ] && [ -n "$(env_get RED_ADMIN_TOKEN)" ]; then
        warn "Node appears to already be configured (.env exists with a token)."
        echo ""
        read -rp "  Run setup wizard anyway and overwrite? [y/N] " overwrite
        [[ "$overwrite" =~ ^[yY]$ ]] || { cmd_status; exit 0; }
    fi

    clear
    echo -e "${BOLD}${CYAN}"
    echo "  ██████╗ ███████╗██████╗     ███████╗███╗   ██╗ ██████╗ ██╗███╗   ██╗███████╗"
    echo "  ██╔══██╗██╔════╝██╔══██╗    ██╔════╝████╗  ██║██╔════╝ ██║████╗  ██║██╔════╝"
    echo "  ██████╔╝█████╗  ██║  ██║    █████╗  ██╔██╗ ██║██║  ███╗██║██╔██╗ ██║█████╗  "
    echo "  ██╔══██╗██╔══╝  ██║  ██║    ██╔══╝  ██║╚██╗██║██║   ██║██║██║╚██╗██║██╔══╝  "
    echo "  ██║  ██║███████╗██████╔╝    ███████╗██║ ╚████║╚██████╔╝██║██║ ╚████║███████╗"
    echo "  ╚═╝  ╚═╝╚══════╝╚═════╝     ╚══════╝╚═╝  ╚═══╝ ╚═════╝ ╚═╝╚═╝  ╚═══╝╚══════╝"
    echo -e "${NC}"
    echo -e "  ${BOLD}First-Time Node Setup Wizard${NC}"
    echo ""
    echo "  This wizard configures your RED Engine node. It will:"
    echo "    • Generate a secure admin token"
    echo "    • Set your site name and node identity"
    echo "    • Write credentials to .env (survives config.json loss)"
    echo "    • Write a minimal config.json (addr, dataDir, token, webhook)"
    echo "    • Build and start the container"
    echo ""
    read -rp "  Press Enter to continue, or Ctrl+C to abort..."

    init_env_file

    # ── 1. Listen address & data directory ─────────────────────────────────
    header "1/5 — Network & Data"
    echo ""
    read -rp "  Listen address [default: :8080]: " addr
    addr="${addr:-:8080}"

    read -rp "  Data directory [default: data]: " datadir
    datadir="${datadir:-data}"

    # ── 2. Site name ────────────────────────────────────────────────────────
    header "2/5 — Site Name"
    echo ""
    echo "  The site name appears in the browser tab, the home page heading,"
    echo "  and the node identity endpoint. You can change this freely at any"
    echo "  time via the admin panel — it has no federation implications."
    echo ""
    read -rp "  Site name [default: RED Engine]: " site_name
    site_name="${site_name:-RED Engine}"

    # ── 3. Node name — PERMANENCE WARNING ───────────────────────────────────
    header "3/5 — Node Identity"
    echo ""
    echo -e "${BOLD}${RED}"
    echo "  ╔══════════════════════════════════════════════════════════════════╗"
    echo "  ║             ⚠  NODE NAME — PLEASE READ CAREFULLY  ⚠             ║"
    echo "  ╠══════════════════════════════════════════════════════════════════╣"
    echo "  ║                                                                  ║"
    echo "  ║  The node name is your identity in the RED federation network.  ║"
    echo "  ║                                                                  ║"
    echo "  ║  When other nodes add you as a peer or mirror, they register    ║"
    echo "  ║  you by this name. If you change it after peers have connected: ║"
    echo "  ║                                                                  ║"
    echo "  ║    • Existing peers will no longer recognise your node          ║"
    echo "  ║    • Sync connections will break and need manual repair         ║"
    echo "  ║    • Mirror relationships will be invalidated                   ║"
    echo "  ║                                                                  ║"
    echo "  ║  You CAN change it later via the admin panel, but it is        ║"
    echo "  ║  NOT recommended once you have active peers or mirrors.         ║"
    echo "  ║                                                                  ║"
    echo "  ╚══════════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"

    read -rp "  Type 'I understand' to continue: " ack
    if [ "$ack" != "I understand" ]; then
        warn "Acknowledgement not received. Exiting setup."
        exit 1
    fi

    echo ""
    local default_node
    default_node=$(hostname 2>/dev/null || echo "red-node")
    read -rp "  Node name [default: ${default_node}]: " node_name
    node_name="${node_name:-$default_node}"

    # ── 4. Security credentials ─────────────────────────────────────────────
    header "4/5 — Security Credentials"
    echo ""
    local admin_token
    admin_token=$(token_gen)
    echo "  A secure 32-character admin token has been generated for you."
    echo "  You can provide your own or press Enter to use the generated one."
    echo ""
    echo -e "  Generated token: ${YELLOW}${admin_token}${NC}"
    echo ""
    read -rp "  Admin token [press Enter to use generated]: " custom_token
    [ -n "$custom_token" ] && admin_token="$custom_token"

    echo ""
    echo "  Webhook secret is used to authenticate GitHub push webhooks."
    echo "  Leave blank to disable webhook sync."
    read -rp "  Webhook secret [optional, Enter to skip]: " webhook_secret

    # ── 5. Write config files ────────────────────────────────────────────────
    header "5/5 — Writing configuration"

    # .env — the resilient credential store (survives config.json deletion)
    cat > "$ENV_FILE" <<EOF
# RED Engine node configuration
# This file is the primary credential store. Keep it safe.
# If config.json is deleted, these values keep the node running.
RED_ADDR=${addr}
RED_DATA_DIR=${datadir}
RED_ADMIN_TOKEN=${admin_token}
RED_WEBHOOK_SECRET=${webhook_secret}
RED_SITE_NAME=${site_name}
RED_NODE_NAME=${node_name}
EOF
    success "Written .env"

    # Ensure .env is gitignored
    if [ -f ".gitignore" ] && ! grep -qxF ".env" ".gitignore"; then
        echo ".env" >> .gitignore
        info "Added .env to .gitignore"
    fi

    # config.json — minimal bootstrap file (credentials backed up by .env)
    cat > "$CONFIG_FILE" <<EOF
{
  "addr": "${addr}",
  "dataDir": "${datadir}",
  "adminToken": "${admin_token}",
  "webhookSecret": "${webhook_secret}"
}
EOF
    success "Written config.json"

    # ── Summary ──────────────────────────────────────────────────────────────
    echo ""
    echo -e "${BOLD}${GREEN}╔══════════════════════════════════════════════════════╗${NC}"
    echo -e "${BOLD}${GREEN}║              Setup complete — save this info         ║${NC}"
    echo -e "${BOLD}${GREEN}╠══════════════════════════════════════════════════════╣${NC}"
    echo -e "${BOLD}${GREEN}║${NC}  Site name   : ${site_name}"
    echo -e "${BOLD}${GREEN}║${NC}  Node name   : ${BOLD}${node_name}${NC}  ⚠  (treat as permanent)"
    echo -e "${BOLD}${GREEN}║${NC}  Listen addr : ${addr}"
    echo -e "${BOLD}${GREEN}║${NC}  Admin token : ${YELLOW}${admin_token}${NC}"
    [ -n "$webhook_secret" ] && \
    echo -e "${BOLD}${GREEN}║${NC}  Webhook sec : ${YELLOW}${webhook_secret}${NC}"
    echo -e "${BOLD}${GREEN}╚══════════════════════════════════════════════════════╝${NC}"
    echo ""
    warn "Save your admin token now — it will not be shown again."
    echo ""

    read -rp "  Proceed to build and start the node? [Y/n] " start
    case "${start:-y}" in
        [nN]*) info "Setup done. Run './setup.sh install' when ready."; exit 0 ;;
    esac

    cmd_install
}

# ─── dispatch ────────────────────────────────────────────────────────────────
case "${1:-}" in
    test)    cmd_test    ;;
    dev)     cmd_dev     "${2:-}" ;;
    install) cmd_install ;;
    update)  cmd_update  ;;
    restart) cmd_restart ;;
    token)   cmd_token   ;;
    backup)  cmd_backup  ;;
    status)  cmd_status  ;;
    help|--help|-h) cmd_help ;;
    "")      cmd_setup   ;;
    *)       die "Unknown command: $1. Run './setup.sh help' for usage." ;;
esac
