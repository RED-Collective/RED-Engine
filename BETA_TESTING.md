# RED Engine — Beta Testing Guide

This guide covers local and federated testing of RED Engine. It is not a
production-hardening guide — see `BULLETPROOFING_ROADMAP.md` before any public
exposure (rate limiting, token rotation, request timeouts, etc. are not yet done).

---

## 1. Start a node

### Single node

```bash
make run
```

Starts one node at `http://localhost:8080` using `data/` as the content directory.
The admin token defaults to `dev-token` (set `RED_ADMIN_TOKEN` to override).

### Two-node federation

```bash
make demo
```

| Node | URL                   | Content dir | Admin token   |
|------|-----------------------|-------------|---------------|
| A    | http://localhost:8080 | `data/`     | `dev-token-A` |
| B    | http://localhost:8081 | `data1/`    | `dev-token-B` |

Each node gets a separate identity and `registry.db` under `.red-demo/stateA` /
`.red-demo/stateB`. `Ctrl-C` stops both.

---

## 2. Content — folders as truth

RED Engine serves whatever is in `data/`. There is no taxonomy classification, no
vault type, no branch UIDs. **The folder structure is the taxonomy.**

- Drop a folder of Markdown files into `data/MyTopic/` → it becomes a Collection
  card on the home page.
- Nest folders freely: `data/Science/Physics/Mechanics/` → three levels of nav.
- A `RED_KNOWLEDGE.md` in any folder sets that folder's description.
- Images referenced via Obsidian `![[image.png]]` wikilinks are served automatically.

### Import content

Go to `/-/admin` → **Import**:

- **Git URL** (ending in `.git`): the engine clones the repo and mirrors it under
  `data/<destination-name>/`.
- **Raw URL** (a single Markdown file): downloaded once and stored directly.
- **Peer sync**: pull from a connected peer node (see section 4).

Imports are tracked in the registry and re-synced every minute.

---

## 3. Sign notes with RED-Feather

RED-Feather is the signing CLI / Obsidian plugin. Each note it signs gets these
frontmatter fields written in-place:

```yaml
red_author: <base64 ed25519 public key>
red_author_name: Alice
red_signed_at: Mon, 02 Jan 2006 15:04:05 MST
red_hash: <sha256 of body>
red_sig: <ed25519 signature>
```

The engine's 4-state verification system:

| State        | Meaning                                           |
|--------------|---------------------------------------------------|
| `verified`   | Valid signature by a key in this node's keyring   |
| `unverified` | Valid signature, but signer key not yet trusted   |
| `tampered`   | Signature present but body-hash mismatch          |
| `unsigned`   | No signature in frontmatter                       |

### Trust an author

1. Get the author's public key (from `red_author` frontmatter, or RED-Feather's
   "copy public key" button, or the admin panel's **Detected Signers** list).
2. `/-/admin` → **Contributors** → **Add** → paste the key + a name.
3. All notes by that key immediately show as **Verified**.

Peer-gossip authors are never auto-trusted. Trust is always an explicit admin act.

---

## 4. Federation — connect nodes

### Add a peer

On Node B's `/-/admin` → **Peers** → **Add**:
- URL: `http://localhost:8080`
- Peer type: `upstream` (you pull from them) / `downstream` (they push to you) /
  `mirror` (bidirectional)
- Import peers: optionally gossip-import A's known peers as well

Node B will verify A's identity via a signed nonce challenge before saving the peer.

### Sync content from a peer

`/-/admin` → **Import** → set Peer URL to the peer's address and Destination to
the local folder name. The engine uses the peer's signed `/-/sync/manifest` to
pull only the files you ask for.

### URL rediscovery (cloudflared / dynamic tunnels)

When a node's public URL changes (e.g. a new cloudflared quick-tunnel), it
announces the new URL to its downstream/mirror peers on startup and on every
heartbeat (10 min). If both endpoints restart simultaneously, a third peer is
queried to resolve the new URL. This is automatic.

Set `RED_PUBLIC_URL=https://your-tunnel.trycloudflare.com` before starting the
node, or write it to `red_public_url` in `config.json`.

---

## 5. Search

Full-text search over all notes is available at `/api/search?q=<query>`. It uses
SQLite FTS5 and returns up to 20 results with a highlighted snippet:

```json
[{"file_path": "Science/Physics/note", "title": "Newton's Laws", "snippet": "…<mark>force</mark>…"}]
```

The search bar in the UI (Cmd/Ctrl+K) uses this endpoint.

---

## 6. Backups

A zip snapshot of `data/` is created automatically on every startup. Up to 10
snapshots are kept under `<state-dir>/backups/`.

- On-demand: `POST /-/admin/backup` (requires `X-Admin-Token`) → returns
  `{name, path, size_bytes, created}`.
- List: `GET /-/admin/backups` → array of existing snapshots, newest first.
- Restore: `unzip <backup>.zip -d data/` then `POST /-/reload`.

---

## 7. End-to-end smoke checklist

```
[ ] make demo
[ ] Node A: drop a folder of .md files into data/ → Collections card appears
[ ] Node A: POST /-/admin/backup → zip created; GET /-/admin/backups lists it
[ ] Node B: add Node A as upstream peer → identity challenge succeeds
[ ] Node B: import from Node A → files appear under data/<dest>/
[ ] Node B: trust Node A's author key → signed notes show Verified
[ ] Search: /api/search?q=<word from a note> → returns results
[ ] Kill Node A; restart with a new RED_PUBLIC_URL; check Node B's peer list
    updates on the next heartbeat
```

---

## Known limitations

- No rate limiting or request-size caps (use only on trusted networks).
- Graceful shutdown is not implemented (SIGTERM terminates immediately).
- Trust is manual and per-author; there is no revocation propagation across peers.
- Git syncs do a full re-clone+reload rather than a per-file delta.
- Sync is additive within a destination folder; a manually-deleted file reappears
  on the next sync of its source (by design — use the admin Remove to clean up).
