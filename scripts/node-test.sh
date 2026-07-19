#!/usr/bin/env bash
#
# node-test.sh — two-node RED federation "Verifying peers" test harness.
#
# Brings up two local RED nodes, each with its own identity (isolated $HOME),
# config, dataDir, and Cloudflare quick tunnel, then exercises the signed
# challenge-response re-registration handshake:
#
#   Node A = upstream   (config.json,  :8080, data/)   content source
#   Node B = downstream (config1.json, :8081, data1/)  consumer
#
# A registers B as a `downstream` peer; B registers A as an `upstream` peer and
# pulls A's content. When A restarts on a NEW tunnel URL it signs nonce|new_url
# with its private key and announces to B, who verifies against A's stored public
# key and updates A's URL.
#
# Usage:
#   ./scripts/node-test.sh build
#   ./scripts/node-test.sh up A | up B | down A | down B
#   ./scripts/node-test.sh register
#   ./scripts/node-test.sh reconnect
#   ./scripts/node-test.sh verify
#   ./scripts/node-test.sh auto          # full cycle, prints VERIFY: PASS/FAIL
#   ./scripts/node-test.sh status
#   ./scripts/node-test.sh clean [--wipe-data]
#
set -uo pipefail

# ── Locate repo root (this script lives in scripts/) ────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT"

STATE="$ROOT/.nodetest"
RED_BIN="$ROOT/red"

# ── Per-node parameters ─────────────────────────────────────────────────────
# node | config | port | dataDir
node_config() { [ "$1" = A ] && echo "config.json"  || echo "config1.json"; }
node_port()   { [ "$1" = A ] && echo "8080"         || echo "8081"; }
node_data()   { [ "$1" = A ] && echo "data"         || echo "data1"; }
node_home()   { echo "$STATE/$1/home"; }
node_dir()    { echo "$STATE/$1"; }

# ── Pretty logging ──────────────────────────────────────────────────────────
c_red=$'\033[31m'; c_grn=$'\033[32m'; c_ylw=$'\033[33m'; c_cyn=$'\033[36m'; c_rst=$'\033[0m'
log()  { printf '%s[node-test]%s %s\n' "$c_cyn" "$c_rst" "$*"; }
ok()   { printf '%s✓%s %s\n' "$c_grn" "$c_rst" "$*"; }
warn() { printf '%s!%s %s\n' "$c_ylw" "$c_rst" "$*"; }
err()  { printf '%s✗%s %s\n' "$c_red" "$c_rst" "$*" >&2; }
die()  { err "$*"; exit 1; }

require() { command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"; }

# ── Helpers ─────────────────────────────────────────────────────────────────

# token_for <config.json> — extract adminToken
token_for() {
  python3 -c "import json,sys; print(json.load(open('$1')).get('adminToken',''))"
}

# base_url <node> — local base URL (for admin calls we always hit localhost)
base_url() { echo "http://localhost:$(node_port "$1")"; }

# db_set_setting <dataDir> <key> <value> — upsert into node_settings.
# Mirrors registry.SetSetting. Only called with the node STOPPED (reconnect) or
# right after first boot (table guaranteed to exist).
db_set_setting() {
  local data="$1" key="$2" val="$3" db="$1/registry.db"
  [ -f "$db" ] || die "registry DB not found at $db (node booted yet?)"
  # Ensure the table exists (no-op if the app already created it), then upsert.
  sqlite3 "$db" \
    "CREATE TABLE IF NOT EXISTS node_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
     INSERT INTO node_settings(key,value) VALUES('$key','$val')
       ON CONFLICT(key) DO UPDATE SET value=excluded.value;" \
    || die "sqlite write failed for $key on $db"
}

# wait_http <url> <timeout-secs> — poll until HTTP 200
wait_http() {
  local url="$1" timeout="${2:-30}" i=0
  while [ "$i" -lt "$timeout" ]; do
    if [ "$(curl -s -o /dev/null -w '%{http_code}' "$url" 2>/dev/null)" = "200" ]; then
      return 0
    fi
    sleep 1; i=$((i+1))
  done
  return 1
}

# wait_tunnel_url <logfile> <timeout-secs> — echo the trycloudflare URL once seen
wait_tunnel_url() {
  local logf="$1" timeout="${2:-45}" i=0 url=""
  while [ "$i" -lt "$timeout" ]; do
    # Quick-tunnel URLs are always multi-word hyphenated (e.g.
    # word-word-word.trycloudflare.com). Require a hyphen so we never match the
    # informational "api.trycloudflare.com" host cloudflared also logs.
    url="$(grep -oE 'https://[a-z0-9]+(-[a-z0-9]+)+\.trycloudflare\.com' "$logf" 2>/dev/null | head -1)"
    [ -n "$url" ] && { echo "$url"; return 0; }
    sleep 1; i=$((i+1))
  done
  return 1
}

# node_pubkey <node> — this node's hex public key (from its isolated HOME)
node_pubkey() {
  local pub; pub="$(node_home "$1")/.red-engine/node.pub"
  [ -f "$pub" ] && cat "$pub" || echo ""
}

# peer_url_in <base> <token> <pubkey> — stored URL for a peer by public key
peer_url_in() {
  local base="$1" token="$2" pubkey="$3"
  curl -s -H "X-Admin-Token: $token" "$base/-/admin/peers" \
    | python3 -c "
import json,sys
key='$pubkey'
try: peers=json.load(sys.stdin) or []
except Exception: peers=[]
for p in peers:
    if p.get('public_key')==key:
        print(p.get('url','')); break
"
}

# pid_alive <pidfile>
pid_alive() { local f="$1"; [ -f "$f" ] && kill -0 "$(cat "$f")" 2>/dev/null; }

# ── Commands ────────────────────────────────────────────────────────────────

ensure_configs() {
  # Create config1.json for node B if missing
  if [ ! -f "$ROOT/config1.json" ]; then
    cat > "$ROOT/config1.json" << 'EOF'
{
  "addr": ":8081",
  "dataDir": "data1",
  "adminToken": "dev-token-B",
  "webhookSecret": "",
  "WebDir": "FRONTEND_BUILD"
}
EOF
    ok "Created config1.json (node B)"
  fi
  # Ensure config.json exists (it should, but just in case)
  if [ ! -f "$ROOT/config.json" ]; then
    cat > "$ROOT/config.json" << 'EOF'
{
  "addr": ":8080",
  "dataDir": "data",
  "adminToken": "8zhOyGP5477eYhrmIFx1WnjXGzRjbMg8",
  "webhookSecret": "",
  "WebDir": "FRONTEND_BUILD"
}
EOF
    ok "Created config.json (node A)"
  fi
}

cmd_build() {
  require go; require npm
  ensure_configs
  log "Building SPA (npm run build)…"
  (cd "$ROOT/internal/router/red-engine-frontend" && npm install --silent && npm run build) >/dev/null 2>&1 || \
    die "npm run build failed (run it directly from internal/router/red-engine-frontend to see errors)"
  log "Building red binary (go build -o red ./cmd/red)…"
  go build -o "$RED_BIN" ./cmd/red || die "go build failed"
  ok "Built: $RED_BIN"
}

# start a tunnel for <node>, returns URL on stdout (also writes <dir>/url).
# Account-less quick tunnels are occasionally refused by Cloudflare with
# "failed to request quick Tunnel: ... unexpected EOF" (transient / rate limit),
# so we retry a few times with backoff before giving up.
start_tunnel() {
  local n="$1" dir port logf attempt max=4 url
  dir="$(node_dir "$n")"; port="$(node_port "$n")"; logf="$dir/tunnel.log"
  mkdir -p "$dir"
  for attempt in $(seq 1 "$max"); do
    : > "$logf"
    cloudflared tunnel --no-autoupdate --url "http://localhost:$port" >"$logf" 2>&1 &
    echo $! > "$dir/tunnel.pid"
    if url="$(wait_tunnel_url "$logf" 45)"; then
      echo "$url" > "$dir/url"
      echo "$url"
      return 0
    fi
    # Failed: kill this attempt's process before retrying.
    kill "$(cat "$dir/tunnel.pid" 2>/dev/null)" 2>/dev/null || true
    if grep -q "failed to request quick Tunnel" "$logf" 2>/dev/null; then
      warn "tunnel for $n refused by Cloudflare (attempt $attempt/$max) — likely rate limit; retrying in $((attempt*10))s…"
    else
      warn "tunnel for $n produced no URL (attempt $attempt/$max); retrying in $((attempt*10))s…"
    fi
    [ "$attempt" -lt "$max" ] && sleep "$((attempt*10))"
  done
  err "tunnel for $n could not be created after $max attempts (see $logf)"
  return 1
}

cmd_up() {
  local n="${1:-}"; [ "$n" = A ] || [ "$n" = B ] || die "usage: up A|B"
  require cloudflared; require sqlite3; require python3; require curl
  [ -x "$RED_BIN" ] || die "red binary missing — run: $0 build"

  local dir cfg port data home url
  dir="$(node_dir "$n")"; cfg="$(node_config "$n")"; port="$(node_port "$n")"
  data="$(node_data "$n")"; home="$(node_home "$n")"
  mkdir -p "$dir" "$home" "$data"

  if pid_alive "$dir/red.pid"; then warn "node $n already running (pid $(cat "$dir/red.pid"))"; return 0; fi

  log "Node $n: starting Cloudflare quick tunnel for :$port…"
  url="$(start_tunnel "$n")" || return 1
  ok "Node $n tunnel: $url"

  # RED_PUBLIC_URL feeds the freshly-minted quick-tunnel URL straight into the
  # node at boot, so nodeinfo advertises it and the startup announce uses it. This
  # is the supported override and avoids poking the registry DB directly.
  log "Node $n: starting red (HOME=$home, config=$cfg, dataDir=$data, public_url=$url)…"
  HOME="$home" RED_PUBLIC_URL="$url" RED_TUNNEL_TYPE=cloudflare_quick \
    "$RED_BIN" -config "$cfg" >"$dir/red.log" 2>&1 &
  echo $! > "$dir/red.pid"

  if ! wait_http "$(base_url "$n")/-/health" 30; then
    err "node $n did not become healthy (see $dir/red.log)"; return 1
  fi
  ok "Node $n healthy on $(base_url "$n") — public_url=$url (key $(node_pubkey "$n" | cut -c1-16)…)"
}

cmd_down() {
  local n="${1:-}"; [ "$n" = A ] || [ "$n" = B ] || die "usage: down A|B"
  local dir; dir="$(node_dir "$n")"
  for p in red tunnel; do
    if [ -f "$dir/$p.pid" ]; then
      local pid; pid="$(cat "$dir/$p.pid")"
      if kill "$pid" 2>/dev/null; then log "Node $n: stopped $p (pid $pid)"; fi
      rm -f "$dir/$p.pid"
    fi
  done
  ok "Node $n down"
}

cmd_register() {
  require curl; require python3
  local aurl burl tokA tokB
  aurl="$(cat "$(node_dir A)/url" 2>/dev/null)" || true
  burl="$(cat "$(node_dir B)/url" 2>/dev/null)" || true
  [ -n "${aurl:-}" ] || die "node A tunnel URL unknown — run: $0 up A"
  [ -n "${burl:-}" ] || die "node B tunnel URL unknown — run: $0 up B"
  tokA="$(token_for "$(node_config A)")"
  tokB="$(token_for "$(node_config B)")"

  log "On A: registering B as downstream ($burl)…"
  curl -s -X POST "$(base_url A)/-/admin/peers/add" \
    -H "X-Admin-Token: $tokA" -H "Content-Type: application/json" \
    -d "{\"url\":\"$burl\",\"peer_type\":\"downstream\"}" \
    -o /dev/null -w 'HTTP %{http_code}\n'

  log "On B: registering A as upstream ($aurl)…"
  curl -s -X POST "$(base_url B)/-/admin/peers/add" \
    -H "X-Admin-Token: $tokB" -H "Content-Type: application/json" \
    -d "{\"url\":\"$aurl\",\"peer_type\":\"upstream\"}" \
    -o /dev/null -w 'HTTP %{http_code}\n'

  # Pull A's first exported path (whatever content A actually serves).
  local rpath fname
  rpath="$(curl -s "$(base_url A)/-/nodeinfo" \
    | python3 -c "import json,sys; p=(json.load(sys.stdin).get('exported_paths') or ['']); print(p[0])")"
  if [ -n "$rpath" ]; then
    fname="$(basename "$rpath")"
    log "On B: pulling $rpath from A (startup sync)…"
    curl -s -X POST "$(base_url B)/-/import" \
      -H "X-Admin-Token: $tokB" -H "Content-Type: application/json" \
      -d "{\"peer_url\":\"$aurl\",\"remote_path\":\"$rpath\",\"filename\":\"$fname\",\"saveToStartup\":true}" \
      -w '\nHTTP %{http_code}\n'
  else
    warn "A exports no paths — skipping downstream file sync"
  fi

  # Snapshot A's URL as seen by B (the value the reconnect test must change).
  local pkA seen
  pkA="$(node_pubkey A)"
  seen="$(peer_url_in "$(base_url B)" "$tokB" "$pkA")"
  echo "$seen" > "$(node_dir B)/seen_url_for_A"
  ok "B currently stores A ($(echo "$pkA" | cut -c1-16)…) at: ${seen:-<none>}"
}

cmd_reconnect() {
  require cloudflared; require sqlite3
  local data home cfg dir url2
  data="$(node_data A)"; home="$(node_home A)"; cfg="$(node_config A)"; dir="$(node_dir A)"

  log "Reconnect test: bringing A down…"
  cmd_down A

  log "Starting a NEW tunnel for A…"
  url2="$(start_tunnel A)" || die "could not start new tunnel for A"
  ok "Node A new tunnel: $url2"

  log "Restarting A on the new tunnel → boot announce signs nonce|$url2 and confirms to B…"
  HOME="$home" RED_PUBLIC_URL="$url2" RED_TUNNEL_TYPE=cloudflare_quick \
    "$RED_BIN" -config "$cfg" >"$dir/red.log" 2>&1 &
  echo $! > "$dir/red.pid"
  wait_http "$(base_url A)/-/health" 30 || die "A did not come back healthy (see $dir/red.log)"
  ok "Node A back up on $url2"
}

cmd_verify() {
  require curl; require python3
  local tokB pkA expected seen i=0
  tokB="$(token_for "$(node_config B)")"
  pkA="$(node_pubkey A)"
  expected="$(cat "$(node_dir A)/url" 2>/dev/null)"
  [ -n "${expected:-}" ] || die "node A current URL unknown"
  [ -n "${pkA:-}" ] || die "node A public key unknown (has A ever booted?)"

  log "Polling B for A's updated URL (expecting $expected)…"
  while [ "$i" -lt 30 ]; do
    seen="$(peer_url_in "$(base_url B)" "$tokB" "$pkA")"
    [ "$seen" = "$expected" ] && break
    sleep 1; i=$((i+1))
  done

  echo
  log "Evidence:"
  grep -E "Startup announce" "$(node_dir A)/red.log" | tail -3 || true
  grep -E "\[Announce\]"     "$(node_dir B)/red.log" | tail -3 || true
  echo

  if [ "$seen" = "$expected" ]; then
    ok "B now stores A at $seen"
    printf '%sVERIFY: PASS%s\n' "$c_grn" "$c_rst"
    log "Peer re-verification AND automatic content re-pull now follow the new URL: B's peer startup-sync is anchored to A's identity (sync_type=peer + peer_key), so the periodic peer-sync loop re-pulls from A's current address. (Reciprocal registration also means an upstream learns its downstream automatically on add.)"
    return 0
  else
    err "B stores A at '${seen:-<none>}', expected '$expected'"
    printf '%sVERIFY: FAIL%s\n' "$c_red" "$c_rst"
    return 1
  fi
}

cmd_status() {
  for n in A B; do
    local dir url; dir="$(node_dir "$n")"; url="$(cat "$dir/url" 2>/dev/null || echo '-')"
    if pid_alive "$dir/red.pid"; then
      printf '%s %-3s %-5s pid=%s url=%s key=%s…\n' "$(ok '' >/dev/null; echo up)" \
        "$n" "$(node_port "$n")" "$(cat "$dir/red.pid")" "$url" "$(node_pubkey "$n" | cut -c1-16)"
    else
      printf 'down %-3s %-5s url=%s\n' "$n" "$(node_port "$n")" "$url"
    fi
  done
}

cmd_auto() {
  cmd_build
  cmd_up A
  cmd_up B
  cmd_register
  cmd_reconnect
  cmd_verify
}

cmd_clean() {
  cmd_down A 2>/dev/null || true
  cmd_down B 2>/dev/null || true
  # Belt-and-suspenders: kill any stray cloudflared for our ports.
  pkill -f "cloudflared tunnel --no-autoupdate --url http://localhost:8080" 2>/dev/null || true
  pkill -f "cloudflared tunnel --no-autoupdate --url http://localhost:8081" 2>/dev/null || true
  rm -rf "$STATE"
  ok "Removed $STATE"
  if [ "${1:-}" = "--wipe-data" ]; then
    rm -f data/registry.db data1/registry.db
    rm -rf data1
    ok "Wiped registry DBs and data1/"
  fi
}

usage() {
  cat <<EOF
node-test.sh — two-node RED peer re-verification harness

  build                 npm run build + go build -o red ./cmd/red
  up A|B                start a node (tunnel + red + health + public_url seed)
  down A|B              stop a node (red + tunnel)
  register              cross-register peers (A↔B) and B pulls /Cryptography
  reconnect             restart A on a new tunnel; it re-announces to B
  verify                assert B updated A's URL; prints VERIFY: PASS/FAIL
  auto                  build → up A → up B → register → reconnect → verify
  status                show node pids / urls / identities
  clean [--wipe-data]   stop everything, remove .nodetest/ (and optionally data1/+DBs)
EOF
}

main() {
  local cmd="${1:-}"; shift || true
  case "$cmd" in
    build)     cmd_build "$@";;
    up)        cmd_up "$@";;
    down)      cmd_down "$@";;
    register)  cmd_register "$@";;
    reconnect) cmd_reconnect "$@";;
    verify)    cmd_verify "$@";;
    auto)      cmd_auto "$@";;
    status)    cmd_status "$@";;
    clean)     cmd_clean "$@";;
    ""|-h|--help|help) usage;;
    *) err "unknown command: $cmd"; usage; exit 1;;
  esac
}

main "$@"
