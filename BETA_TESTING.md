# RED-Engine — Beta Testing Guide (local dev)

This guide is for **local-dev beta testing** of content sync, taxonomy foldering,
and federation. It assumes you run nodes on your own machine(s); it is **not** a
production/hardening guide (see `BULLETPROOFING_ROADMAP.md` before any public
exposure — token rotation, timeouts, rate limiting, etc. are intentionally *not*
done yet).

## 1. Start a two-node federation

```bash
make demo
```

This builds the binary and starts:

| Node | URL                     | Content dir | Admin token (default) |
|------|-------------------------|-------------|-----------------------|
| A    | http://localhost:8080   | `data/`     | `dev-token-A`         |
| B    | http://localhost:8081   | `data1/`    | `dev-token-B`         |

Each node gets its own identity + `registry.db` under `.red-demo/state{A,B}` (via
`RED_STATE_DIR`). Override tokens with `RED_ADMIN_TOKEN_A` / `RED_ADMIN_TOKEN_B`.
`Ctrl-C` stops both. (Single node: `make run`.)

## 2. Classify a vault in RED-Feather

A **vault = one knowledge branch.** In Obsidian (RED-Feather plugin) or via the
`red-feather` CLI, set the vault's classification — this writes
`signer.db.vault_metadata`:

- `vault_type`: `library` (taxonomy-filed) or `manual` (guide).
- `taxonomy_uid`: a core node (`c<id>`, see `--list-taxonomy`) or a **community
  branch** you create with `--create-branch --parent-uid <uid> --name <Name>`
  (its signed JSON goes into `branch_record`).

The engine reads this as **authoritative** — every note in the vault is filed under
that branch, regardless of any per-file `red_taxonomy_uid` frontmatter (which can be
stale).

## 3. Sync content onto a node

On a node's `/-/admin` → **Import**:

- **Git/URL import:** paste the vault's git URL. The engine clones it, reads the
  classification, registers any community branches, and files notes under
  `data/<vault_type>/<root…leaf>/`. Example: a `library` vault on the `api` branch
  (under Web Development) →
  `data/library/applied-sciences/computer-science/web-development/api/`.
- **Peer sync:** set Peer URL = the other node, Remote path = a bucket (`library`)
  or a sub-branch path. The puller fetches that node's manifest, registers the
  branches, and mirrors the same branch→leaf tree locally.

## 4. Make synced content **verify** (manual trust step)

Synced notes show **"Untrusted Key / Unverified"** until you trust their author —
this is expected in beta (trust is manual by design for now).

1. Find the note's **author public key**. red-engine now surfaces it: the
   `GET /api/content?path=…` response includes `author_key` (and
   `verification_error`) for any signed note. (Also available via red-feather's
   "copy public key" or the note's `red_author` frontmatter line.)
2. On the node: `/-/admin` → **Contributors** → **Add** → paste the pubkey + a name.
3. Reload — the note now renders **Verified** (the signature is checked against the
   trusted key; tampering shows as "Hash Mismatch").

> Peer-gossip authors are **not** auto-trusted. You trust each author explicitly.

## 5. Reclassify / remove (now safe)

- **Reclassify a vault** (e.g. you moved it from Web Development to a new `api`
  branch) and re-sync: the engine cleans up the note's old branch location and
  files it under the new one — no leftover duplicates. (Per-source *sync ledger*.)
- **Remove a tracked sync** with "delete local files": deletes **only that
  source's** notes (and its hidden git cache), never the shared bucket that other
  vaults also write into.

## 6. End-to-end smoke (what to test)

1. `make demo`.
2. Node A: import a RED-Feather vault → confirm the chain at
   `data/<vault_type>/…/<leaf>/`.
3. Node B: `/-/admin` → Peers → add `http://localhost:8080`; then Import → peer sync
   `library` → B reproduces the same tree (and the community branch appears in B's
   taxonomy).
4. On B, trust the author (step 4) → notes verify.
5. Reclassify the vault in RED-Feather, re-sync A → old-branch copy is gone.
6. Remove the sync on A with delete-files → only that vault's notes go.

## Known limitations in this beta

- **No production hardening yet:** no rate limiting, request timeouts, body-size
  limits, or graceful shutdown; the admin token is read from config/env (rotate
  before any exposure). Run on trusted/local networks only.
- **Trust is manual + per-author** (no TOFU, no revocation propagation).
- **Search** uses the in-memory nav index; the `content_index`/FTS tables exist but
  are not yet populated (no full-text/tag search).
- **Sync is additive across sources** within a bucket; cleanup is per-source via the
  ledger (a manually-deleted file reappears on the next sync of its source).
- `git` vault syncs do a full re-clone+reload (no per-file delta) — fine at vault
  scale.
