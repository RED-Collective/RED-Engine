#!/usr/bin/env bash
#
# three-node-test.sh — live 3-node RED federation test over Cloudflare quick tunnels.
#
# Tests two scenarios end-to-end:
#
#  SCENARIO 1 — Gossip import (import_peers flag)
#   A and C are already peers. B adds A with import_peers=true. The /-/peers list A
#   returns includes C, so B must discover and register C in the same API call
#   (gossip is synchronous — no second add required). Verified: B's registry shows C.
#
#  SCENARIO 2 — Dual-restart URL rediscovery + direct sync
#   A, B, C form a full mesh. A seeds content; B pulls it (subscription anchored to
#   A's key). Then A AND B are killed and come back on BRAND-NEW tunnel URLs while C
#   stays put. Each asks C for the other's current URL via signed /-/peer/resolve,
#   re-authenticates the target, and updates the stored URL. After rediscovery:
#     - B's registry stores A's new URL (URL_A2), not the old dead URL
#     - B's next peer-sync re-pulls content directly from A's new URL (not via C)
#     - A's registry stores B's new URL symmetrically
#
#   Node A  config-a.json  :9001  source
#   Node B  config-b.json  :9002  consumer
#   Node C  config-c.json  :9003  stable directory (never moves)
#
# Usage:
#   ./scripts/three-node-test.sh auto         # full cycle, prints PASS/FAIL
#   ./scripts/three-node-test.sh clean
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT"

STATE="$ROOT/.nodetest3"
RED_BIN="$ROOT/red"

# Fast federation loops so the live test converges in a couple of minutes, not 10.
HEARTBEAT_SECS=15
PEERSYNC_SECS=15

SEED_REPO="https://github.com/mundimark/awesome-markdown"
SEED_BUCKET="awesome-markdown"
NEW_REPO="https://github.com/StandardCodebase/Test-Repository"
NEW_BUCKET="Test-Repository"

c_red=$'\033[31m'; c_grn=$'\033[32m'; c_ylw=$'\033[33m'; c_cyn=$'\033[36m'; c_rst=$'\033[0m'
log()  { printf '%s[3node]%s %s\n' "$c_cyn" "$c_rst" "$*"; }
ok()   { printf '%s✓%s %s\n' "$c_grn" "$c_rst" "$*"; }
warn() { printf '%s!%s %s\n' "$c_ylw" "$c_rst" "$*" >&2; }
err()  { printf '%s✗%s %s\n' "$c_red" "$c_rst" "$*" >&2; }
die()  { err "$*"; exit 1; }

node_port()  { case "$1" in A) echo 9001;; B) echo 9002;; C) echo 9003;; esac; }
node_token() { case "$1" in A) echo TOK-A-aaaaaaaaaaaaaaaa;; B) echo TOK-B-bbbbbbbbbbbbbbbb;; C) echo TOK-C-cccccccccccccccc;; esac; }
node_dir()   { echo "$STATE/$1"; }
node_cfg()   { echo "$STATE/$1/config.json"; }
node_data()  { echo "$STATE/$1/data"; }
node_state() { echo "$STATE/$1/state"; }
base_url()   { echo "http://localhost:$(node_port "$1")"; }
node_pubkey(){ cat "$(node_state "$1")/node.pub" 2>/dev/null || echo ""; }

wait_http() {
  local url="$1" timeout="${2:-30}" i=0
  while [ "$i" -lt "$timeout" ]; do
    [ "$(curl -s -o /dev/null -w '%{http_code}' "$url" 2>/dev/null)" = "200" ] && return 0
    sleep 1; i=$((i+1))
  done
  return 1
}

# wait_tunnel_ready polls the node's public Cloudflare URL (not localhost) until
# /-/health returns 200 via HTTPS.  Required before making any peer-add calls:
# the tunnel URL may appear in logs before Cloudflare actually forwards traffic.
wait_tunnel_ready() {
  local url="$1" timeout="${2:-60}" i=0
  while [ "$i" -lt "$timeout" ]; do
    [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$url/-/health" 2>/dev/null)" = "200" ] && return 0
    sleep 1; i=$((i+1))
  done
  return 1
}

wait_tunnel_url() {
  local logf="$1" timeout="${2:-50}" i=0 url=""
  while [ "$i" -lt "$timeout" ]; do
    url="$(grep -oE 'https://[a-z0-9]+(-[a-z0-9]+)+\.trycloudflare\.com' "$logf" 2>/dev/null | head -1)"
    [ -n "$url" ] && { echo "$url"; return 0; }
    sleep 1; i=$((i+1))
  done
  return 1
}

start_tunnel() {
  local n="$1" dir port logf attempt max=6 url
  dir="$(node_dir "$n")"; port="$(node_port "$n")"; logf="$dir/tunnel.log"
  mkdir -p "$dir"
  for attempt in $(seq 1 "$max"); do
    : > "$logf"
    cloudflared tunnel --no-autoupdate --url "http://localhost:$port" >"$logf" 2>&1 &
    echo $! > "$dir/tunnel.pid"
    if url="$(wait_tunnel_url "$logf" 70)"; then
      echo "$url" > "$dir/url"; echo "$url"; return 0
    fi
    kill "$(cat "$dir/tunnel.pid" 2>/dev/null)" 2>/dev/null || true
    warn "tunnel $n attempt $attempt/$max produced no URL; retrying in $((attempt*10))s…"
    [ "$attempt" -lt "$max" ] && sleep "$((attempt*10))"
  done
  err "tunnel for $n failed after $max attempts (see $logf)"; return 1
}

write_config() {
  local n="$1" cfg data state
  cfg="$(node_cfg "$n")"; data="$(node_data "$n")"; state="$(node_state "$n")"
  mkdir -p "$(node_dir "$n")" "$data" "$state"
  cat > "$cfg" <<EOF
{
  "addr": ":$(node_port "$n")",
  "dataDir": "$data",
  "stateDir": "$state",
  "adminToken": "$(node_token "$n")",
  "nodeName": "Node-$n"
}
EOF
}

start_node() {
  local n="$1" url="$2" dir cfg
  dir="$(node_dir "$n")"; cfg="$(node_cfg "$n")"
  RED_PUBLIC_URL="$url" RED_TUNNEL_TYPE=cloudflare_quick \
    RED_FEDERATION_HEARTBEAT="$HEARTBEAT_SECS" RED_PEER_SYNC_INTERVAL="$PEERSYNC_SECS" \
    "$RED_BIN" -config "$cfg" >"$dir/red.log" 2>&1 &
  echo $! > "$dir/red.pid"
  wait_http "$(base_url "$n")/-/health" 30 || { err "node $n unhealthy (see $dir/red.log)"; return 1; }
}

stop_node() {
  local n="$1" dir; dir="$(node_dir "$n")"
  for p in red tunnel; do
    [ -f "$dir/$p.pid" ] && { kill "$(cat "$dir/$p.pid")" 2>/dev/null && log "stopped $n/$p"; rm -f "$dir/$p.pid"; }
  done
}

# launch_node_ready starts <node> behind a tunnel and only returns 0 once the
# tunnel VERIFIABLY forwards traffic (public /-/health == 200). A quick tunnel
# whose URL appears in the log but never forwards — Cloudflare rate-limiting
# after tunnel churn — used to produce a warn-and-continue, which guaranteed a
# 6-minute Scenario-2 failure (direct re-auth at the new URL can never succeed
# through a dead edge). Now a non-forwarding tunnel is recycled for a FRESH one
# (node restarted with it, since RED_PUBLIC_URL must match the tunnel) up to 3
# times. Whatever URL it ends up on is simply "the new URL" peers rediscover.
launch_node_ready() {
  local n="$1" t url
  for t in 1 2 3; do
    if ! url="$(start_tunnel "$n")"; then
      warn "no tunnel URL for $n (attempt $t/3)"; sleep 20; continue
    fi
    start_node "$n" "$url" || return 1
    if wait_tunnel_ready "$url" 90; then
      ok "node $n up on $(base_url "$n"), tunnel forwarding: $url"
      return 0
    fi
    warn "tunnel $n got URL but never forwarded in 90 s (attempt $t/3) — recycling tunnel"
    stop_node "$n"
    sleep 20  # rate-limit cooldown before asking Cloudflare for another tunnel
  done
  err "node $n never got a forwarding tunnel after 3 attempts"
  return 1
}

add_peer() {  # add_peer <srcNode> <peerTunnelURL>
  local n="$1" purl="$2" code attempt
  for attempt in 1 2 3 4 5 6; do
    code="$(curl -s -X POST "$(base_url "$n")/-/admin/peers/add" \
      -H "X-Admin-Token: $(node_token "$n")" -H "Content-Type: application/json" \
      -d "{\"url\":\"$purl\",\"peer_type\":\"mirror\"}" -o /dev/null -w '%{http_code}')"
    [ "$code" != "502" ] && break
    [ "$attempt" -lt 6 ] && sleep 15
  done
  echo "$code"
}

add_peer_with_gossip() {  # add_peer_with_gossip <srcNode> <peerTunnelURL>  → prints response body
  local n="$1" purl="$2" resp attempt
  for attempt in 1 2 3 4 5; do
    resp="$(curl -s -X POST "$(base_url "$n")/-/admin/peers/add" \
      -H "X-Admin-Token: $(node_token "$n")" -H "Content-Type: application/json" \
      -d "{\"url\":\"$purl\",\"peer_type\":\"upstream\",\"import_peers\":true}")"
    # Success returns JSON {"gossip_imported":N}; error returns plain text — retry on non-JSON.
    echo "$resp" | python3 -c "import json,sys; json.load(sys.stdin)" 2>/dev/null && break
    [ "$attempt" -lt 5 ] && sleep 15
  done
  echo "$resp"
}

gossip_count() {  # gossip_count <json_response>  → integer
  echo "$1" | python3 -c "import json,sys
try: print(json.load(sys.stdin).get('gossip_imported',0))
except Exception: print(0)"
}

peer_url_in() {  # peer_url_in <node> <peerPubKey>
  local n="$1" key="$2"
  curl -s -H "X-Admin-Token: $(node_token "$n")" "$(base_url "$n")/-/admin/peers" \
    | python3 -c "import json,sys
key='$key'
try: peers=json.load(sys.stdin) or []
except Exception: peers=[]
for p in peers:
    if p.get('public_key')==key: print(p.get('url','')); break"
}

manifest_count() {  # manifest_count <node> <bucket>  → number of files B sees for a bucket
  local n="$1" bucket="$2"
  curl -s "$(base_url "$n")/content/$bucket/manifest.json" \
    | python3 -c "import json,sys
try: m=json.load(sys.stdin)
except Exception: print(0); raise SystemExit
print(len((m or {}).get('files',{})))" 2>/dev/null || echo 0
}

# kill_all stops every node + tunnel this test could have started, matched by
# command-line PATTERN rather than tracked PID files. This is the critical
# safety net: a node left running by an earlier aborted run (die never cleaned
# up, and PID files may be stale or reused) keeps its port 9001-9003 bound, so a
# fresh node fails to bind — but the fresh process still briefly opens and
# migrates the SAME registry.db, yanking the file inode out from under the old
# process and producing "attempt to write a readonly database (1032)"
# (SQLITE_READONLY_DBMOVED) on every subsequent write. Clearing leftovers by
# pattern guarantees exactly one process per database. Safe to call repeatedly.
kill_all() {
  pkill -f "$RED_BIN -config $STATE/" 2>/dev/null || true
  for p in 9001 9002 9003; do
    pkill -f "cloudflared tunnel --no-autoupdate --url http://localhost:$p" 2>/dev/null || true
  done
}

cmd_clean() {
  kill_all
  for n in A B C; do stop_node "$n" 2>/dev/null || true; done
  rm -rf "$STATE"; ok "removed $STATE"
}

cmd_auto() {
  command -v cloudflared >/dev/null || die "cloudflared not found"
  command -v python3 >/dev/null || die "python3 not found"
  [ -x "$RED_BIN" ] || die "red binary missing at $RED_BIN (go build -o red ./cmd/red)"

  # Start from a guaranteed-clean slate: kill any node/tunnel left by an earlier
  # aborted run and delete its state dirs, so no stale process holds a port or a
  # registry.db open (the cause of the readonly-database/DBMOVED failures). The
  # EXIT trap then tears our own processes down no matter how we leave — normal
  # finish, die, or Ctrl-C — so the next run never inherits leftovers.
  kill_all
  rm -rf "$STATE"
  trap kill_all EXIT INT TERM
  mkdir -p "$STATE"

  ###########################################################################
  log "PHASE 1 — config + launch 3 nodes, each on its own Cloudflare tunnel"
  ###########################################################################
  for n in A B C; do write_config "$n"; done
  local URL_A1 URL_B1 URL_C
  # Each node launches behind a tunnel that is VERIFIED to forward traffic
  # before we move on (launch_node_ready recycles dead tunnels). A tunnel URL
  # appearing in cloudflared's log does NOT mean Cloudflare routes it yet, and
  # a never-forwarding tunnel poisons every later phase.
  log "launching nodes, each behind a forwarding-verified tunnel…"
  launch_node_ready A || die "launch A"
  launch_node_ready B || die "launch B"
  launch_node_ready C || die "launch C"
  URL_A1="$(cat "$(node_dir A)/url")"; ok "A1 = $URL_A1"
  URL_B1="$(cat "$(node_dir B)/url")"; ok "B1 = $URL_B1"
  URL_C="$(cat "$(node_dir C)/url")";  ok "C  = $URL_C  (stable directory)"
  local PK_A PK_B PK_C
  PK_A="$(node_pubkey A)"; PK_B="$(node_pubkey B)"; PK_C="$(node_pubkey C)"

  ###########################################################################
  log "SCENARIO 1 — gossip import: B adds A with import_peers=true, must discover C"
  ###########################################################################
  # Wire A↔C first so A's /-/peers list includes C when B gossips.
  log "A↔C: $(add_peer A "$URL_C") / $(add_peer C "$URL_A1")"
  # Sleep to let the reciprocal RegisterAsDownstream goroutines complete.
  # Each handler calls FetchNodeInfo on the other side; the goroutines are
  # fire-and-forget so the 201 response arrives before the goroutine finishes.
  sleep 3

  # B adds A with import_peers=true. The handler runs gossip synchronously and
  # returns {"gossip_imported": N} in the 201 body — N must be ≥ 1 (C is new).
  log "B adds A with import_peers=true (expecting C to be discovered transitively)…"
  local gossip_resp gossip_n
  gossip_resp="$(add_peer_with_gossip B "$URL_A1")"
  gossip_n="$(gossip_count "$gossip_resp")"
  log "  addPeer response: $gossip_resp"
  log "  gossip_imported = $gossip_n"

  # B must know C immediately — no second add.
  local b_knows_c
  b_knows_c="$(peer_url_in B "$PK_C")"
  log "  B's stored URL for C = ${b_knows_c:-<none>}"

  ###########################################################################
  log "SCENARIO 1 VERDICT"
  ###########################################################################
  local s1_pass=1
  [ "${gossip_n:-0}" -ge 1 ] \
    && ok "gossip_imported ≥ 1 in single API call" \
    || { err "gossip_imported=${gossip_n:-0} — gossip may still be async"; s1_pass=0; }
  [ -n "$b_knows_c" ] \
    && ok "B knows C ($b_knows_c) without a second add" \
    || { err "B does not know C after add-with-gossip (want $URL_C)"; s1_pass=0; }
  [ "$s1_pass" = 1 ] \
    && printf '%s✓ SCENARIO 1 (gossip import): PASS%s\n' "$c_grn" "$c_rst" \
    || printf '%s✗ SCENARIO 1 (gossip import): FAIL%s\n' "$c_red" "$c_rst"
  echo

  ###########################################################################
  log "PHASE 2 — complete the full mesh + seed content on A"
  ###########################################################################
  # B and C already know A; wire remaining pairs.
  log "A↔B: $(add_peer A "$URL_B1") / B↔C: $(add_peer B "$URL_C") $(add_peer C "$URL_B1")"
  # Allow RegisterAsDownstream goroutines to finish so C is in A's and B's
  # registries before the restart test relies on it for rediscovery.
  sleep 5
  log "seeding $SEED_REPO on A (git import)…"
  curl -s -X POST "$(base_url A)/-/import" -H "X-Admin-Token: $(node_token A)" \
    -H "Content-Type: application/json" -d "{\"url\":\"$SEED_REPO\",\"saveToStartup\":true}" \
    -w '\n  → HTTP %{http_code}\n'
  sleep 2
  for n in B C; do
    log "subscribing $n to A's /$SEED_BUCKET (peer sync, anchored to A's key)…"
    curl -s -X POST "$(base_url "$n")/-/import" -H "X-Admin-Token: $(node_token "$n")" \
      -H "Content-Type: application/json" \
      -d "{\"peer_url\":\"$URL_A1\",\"remote_path\":\"$SEED_BUCKET\",\"filename\":\"$SEED_BUCKET\",\"saveToStartup\":true}" \
      -w '  → HTTP %{http_code}\n'
  done

  ###########################################################################
  log "VERIFICATION 1 — B and C pulled A's content"
  ###########################################################################
  local v1b v1c
  v1b="$(manifest_count B "$SEED_BUCKET")"; v1c="$(manifest_count C "$SEED_BUCKET")"
  log "files in $SEED_BUCKET — B:$v1b C:$v1c"
  [ "${v1b:-0}" -gt 0 ] && [ "${v1c:-0}" -gt 0 ] && ok "initial sync OK" || warn "initial sync incomplete (B:$v1b C:$v1c)"

  ###########################################################################
  log "PHASE 3 — kill A and B (and their tunnels); restart on NEW tunnels"
  ###########################################################################
  stop_node A; stop_node B
  sleep 15  # let Cloudflare's rate limit window reset before opening new tunnels
  local URL_A2 URL_B2
  # Hard requirement, not best-effort: if A2/B2 come back on tunnels that never
  # forward, the heal CANNOT succeed (B's direct re-auth at A's new URL needs a
  # live edge) and the remaining ~6 minutes are a guaranteed FAIL. So recycle
  # until forwarding is verified, or die with a clear reason.
  launch_node_ready A || die "relaunch A on a forwarding tunnel"
  sleep 5   # stagger A2/B2 so Cloudflare doesn't see two simultaneous requests
  launch_node_ready B || die "relaunch B on a forwarding tunnel"
  URL_A2="$(cat "$(node_dir A)/url")"; ok "A2 = $URL_A2 (was $URL_A1)"
  URL_B2="$(cat "$(node_dir B)/url")"; ok "B2 = $URL_B2 (was $URL_B1)"
  [ "$URL_A2" = "$URL_A1" ] && warn "A tunnel URL did not change (test less meaningful)"
  [ "$URL_B2" = "$URL_B1" ] && warn "B tunnel URL did not change (test less meaningful)"
  ok "A and B back up on forwarding tunnels; C unchanged at $URL_C"

  # Drop a sentinel note so we can prove B pulled fresh content from A's new URL.
  local sentinel="SENTINEL-$(date +%s).md"
  echo "# rediscovery sentinel $(date -u +%FT%TZ)" > "$(node_data A)/$SEED_BUCKET/$sentinel"
  log "added sentinel $sentinel to A:/$SEED_BUCKET"

  ###########################################################################
  log "PHASE 4 — wait for self-announce to C, then A↔B rediscovery via C"
  ###########################################################################
  log "expected: A,B announce new URLs to C; then each asks C for the other's current URL"
  local i=0 seenA="" seenB=""
  while [ "$i" -lt 12 ]; do
    sleep "$HEARTBEAT_SECS"
    seenA="$(peer_url_in B "$PK_A")"   # what B's registry stores for A
    seenB="$(peer_url_in A "$PK_B")"   # what A's registry stores for B
    log "  t+$(( (i+1)*HEARTBEAT_SECS ))s — B stores A=${seenA:-<none>} | A stores B=${seenB:-<none>}"
    [ "$seenA" = "$URL_A2" ] && [ "$seenB" = "$URL_B2" ] && break
    i=$((i+1))
  done

  echo; log "── evidence: C answering directory lookups ──"
  grep -E "\[Resolve\] ANSWER" "$(node_dir C)/red.log" | tail -4 || true
  log "── evidence: A and B healing peers via C (incl. failed attempts) ──"
  grep -hE "\[Resolve\] (HEALED|rediscovered|could not)" "$(node_dir A)/red.log" "$(node_dir B)/red.log" | tail -8 || true
  echo

  ###########################################################################
  log "VERIFICATION 2 — B's registry stores A's NEW URL (not old dead URL)"
  ###########################################################################
  [ "$seenA" = "$URL_A2" ] \
    && ok "B's registry: A → $seenA (new URL, not dead URL_A1)" \
    || err "B's registry: A → ${seenA:-<none>} (want URL_A2=$URL_A2)"
  [ "$seenB" = "$URL_B2" ] \
    && ok "A's registry: B → $seenB (new URL, not dead URL_B1)" \
    || err "A's registry: B → ${seenB:-<none>} (want URL_B2=$URL_B2)"

  ###########################################################################
  log "VERIFICATION 3 — B syncs content directly from A's NEW URL, not via C"
  ###########################################################################
  local j=0 sync_ok=0
  while [ "$j" -lt 10 ]; do
    sleep "$PEERSYNC_SECS"
    if curl -s "$(base_url B)/content/$SEED_BUCKET/$sentinel" | grep -q "rediscovery sentinel"; then
      sync_ok=1; break
    fi
    j=$((j+1))
  done

  if [ "$sync_ok" = 1 ]; then
    ok "B has the sentinel — content reached B after rediscovery"
    # Prove the [PeerSync] pull used A's new URL, not C's URL.
    local a2_host; a2_host="${URL_A2#https://}"
    local c_host;  c_host="${URL_C#https://}"
    if grep -q "\[PeerSync\] re-pulled.*${a2_host}" "$(node_dir B)/red.log" 2>/dev/null; then
      ok "B's [PeerSync] log confirms pull from A's new URL ($URL_A2)"
    else
      warn "no [PeerSync] re-pulled line for $URL_A2 in B's log (sentinel arrived but URL evidence absent)"
    fi
    if grep "\[PeerSync\] re-pulled" "$(node_dir B)/red.log" 2>/dev/null | grep -q "$c_host"; then
      err "B's [PeerSync] log shows a pull via C's URL — sync was not direct A→B"; sync_ok=0
    else
      ok "B never used C's URL for its peer-sync pull — direct A→B confirmed"
    fi
  else
    warn "B did not receive the sentinel within the wait window"
  fi

  echo; log "── B's [PeerSync] re-pulled lines ──"
  grep "\[PeerSync\] re-pulled" "$(node_dir B)/red.log" | tail -6 || true
  echo

  # Bonus: subscribe B to a new repo over the healed link.
  log "seeding NEW repo $NEW_REPO on A (post-restart)…"
  curl -s -X POST "$(base_url A)/-/import" -H "X-Admin-Token: $(node_token A)" \
    -H "Content-Type: application/json" -d "{\"url\":\"$NEW_REPO\",\"saveToStartup\":true}" \
    -w '\n  → HTTP %{http_code}\n'
  log "subscribing B to A's NEW /$NEW_BUCKET over the rediscovered link…"
  curl -s -X POST "$(base_url B)/-/import" -H "X-Admin-Token: $(node_token B)" \
    -H "Content-Type: application/json" \
    -d "{\"peer_url\":\"$URL_A2\",\"remote_path\":\"$NEW_BUCKET\",\"filename\":\"$NEW_BUCKET\",\"saveToStartup\":true}" \
    -w '  → HTTP %{http_code}\n'
  local v2new; v2new="$(manifest_count B "$NEW_BUCKET")"
  [ "${v2new:-0}" -gt 0 ] && ok "B holds $v2new files of $NEW_BUCKET via healed link" \
                           || warn "Test-Repository not synced to B ($v2new)"

  ###########################################################################
  log "FINAL RESULTS"
  ###########################################################################
  local pass=1
  [ "$s1_pass" = 1 ] && ok "Scenario 1: gossip import (3-node, single call)" || { err "Scenario 1 FAILED"; pass=0; }
  [ "$seenA" = "$URL_A2" ] && ok "Scenario 2: B's DB has A's new URL" || { err "B's DB wrong for A"; pass=0; }
  [ "$seenB" = "$URL_B2" ] && ok "Scenario 2: A's DB has B's new URL" || { err "A's DB wrong for B"; pass=0; }
  grep -q "\[Resolve\] HEALED" "$(node_dir A)/red.log" && ok "A healed a peer via directory C" || warn "no HEALED line in A log"
  grep -q "\[Resolve\] HEALED" "$(node_dir B)/red.log" && ok "B healed a peer via directory C" || warn "no HEALED line in B log"
  grep -q "\[Resolve\] ANSWER" "$(node_dir C)/red.log" && ok "C served signed directory lookups" || { err "C never answered a resolve"; pass=0; }
  [ "$sync_ok" = 1 ] && ok "Scenario 2: content re-synced directly A→B after rediscovery" || { err "sync did not resume"; pass=0; }

  echo
  if [ "$pass" = 1 ]; then printf '%s3-NODE TEST: PASS%s\n' "$c_grn" "$c_rst"; return 0; fi

  printf '%s3-NODE TEST: FAIL%s\n' "$c_red" "$c_rst"
  # Post-mortem: print the federation-relevant log lines INTO the run output so
  # the evidence survives even if the state dir is wiped by `clean` afterwards.
  echo; log "── post-mortem: federation log excerpts (also in $STATE/<node>/red.log) ──"
  for n in A B C; do
    echo "--- node $n ---"
    grep -E "\[Resolve\]|\[Gossip\]|\[PeerSync\]|[Aa]nnounce|Heartbeat" "$(node_dir "$n")/red.log" 2>/dev/null | tail -25
  done
  return 1
}

case "${1:-}" in
  auto)  cmd_auto ;;
  clean) cmd_clean ;;
  *) echo "usage: $0 auto | clean" ;;
esac
