#!/usr/bin/env bash
#
# local-rediscovery-test.sh — deterministic localhost repro of the three-node
# dual-restart rediscovery scenario, with NO Cloudflare tunnels.
#
# Same logic as three-node-test.sh SCENARIO 2, but "a new tunnel URL" is
# simulated by restarting the node on a NEW localhost port. This isolates the
# backend heal path (announce → /-/peer/resolve → direct re-auth → UpdatePeerURL
# → peer-sync resume) from Cloudflare quick-tunnel flakiness, so a FAIL here is
# a real backend bug and a PASS here means cloud-run failures are infrastructure.
#
#   A  :9101 → restarts on :9111   (URL changes)
#   B  :9102 → restarts on :9112   (URL changes)
#   C  :9103                       (stable directory, never moves)
#
# Usage:  ./scripts/local-rediscovery-test.sh        # run, prints PASS/FAIL
#         ./scripts/local-rediscovery-test.sh clean
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT"

STATE="$ROOT/.localredisc"
RED_BIN="$ROOT/red"

HEARTBEAT_SECS=5
PEERSYNC_SECS=5
BUCKET="seed-notes"

c_red=$'\033[31m'; c_grn=$'\033[32m'; c_ylw=$'\033[33m'; c_cyn=$'\033[36m'; c_rst=$'\033[0m'
log()  { printf '%s[local]%s %s\n' "$c_cyn" "$c_rst" "$*"; }
ok()   { printf '%s✓%s %s\n' "$c_grn" "$c_rst" "$*"; }
warn() { printf '%s!%s %s\n' "$c_ylw" "$c_rst" "$*" >&2; }
err()  { printf '%s✗%s %s\n' "$c_red" "$c_rst" "$*" >&2; }
die()  { err "$*"; exit 1; }

node_token() { case "$1" in A) echo TOK-A-aaaaaaaaaaaaaaaa;; B) echo TOK-B-bbbbbbbbbbbbbbbb;; C) echo TOK-C-cccccccccccccccc;; esac; }
node_dir()   { echo "$STATE/$1"; }
node_data()  { echo "$STATE/$1/data"; }
node_state() { echo "$STATE/$1/state"; }
node_pubkey(){ cat "$(node_state "$1")/node.pub" 2>/dev/null || echo ""; }

# Current port per node lives in a file so restart-on-new-port is one write.
node_port()  { cat "$(node_dir "$1")/port"; }
base_url()   { echo "http://localhost:$(node_port "$1")"; }

kill_all() { pkill -f "$RED_BIN -config $STATE/" 2>/dev/null || true; }

wait_http() {
  local url="$1" timeout="${2:-30}" i=0
  while [ "$i" -lt "$timeout" ]; do
    [ "$(curl -s -o /dev/null -w '%{http_code}' "$url" 2>/dev/null)" = "200" ] && return 0
    sleep 1; i=$((i+1))
  done
  return 1
}

write_config() {  # write_config <node> <port>
  local n="$1" port="$2"
  mkdir -p "$(node_dir "$n")" "$(node_data "$n")" "$(node_state "$n")"
  echo "$port" > "$(node_dir "$n")/port"
  cat > "$(node_dir "$n")/config.json" <<EOF
{
  "addr": ":$port",
  "dataDir": "$(node_data "$n")",
  "stateDir": "$(node_state "$n")",
  "adminToken": "$(node_token "$n")",
  "nodeName": "Node-$n"
}
EOF
}

start_node() {
  local n="$1" dir; dir="$(node_dir "$n")"
  RED_PUBLIC_URL="$(base_url "$n")" RED_ALLOW_PRIVATE_SYNC=true \
    RED_FEDERATION_HEARTBEAT="$HEARTBEAT_SECS" RED_PEER_SYNC_INTERVAL="$PEERSYNC_SECS" \
    "$RED_BIN" -config "$dir/config.json" >>"$dir/red.log" 2>&1 &
  echo $! > "$dir/red.pid"
  wait_http "$(base_url "$n")/-/health" 30 || { err "node $n unhealthy (see $dir/red.log)"; return 1; }
}

stop_node() {
  local n="$1" dir; dir="$(node_dir "$1")"
  [ -f "$dir/red.pid" ] && { kill "$(cat "$dir/red.pid")" 2>/dev/null; rm -f "$dir/red.pid"; }
}

add_peer() {  # add_peer <srcNode> <peerURL>
  curl -s -X POST "$(base_url "$1")/-/admin/peers/add" \
    -H "X-Admin-Token: $(node_token "$1")" -H "Content-Type: application/json" \
    -d "{\"url\":\"$2\",\"peer_type\":\"mirror\"}" -o /dev/null -w '%{http_code}'
}

peer_url_in() {  # peer_url_in <node> <peerPubKey>
  curl -s -H "X-Admin-Token: $(node_token "$1")" "$(base_url "$1")/-/admin/peers" \
    | python3 -c "import json,sys
key='$2'
try: peers=json.load(sys.stdin) or []
except Exception: peers=[]
for p in peers:
    if p.get('public_key')==key: print(p.get('url','')); break"
}

cmd_clean() { kill_all; rm -rf "$STATE"; ok "removed $STATE"; }

cmd_run() {
  command -v python3 >/dev/null || die "python3 not found"
  [ -x "$RED_BIN" ] || die "red binary missing at $RED_BIN (go build -o red ./cmd/red)"

  kill_all
  rm -rf "$STATE"
  trap kill_all EXIT INT TERM
  mkdir -p "$STATE"

  ###########################################################################
  log "PHASE 1 — 3 nodes on localhost, full mesh"
  ###########################################################################
  write_config A 9101; write_config B 9102; write_config C 9103
  for n in A B C; do
    start_node "$n" || die "start $n"
    ok "node $n up on $(base_url "$n") key=$(node_pubkey "$n" | cut -c1-16)…"
  done
  local PK_A PK_B PK_C URL_A1 URL_B1
  PK_A="$(node_pubkey A)"; PK_B="$(node_pubkey B)"; PK_C="$(node_pubkey C)"
  URL_A1="$(base_url A)"; URL_B1="$(base_url B)"

  log "mesh: A↔B $(add_peer A "$(base_url B)")/$(add_peer B "$(base_url A)")  A↔C $(add_peer A "$(base_url C)")/$(add_peer C "$(base_url A)")  B↔C $(add_peer B "$(base_url C)")/$(add_peer C "$(base_url B)")"
  sleep 3  # let reciprocal RegisterAsDownstream goroutines finish

  ###########################################################################
  log "PHASE 2 — seed content on A, subscribe B"
  ###########################################################################
  mkdir -p "$(node_data A)/$BUCKET"
  for i in 1 2 3; do echo "# seed note $i" > "$(node_data A)/$BUCKET/note-$i.md"; done
  sleep 2  # watcher pickup
  curl -s -X POST "$(base_url B)/-/import" -H "X-Admin-Token: $(node_token B)" \
    -H "Content-Type: application/json" \
    -d "{\"peer_url\":\"$(base_url A)\",\"remote_path\":\"$BUCKET\",\"filename\":\"$BUCKET\",\"saveToStartup\":true}" \
    -w '\n  → HTTP %{http_code}\n'
  curl -s "$(base_url B)/content/$BUCKET/note-1.md" | grep -q "seed note" \
    && ok "B pulled A's seed content" || die "B did not pull A's seed content"

  ###########################################################################
  log "PHASE 3 — kill A and B; restart on NEW ports (simulated new tunnel URLs)"
  ###########################################################################
  stop_node A; stop_node B
  sleep 1
  write_config A 9111; write_config B 9112   # new ports = new public URLs, same identity/state
  start_node A || die "restart A"
  start_node B || die "restart B"
  local URL_A2 URL_B2
  URL_A2="$(base_url A)"; URL_B2="$(base_url B)"
  ok "A back on $URL_A2 (was $URL_A1); B back on $URL_B2 (was $URL_B1); C unchanged"

  echo "# rediscovery sentinel $(date -u +%FT%TZ)" > "$(node_data A)/$BUCKET/sentinel.md"

  ###########################################################################
  log "PHASE 4 — wait for A↔B to heal via C (signed resolve + direct re-auth)"
  ###########################################################################
  local i=0 seenA="" seenB="" healed=0
  while [ "$i" -lt 12 ]; do
    sleep "$HEARTBEAT_SECS"
    seenA="$(peer_url_in B "$PK_A")"
    seenB="$(peer_url_in A "$PK_B")"
    log "  t+$(( (i+1)*HEARTBEAT_SECS ))s — B stores A=${seenA:-<none>} | A stores B=${seenB:-<none>}"
    [ "$seenA" = "$URL_A2" ] && [ "$seenB" = "$URL_B2" ] && { healed=1; break; }
    i=$((i+1))
  done

  local sync_ok=0 j=0
  if [ "$healed" = 1 ]; then
    log "waiting for B to pull the sentinel from A's NEW url…"
    while [ "$j" -lt 10 ]; do
      sleep "$PEERSYNC_SECS"
      curl -s "$(base_url B)/content/$BUCKET/sentinel.md" | grep -q "rediscovery sentinel" && { sync_ok=1; break; }
      j=$((j+1))
    done
  fi

  echo; log "── evidence: C's directory answers ──"
  grep -E "\[Resolve\] ANSWER" "$(node_dir C)/red.log" | tail -4 || true
  log "── evidence: heals on A and B ──"
  grep -hE "\[Resolve\] (HEALED|rediscovered|could not)" "$(node_dir A)/red.log" "$(node_dir B)/red.log" | tail -8 || true
  echo

  ###########################################################################
  log "FINAL RESULTS"
  ###########################################################################
  local pass=1
  [ "$seenA" = "$URL_A2" ] && ok "B healed A → $seenA" || { err "B stores A=${seenA:-<none>} (want $URL_A2)"; pass=0; }
  [ "$seenB" = "$URL_B2" ] && ok "A healed B → $seenB" || { err "A stores B=${seenB:-<none>} (want $URL_B2)"; pass=0; }
  [ "$sync_ok" = 1 ] && ok "B pulled sentinel from A's new URL (sync resumed)" || { err "sync did not resume"; pass=0; }

  if [ "$pass" = 1 ]; then
    printf '\n%sLOCAL REDISCOVERY TEST: PASS%s\n' "$c_grn" "$c_rst"
  else
    printf '\n%sLOCAL REDISCOVERY TEST: FAIL%s\n' "$c_red" "$c_rst"
    log "── full federation log excerpts ──"
    for n in A B C; do
      echo "--- node $n ---"
      grep -E "\[Resolve\]|\[Gossip\]|\[PeerSync\]|announce|Announce" "$(node_dir "$n")/red.log" | tail -20
    done
    exit 1
  fi
}

case "${1:-run}" in
  clean) cmd_clean ;;
  run|"") cmd_run ;;
  *) die "usage: $0 [run|clean]" ;;
esac
