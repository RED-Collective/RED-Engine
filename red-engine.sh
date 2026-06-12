#!/usr/bin/env bash
# red-engine.sh — RED Engine unified node management (Podman) — Linux / macOS
#
# Usage: ./red-engine.sh <command> [-p PORT] [-d DIR] [options]
#
# SETUP & BUILD
#   setup               Interactive first-time wizard for the node on -p PORT
#   build               Build (or rebuild) the red-engine:latest container image
#
# NODE LIFECYCLE
#   start [--tunnel]    Start the node; --tunnel also opens a cloudflared tunnel
#   stop  [--wipe-data] Stop the node (container + tunnel); --wipe-data removes data dir
#   restart             Restart without rebuilding the image
#   logs  [--follow]    Show last 100 lines; --follow to stream live
#   status              Table of all active red_engine_* containers + health
#
# TUNNEL
#   tunnel              Start a cloudflared quick tunnel for this node's port
#                       (auto-pushes public_url to the node's settings API)
#
# MAINTENANCE
#   token               Rotate the admin token for this node
#   backup              Tar-gz this node's data directory into backups/
#   update              git pull -> go test -> rebuild image -> restart
#
# DEVELOPMENT
#   test                Run Go unit tests (go test ./...)
#   dev                 Vite dev server + Go live reload (no container)
#   help                Show this message
#
# FLAGS
#   -p, --port <n>      Port of the node to operate on  [default: 8080]
#   -d, --dir  <name>   Data directory name inside the node dir  [default: data]
#   --node-name <s>     Override node name  (setup / start)
#   --token <s>         Override admin token  (start)
#   --tunnel            Also start a cloudflared tunnel  (start)
#   --follow            Stream logs  (logs)
#   --wipe-data         Also delete the data directory  (stop)
#
# MULTI-NODE EXAMPLE — 3 independent nodes for federation testing
#   ./red-engine.sh setup   -p 8080 -d vault-a
#   ./red-engine.sh setup   -p 8081 -d vault-b
#   ./red-engine.sh setup   -p 8082 -d vault-c
#   ./red-engine.sh build
#   ./red-engine.sh start   -p 8080 -d vault-a --tunnel
#   ./red-engine.sh start   -p 8081 -d vault-b --tunnel
#   ./red-engine.sh start   -p 8082 -d vault-c --tunnel
#   ./red-engine.sh status

set -euo pipefail

# ── Colours ───────────────────────────────────────────────────────────────────
if [[ -t 1 ]]; then
    CY='\033[0;36m'
    GN='\033[0;32m'
    YL='\033[0;33m'
    RD='\033[0;31m'
    MG='\033[0;35m'
    DG='\033[0;90m'
    NC='\033[0m'
else
    CY='' GN='' YL='' RD='' MG='' DG='' NC=''
fi

info() { echo -e "${CY}[*] $*${NC}"; }
ok()   { echo -e "${GN}[+] $*${NC}"; }
warn() { echo -e "${YL}[!] $*${NC}"; }
fail() { echo -e "${RD}[x] $*${NC}"; }
head_() { echo -e "\n${CY}==> $*${NC}"; }
die()  { fail "$*"; exit 1; }

# ── Platform ──────────────────────────────────────────────────────────────────
case "$(uname -s)" in
    Linux*)  IS_LINUX=true;  IS_MACOS=false ;;
    Darwin*) IS_LINUX=false; IS_MACOS=true  ;;
    *)       IS_LINUX=false; IS_MACOS=false ;;
esac

# ── Paths ─────────────────────────────────────────────────────────────────────
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE_NAME="localhost/red-engine:latest"
NODES_DIR="$ROOT/.red-nodes"

# ── Argument parsing ──────────────────────────────────────────────────────────
COMMAND=""
PORT=8080
DIR_NAME="data"
NODE_NAME=""
TOKEN_ARG=""
TUNNEL=false
FOLLOW=false
WIPE_DATA=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -p|--port)
            [[ $# -ge 2 ]] || die "--port requires a value"
            PORT="$2"; shift 2 ;;
        -d|--dir)
            [[ $# -ge 2 ]] || die "--dir requires a value"
            DIR_NAME="$2"; shift 2 ;;
        --node-name)
            [[ $# -ge 2 ]] || die "--node-name requires a value"
            NODE_NAME="$2"; shift 2 ;;
        --token)
            [[ $# -ge 2 ]] || die "--token requires a value"
            TOKEN_ARG="$2"; shift 2 ;;
        --tunnel)    TUNNEL=true;    shift ;;
        --follow)    FOLLOW=true;    shift ;;
        --wipe-data) WIPE_DATA=true; shift ;;
        -*)
            die "Unknown flag: $1. Run: ./red-engine.sh help" ;;
        *)
            [[ -z "$COMMAND" ]] && COMMAND="$1"
            shift ;;
    esac
done

# ── Per-port path helpers ─────────────────────────────────────────────────────
# Port 8080 keeps files at the repo root for backwards compatibility.
# Every other port lives under .red-nodes/<port>/.

node_dir() {
    if [[ "$PORT" -eq 8080 ]]; then echo "$ROOT"
    else echo "$NODES_DIR/$PORT"
    fi
}

data_dir() {
    echo "$(node_dir)/$DIR_NAME"
}

state_dir() {
    echo "$(node_dir)/state"
}

env_file() {
    if [[ "$PORT" -eq 8080 ]]; then echo "$ROOT/.env"
    else echo "$NODES_DIR/$PORT/.env"
    fi
}

container_name()  { echo "red_engine_$PORT"; }
tunnel_log()      { echo "$(node_dir)/cloudflared.log"; }
tunnel_pid_file() { echo "$(node_dir)/cloudflared.pid"; }
tunnel_url_file() { echo "$(node_dir)/tunnel-url"; }

# ── .env helpers ──────────────────────────────────────────────────────────────
read_env_key() {
    local key="$1" file="$2"
    [[ -f "$file" ]] || { echo ""; return; }
    grep -m1 "^${key}=" "$file" 2>/dev/null | sed "s/^${key}=//" || echo ""
}

write_env_key() {
    local key="$1" value="$2" file="$3"
    mkdir -p "$(dirname "$file")"
    touch "$file"
    if grep -q "^${key}=" "$file" 2>/dev/null; then
        # sed -i differs between GNU (Linux) and BSD (macOS)
        if $IS_MACOS; then
            sed -i '' "s|^${key}=.*|${key}=${value}|" "$file"
        else
            sed -i "s|^${key}=.*|${key}=${value}|" "$file"
        fi
    else
        printf '\n%s=%s\n' "$key" "$value" >> "$file"
    fi
}

gen_token() {
    # head exits after 48 bytes, sending SIGPIPE to tr; || true silences pipefail
    LC_ALL=C tr -dc 'a-zA-Z0-9' < /dev/urandom 2>/dev/null | head -c 48 || true
}

# ── Prerequisite checks ───────────────────────────────────────────────────────
assert_tool() {
    local name="$1" hint="${2:-}"
    command -v "$name" >/dev/null 2>&1 && return
    local msg="$name not found."
    [[ -n "$hint" ]] && msg+=" $hint"
    die "$msg"
}

# ── Container helpers ─────────────────────────────────────────────────────────
container_status() {
    local name="$1"
    podman inspect --format '{{.State.Status}}' "$name" 2>/dev/null | tr -d '[:space:]' || true
}

wait_node_health() {
    local port="$1" timeout="${2:-30}"
    local i
    for ((i=0; i<timeout; i++)); do
        curl -sf --max-time 2 "http://localhost:${port}/-/health" >/dev/null 2>&1 && return 0
        sleep 1
    done
    return 1
}

# ── Tunnel helpers ────────────────────────────────────────────────────────────
stop_tunnel() {
    local port="${1:-$PORT}"
    local nd
    if [[ "$port" -eq 8080 ]]; then nd="$ROOT"
    else nd="$NODES_DIR/$port"
    fi

    local pid_file="$nd/cloudflared.pid"
    local url_file="$nd/tunnel-url"

    if [[ -f "$pid_file" ]]; then
        local pid; pid=$(tr -d '[:space:]' < "$pid_file")
        if [[ "$pid" =~ ^[0-9]+$ ]]; then
            kill "$pid" 2>/dev/null || true
            info "Stopped cloudflared (pid $pid) for port $port"
        fi
        rm -f "$pid_file" "$url_file"
    fi
}

start_tunnel() {
    assert_tool cloudflared \
        "Install from https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/"

    local log_file; log_file="$(tunnel_log)"
    local pid_file; pid_file="$(tunnel_pid_file)"
    local url_file; url_file="$(tunnel_url_file)"
    local nd;       nd="$(node_dir)"

    mkdir -p "$nd"

    # Kill any stale tunnel for this port
    if [[ -f "$pid_file" ]]; then
        local old_pid; old_pid=$(tr -d '[:space:]' < "$pid_file")
        [[ "$old_pid" =~ ^[0-9]+$ ]] && kill "$old_pid" 2>/dev/null || true
        rm -f "$pid_file" "$url_file"
    fi

    local max_attempts=4
    local url=""
    local attempt
    for ((attempt=1; attempt<=max_attempts; attempt++)); do
        : > "$log_file"

        cloudflared tunnel --no-autoupdate --url "http://localhost:$PORT" \
            >"$log_file" 2>&1 &
        local pid=$!
        echo "$pid" > "$pid_file"

        # Wait up to 50s for the URL to appear in the log
        local i
        for ((i=0; i<50; i++)); do
            sleep 1
            if [[ -f "$log_file" ]]; then
                url=$(grep -oE 'https://[a-z0-9]+(-[a-z0-9]+)+\.trycloudflare\.com' \
                        "$log_file" 2>/dev/null | head -1 || true)
                [[ -n "$url" ]] && break
            fi
        done

        if [[ -n "$url" ]]; then
            echo "$url" > "$url_file"
            echo "$url"
            return 0
        fi

        # Failed — kill and retry with backoff
        kill "$pid" 2>/dev/null || true
        rm -f "$pid_file"

        if grep -q "failed to request quick Tunnel" "$log_file" 2>/dev/null; then
            warn "Cloudflare rate-limited quick tunnel (attempt $attempt/$max_attempts) — retrying in $((attempt * 10))s..."
        else
            warn "Tunnel produced no URL (attempt $attempt/$max_attempts) — retrying in $((attempt * 10))s..."
        fi

        [[ $attempt -lt $max_attempts ]] && sleep $((attempt * 10))
    done

    die "cloudflared did not produce a URL after $max_attempts attempts. Check: $log_file"
}

# ── Commands ──────────────────────────────────────────────────────────────────

cmd_build() {
    assert_tool podman "Install from https://podman.io/"
    head_ "Building container image: $IMAGE_NAME"
    podman build --network=host -t "$IMAGE_NAME" "$ROOT"
    ok "Image ready: $IMAGE_NAME"
}

cmd_setup() {
    local env_f; env_f="$(env_file)"
    local nd;    nd="$(node_dir)"
    local dd;    dd="$(data_dir)"
    local sd;    sd="$(state_dir)"

    if [[ -f "$env_f" ]] && [[ -n "$(read_env_key RED_ADMIN_TOKEN "$env_f")" ]]; then
        warn "Node on port $PORT is already configured ($env_f)."
        read -rp "  Re-run setup wizard and overwrite? [y/N]: " ow
        if [[ ! "$ow" =~ ^[yY]$ ]]; then cmd_status; exit 0; fi
    fi

    clear
    echo -e "${CY}"
    cat << 'BANNER'
  ██████╗ ███████╗██████╗     ███████╗███╗   ██╗ ██████╗ ██╗███╗   ██╗███████╗
  ██╔══██╗██╔════╝██╔══██╗    ██╔════╝████╗  ██║██╔════╝ ██║████╗  ██║██╔════╝
  ██████╔╝█████╗  ██║  ██║    █████╗  ██╔██╗ ██║██║  ███╗██║██╔██╗ ██║█████╗
  ██╔══██╗██╔══╝  ██║  ██║    ██╔══╝  ██║╚██╗██║██║   ██║██║██║╚██╗██║██╔══╝
  ██║  ██║███████╗██████╔╝    ███████╗██║ ╚████║╚██████╔╝██║██║ ╚████║███████╗
  ╚═╝  ╚═╝╚══════╝╚═════╝     ╚══════╝╚═╝  ╚═══╝ ╚═════╝ ╚═╝╚═╝  ╚═══╝╚══════╝
BANNER
    echo -e "  Node Setup  (port $PORT, data dir: ${YL}${DIR_NAME}${CY})${NC}"

    # ── Node name ──────────────────────────────────────────────────────────────
    echo
    echo -e "  ${CY}Node Identity${NC}"
    echo -e "  ${DG}──────────────────────────────────────────────────────────${NC}"
    echo -e "  ${YL}The node name is your permanent identity in the RED network.${NC}"
    echo -e "  ${YL}Changing it after peers have connected will break those connections.${NC}"
    echo
    local default_name
    if [[ -n "$NODE_NAME" ]]; then
        default_name="$NODE_NAME"
    else
        default_name="$(hostname 2>/dev/null || echo "red-node-$PORT")"
    fi
    read -rp "  Node name [default: $default_name]: " chosen_name
    chosen_name="${chosen_name:-$default_name}"
    read -rp "  Type 'I understand' to confirm the node name is permanent: " ack
    [[ "$ack" == "I understand" ]] || die "Acknowledgement required. Aborting."

    # ── Admin token ────────────────────────────────────────────────────────────
    echo
    echo -e "  ${CY}Admin Token${NC}"
    echo -e "  ${DG}──────────────────────────────────────────────────────────${NC}"
    local generated; generated="$(gen_token)"
    echo -e "  Generated: ${YL}${generated}${NC}"
    read -rp "  Press Enter to accept, or type a custom token: " custom_token
    local admin_token="${custom_token:-$generated}"

    # ── Optional ───────────────────────────────────────────────────────────────
    echo
    echo -e "  ${CY}Optional${NC}"
    echo -e "  ${DG}──────────────────────────────────────────────────────────${NC}"
    read -rp "  Node description [optional, shown publicly]: " description
    read -rp "  Webhook secret   [optional, for GitHub push sync]: " webhook

    # ── Write .env ─────────────────────────────────────────────────────────────
    mkdir -p "$nd" "$dd" "$sd"

    local stamp; stamp="$(date '+%Y-%m-%d %H:%M:%S')"
    cat > "$env_f" << EOF
# RED Engine node configuration — port $PORT
# Generated: $stamp
RED_NODE_NAME=$chosen_name
RED_ADMIN_TOKEN=$admin_token
RED_NODE_DESCRIPTION=$description
RED_WEBHOOK_SECRET=$webhook
EOF

    # Keep .env and .red-nodes/ out of git for the default node
    if [[ "$PORT" -eq 8080 ]]; then
        local gi="$ROOT/.gitignore"
        if [[ -f "$gi" ]]; then
            grep -qE '^\.env$'      "$gi" || echo ".env"        >> "$gi"
            grep -qE '^\.red-nodes' "$gi" || echo ".red-nodes/" >> "$gi"
        fi
    fi

    echo
    echo -e "${GN}+===============================================================+${NC}"
    echo -e "${GN}|  Setup complete — save these credentials                      |${NC}"
    echo -e "${GN}+===============================================================+${NC}"
    echo    "  Port        : $PORT"
    echo    "  Data dir    : $DIR_NAME"
    echo -e "  Node name   : ${YL}${chosen_name}${NC}"
    echo -e "  Admin token : ${YL}${admin_token}${NC}"
    [[ -n "$webhook" ]] && echo -e "  Webhook sec : ${YL}${webhook}${NC}"
    echo -e "${GN}+===============================================================+${NC}"
    echo
    warn "To view/rotate your Admin token, run: ./red-engine.sh env -p $PORT token."
    echo

    read -rp "  Build container image now? [Y/n]: " do_build
    [[ "$do_build" =~ ^[nN]$ ]] || cmd_build

    local started=false
    read -rp "  Start the node on port $PORT? [Y/n]: " do_start
    if [[ ! "$do_start" =~ ^[nN]$ ]]; then
        cmd_start
        started=true
    fi

    # Final summary — always printed so the token is visible even after build/start output
    echo
    echo -e "${GN}+===============================================================+${NC}"
    echo -e "${GN}|  Setup complete                                               |${NC}"
    echo -e "${GN}+===============================================================+${NC}"
    echo -e "  Admin token : ${YL}${admin_token}${NC}"
    echo -e "  Stored in   : ${DG}$(env_file)${NC}"
    echo -e "${GN}+---------------------------------------------------------------+${NC}"
    if $started; then
        echo -e "  Node running  : ${CY}http://localhost:${PORT}${NC}"
        echo -e "  View status   : ${DG}./red-engine.sh status${NC}"
        echo -e "  Add tunnel    : ${DG}./red-engine.sh tunnel -p ${PORT}${NC}"
    else
        echo -e "  To start your node run:"
        echo -e "  ${CY}./red-engine.sh start -p ${PORT} -d ${DIR_NAME}${NC}"
        echo -e "  ${CY}./red-engine.sh start -p ${PORT} -d ${DIR_NAME} --tunnel${NC}  (+ cloudflared)"
    fi
    echo -e "${GN}+===============================================================+${NC}"
    echo
}

cmd_start() {
    assert_tool podman "Install from https://podman.io/"

    local env_f; env_f="$(env_file)"
    local dd;    dd="$(data_dir)"
    local sd;    sd="$(state_dir)"
    local cntr;  cntr="$(container_name)"

    [[ -f "$env_f" ]] || die "No .env found for port $PORT. Run: ./red-engine.sh setup -p $PORT"

    podman image exists "$IMAGE_NAME" 2>/dev/null \
        || die "Image $IMAGE_NAME not found. Run first: ./red-engine.sh build"

    mkdir -p "$dd" "$sd"

    # Remove any stale container
    local existing; existing="$(container_status "$cntr")"
    if [[ -n "$existing" ]]; then
        info "Removing existing container $cntr (was: $existing)..."
        podman rm -f "$cntr" >/dev/null 2>&1 || true
    fi

    # Read credentials — CLI flags take priority over .env
    local admin_token node_name description webhook_sec
    if [[ -n "$TOKEN_ARG" ]]; then
        admin_token="$TOKEN_ARG"
    else
        admin_token="$(read_env_key RED_ADMIN_TOKEN "$env_f")"
    fi
    if [[ -n "$NODE_NAME" ]]; then
        node_name="$NODE_NAME"
    else
        node_name="$(read_env_key RED_NODE_NAME "$env_f")"
    fi
    description="$(read_env_key RED_NODE_DESCRIPTION "$env_f")"
    webhook_sec="$( read_env_key RED_WEBHOOK_SECRET   "$env_f")"

    [[ -n "$admin_token" ]] || die "RED_ADMIN_TOKEN not set in $env_f"

    info "Starting $cntr (data: $DIR_NAME)..."

    # SELinux volume relabelling on Linux
    local vol_flags=":rw"
    $IS_LINUX && vol_flags=":rw,z"

    local run_args=(
        "run" "-d"
        "--pull=never"
        "--name"    "$cntr"
        "--restart" "unless-stopped"
        "--userns=keep-id"
        "-v" "${dd}:/app/data${vol_flags}"
        "-v" "${sd}:/app/state${vol_flags}"
        "-e" "RED_DATA_DIR=/app/data"
        "-e" "RED_STATE_DIR=/app/state"
        "-e" "RED_ADMIN_TOKEN=${admin_token}"
        "-e" "RED_NODE_NAME=${node_name}"
        "-e" "RED_NODE_DESCRIPTION=${description}"
        "-e" "RED_WEBHOOK_SECRET=${webhook_sec}"
    )

    if $IS_LINUX; then
        # Host networking: container shares the host netns; app binds :PORT directly
        run_args+=("--network" "host" "-e" "RED_ADDR=:${PORT}")
    else
        # macOS: bridge networking with port mapping
        run_args+=("-p" "${PORT}:8080" "-e" "RED_ADDR=:8080")
    fi

    run_args+=("$IMAGE_NAME")

    podman "${run_args[@]}"

    info "Waiting for health check on port $PORT..."
    if ! wait_node_health "$PORT" 30; then
        fail "Node did not become healthy within 30s. Check logs:"
        echo "  ./red-engine.sh logs -p $PORT [--follow]"
        exit 1
    fi
    ok "$cntr is UP — http://localhost:$PORT"

    $TUNNEL && cmd_tunnel
}

cmd_stop() {
    local cntr; cntr="$(container_name)"
    local state; state="$(container_status "$cntr")"
    if [[ -n "$state" ]]; then
        info "Stopping container $cntr..."
        podman rm -f "$cntr" >/dev/null 2>&1 || true
        ok "$cntr stopped."
    else
        warn "No container found: $cntr"
    fi

    stop_tunnel "$PORT"

    if $WIPE_DATA; then
        local dd; dd="$(data_dir)"
        if [[ -d "$dd" ]]; then
            rm -rf "$dd"
            info "Wiped data directory: $dd"
        fi
    fi
}

cmd_restart() {
    local env_f; env_f="$(env_file)"
    [[ -f "$env_f" ]] || die "No .env found for port $PORT. Run setup first."
    cmd_stop
    sleep 2
    cmd_start
}

cmd_logs() {
    local cntr; cntr="$(container_name)"
    [[ -n "$(container_status "$cntr")" ]] || die "Container $cntr is not running."
    if $FOLLOW; then
        podman logs -f "$cntr"
    else
        podman logs --tail 100 "$cntr"
    fi
}

cmd_tunnel() {
    info "Requesting cloudflared quick tunnel for port $PORT..."
    local url; url="$(start_tunnel)"
    ok "Tunnel URL: $url"

    # Push public_url into the running node's settings via the admin API
    local env_f; env_f="$(env_file)"
    local admin_token; admin_token="$(read_env_key RED_ADMIN_TOKEN "$env_f")"
    if [[ -n "$admin_token" ]]; then
        local body="{\"public_url\":\"${url}\",\"tunnel_type\":\"cloudflare_quick\"}"
        if curl -sf -X POST "http://localhost:$PORT/-/admin/config" \
                -H "Content-Type: application/json" \
                -H "X-Admin-Token: $admin_token" \
                -d "$body" >/dev/null 2>&1; then
            info "public_url updated in node settings."
        else
            warn "Could not push public_url to the running node."
            warn "The URL is saved in $(tunnel_url_file); restart the node to pick it up."
        fi
    fi
}

cmd_status() {
    assert_tool podman "Install from https://podman.io/"
    head_ "RED Engine nodes"
    echo

    local containers=()
    while IFS= read -r line; do
        [[ "$line" =~ ^red_engine_[0-9]+$ ]] && containers+=("$line")
    done < <(podman ps -a --format "{{.Names}}" 2>/dev/null || true)

    if [[ ${#containers[@]} -eq 0 ]]; then
        warn "No red_engine containers found."
        echo -e "  ${DG}Run: ./red-engine.sh setup [-p PORT] [-d DIR]${NC}"
        return
    fi

    local c
    for c in "${containers[@]}"; do
        local p="${c#red_engine_}"
        local state; state="$(podman inspect --format '{{.State.Status}}' "$c" \
                                2>/dev/null | tr -d '[:space:]' || echo "unknown")"

        local health="unreachable"
        curl -sf --max-time 2 "http://localhost:${p}/-/health" >/dev/null 2>&1 \
            && health="healthy"

        local url_dir
        if [[ "$p" -eq 8080 ]]; then url_dir="$ROOT"
        else url_dir="$NODES_DIR/$p"
        fi
        local tunnel_url=""
        local url_f="$url_dir/tunnel-url"
        [[ -f "$url_f" ]] && tunnel_url="$(tr -d '[:space:]' < "$url_f")"

        local sc="$YL"
        case "$state" in running) sc="$GN" ;; exited) sc="$RD" ;; esac
        local hc="$RD"
        [[ "$health" == "healthy" ]] && hc="$GN"

        echo -e "  ${DG}────────────────────────────────────────────────────────────${NC}"
        printf   "  ${CY}%-22s${NC}  port ${YL}%-6s${NC}  ${sc}%s${NC}  ${hc}%s${NC}\n" \
            "$c" "$p" "$state" "$health"
        printf   "  Local  : ${CY}http://localhost:%s${NC}\n" "$p"
        if [[ -n "$tunnel_url" ]]; then
            printf "  Tunnel : ${MG}%s${NC}\n" "$tunnel_url"
        else
            printf "  Tunnel : ${DG}none${NC}\n"
        fi
    done
    echo -e "  ${DG}────────────────────────────────────────────────────────────${NC}"
    echo
}

cmd_token() {
    local env_f; env_f="$(env_file)"
    [[ -f "$env_f" ]] || die "No .env found for port $PORT."

    head_ "Rotate admin token (port $PORT)"
    local current; current="$(read_env_key RED_ADMIN_TOKEN "$env_f")"
    [[ -n "$current" ]] && echo -e "  Current: ${YL}${current}${NC}"

    echo
    read -rp "  Generate and apply a new secure token? [y/N]: " choice
    if [[ ! "$choice" =~ ^[yY]$ ]]; then info "Token unchanged."; return; fi

    local new_token; new_token="$(gen_token)"
    write_env_key "RED_ADMIN_TOKEN" "$new_token" "$env_f"
    echo
    ok "New admin token: $new_token"
    warn "Restart the node for the change to take effect:"
    echo "  ./red-engine.sh restart -p $PORT"
}

cmd_backup() {
    local dd; dd="$(data_dir)"
    [[ -d "$dd" ]] || die "Data directory not found: $dd"

    local stamp; stamp="$(date '+%Y%m%d_%H%M%S')"
    local back_dir="$ROOT/backups"
    local dest="$back_dir/${DIR_NAME}-${PORT}_${stamp}.tar.gz"
    mkdir -p "$back_dir"
    tar -czf "$dest" -C "$(dirname "$dd")" "$(basename "$dd")"
    ok "Backup written: $dest"
}

cmd_update() {
    head_ "Updating RED Engine (port $PORT)"

    info "Pulling latest source..."
    git -C "$ROOT" pull || warn "git pull failed — continuing with current code."

    assert_tool go "Install from https://golang.org/"
    info "Running tests before rebuild..."
    (cd "$ROOT" && go test ./...) || die "Tests failed. Aborting update to protect the running node."

    cmd_build

    local cntr; cntr="$(container_name)"
    if [[ "$(container_status "$cntr")" == "running" ]]; then
        info "Restarting node..."
        cmd_restart
    else
        info "Node was not running. Start with:"
        echo "  ./red-engine.sh start -p $PORT"
    fi
    ok "Update complete."
}

cmd_test() {
    assert_tool go "Install from https://golang.org/"
    head_ "Running test suite"
    (cd "$ROOT" && go test ./...) && ok "All tests passed." || die "Tests failed."
}

cmd_dev() {
    assert_tool go  "Install from https://golang.org/"
    assert_tool npm "Install from https://nodejs.org/"
    head_ "Starting development environment"

    if ! command -v air >/dev/null 2>&1; then
        warn "air not found — installing..."
        go install github.com/air-verse/air@latest
        export PATH="$PATH:$(go env GOPATH)/bin"
    fi

    if [[ ! -d "$ROOT/node_modules" ]]; then
        info "Installing npm dependencies..."
        (cd "$ROOT" && npm install --legacy-peer-deps)
    fi

    export NODE_OPTIONS="--disable-warning=DEP0205"
    info "Starting Vite dev server on :5173..."
    (cd "$ROOT" && npx vite) &
    local vite_pid=$!

    warn "Open http://localhost:5173 in your browser."
    export DEV_MODE=true

    cleanup() {
        kill "$vite_pid" 2>/dev/null || true
        ok "Development environment stopped."
    }
    trap cleanup EXIT

    cd "$ROOT"
    if [[ -f ".air.dev.toml" ]]; then
        air -c .air.dev.toml -- "-config=config.json"
    else
        go run ./cmd/red/main.go "-config=config.json"
    fi
}

cmd_help() {
    cat << EOF

  RED Engine — Podman node management (Linux / macOS)
  =====================================================

  ./red-engine.sh <command> [-p PORT] [-d DIR] [options]

  SETUP & BUILD
    setup               First-time wizard for the node on -p PORT (default: 8080)
    build               Build (or rebuild) the red-engine:latest container image

  NODE LIFECYCLE
    start [--tunnel]    Start the node; --tunnel also opens a cloudflared tunnel
    stop  [--wipe-data] Stop the node (container + tunnel); --wipe-data deletes data dir
    restart             Restart without rebuilding the image
    logs  [--follow]    Show last 100 lines; --follow to stream live
    status              Table of all active red_engine_* containers + health

  TUNNEL
    tunnel              Start a cloudflared quick tunnel for this node's port
                        (auto-pushes public_url to the node's settings API)

  MAINTENANCE
    token               Rotate the admin token for this node
    backup              Tar-gz this node's data directory into backups/
    update              git pull -> go test -> rebuild image -> restart

  DEVELOPMENT
    test                Run Go unit tests (go test ./...)
    dev                 Vite dev server + Go live reload (no container)
    help                Show this message

  FLAGS
    -p, --port <n>      Port of the node to operate on  [default: 8080]
    -d, --dir  <name>   Data directory name inside the node dir  [default: data]
    --node-name <s>     Override node name  (setup / start)
    --token <s>         Override admin token  (start)
    --tunnel            Also start a cloudflared tunnel  (start)
    --follow            Stream logs  (logs)
    --wipe-data         Also delete the data directory  (stop)

  MULTI-NODE EXAMPLE — 3 independent nodes for federation testing
    ./red-engine.sh setup   -p 8080 -d vault-a   # wizard for node 1
    ./red-engine.sh setup   -p 8081 -d vault-b   # wizard for node 2
    ./red-engine.sh setup   -p 8082 -d vault-c   # wizard for node 3
    ./red-engine.sh build                         # build image once
    ./red-engine.sh start   -p 8080 -d vault-a --tunnel
    ./red-engine.sh start   -p 8081 -d vault-b --tunnel
    ./red-engine.sh start   -p 8082 -d vault-c --tunnel
    ./red-engine.sh status                        # live view of all 3

  Node state layout:
    Port 8080  ->  ./vault-a/    ./state/    ./.env   (repo root)
    Port 8081  ->  ./.red-nodes/8081/vault-b/    state/    .env
    Port 8082  ->  ./.red-nodes/8082/vault-c/    state/    .env

EOF
}

# ── Dispatch ──────────────────────────────────────────────────────────────────
cmd="$(echo "$COMMAND" | tr '[:upper:]' '[:lower:]')"
case "$cmd" in
    setup)   cmd_setup   ;;
    build)   cmd_build   ;;
    start)   cmd_start   ;;
    stop)    cmd_stop    ;;
    restart) cmd_restart ;;
    logs)    cmd_logs    ;;
    tunnel)  cmd_tunnel  ;;
    status)  cmd_status  ;;
    token)   cmd_token   ;;
    backup)  cmd_backup  ;;
    update)  cmd_update  ;;
    test)    cmd_test    ;;
    dev)     cmd_dev     ;;
    help)    cmd_help    ;;
    "")
        env_f="$(env_file)"
        if [[ -f "$env_f" ]] && [[ -n "$(read_env_key RED_ADMIN_TOKEN "$env_f")" ]]; then
            cmd_status
        else
            cmd_help
        fi
        ;;
    *)
        die "Unknown command: '$COMMAND'. Run: ./red-engine.sh help"
        ;;
esac
