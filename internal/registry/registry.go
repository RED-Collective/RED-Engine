package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// registrySchemaVersion is the current schema generation. It is stored in
// node_settings under "registry_schema_version" and gates breaking migrations.
const registrySchemaVersion = 6

var (
	db   *sql.DB
	once sync.Once
)

var errNotInit = errors.New("registry not initialised")

// execer is satisfied by both *sql.DB and *sql.Tx so schema helpers can run
// either directly or inside a migration transaction.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type Peer struct {
	ID              int        `json:"id"`
	URL             string     `json:"url"`
	PublicKey       string     `json:"public_key"` // node_public_key — stable identity anchor
	Name            string     `json:"name"`
	PeerType        string     `json:"peer_type"` // "upstream", "downstream", "mirror"
	Description     string     `json:"description"`
	PublicURL       string     `json:"public_url"`
	TunnelType      string     `json:"tunnel_type"` // "", "direct", "cloudflare_quick", "cloudflare_named"
	IsOnline        bool       `json:"is_online"`
	OnlineCheckedAt *time.Time `json:"online_checked_at,omitempty"`
	ExportedPaths   []string   `json:"exported_paths"`
	LastSeen        time.Time  `json:"last_seen"`
	AddedAt         time.Time  `json:"added_at"`
}

type StartupSync struct {
	ID       int    `json:"id"`
	URL      string `json:"url"`
	Filename string `json:"filename"`
	SyncType string `json:"sync_type"`
	// PeerKey and RemotePath are set only for sync_type='peer' entries. PeerKey
	// is the source peer's node public key — the stable anchor used to look up the
	// peer's CURRENT url at sync time, so a peer that moved (e.g. a new cloudflared
	// tunnel) is still re-pulled. RemotePath is the content subtree to pull.
	PeerKey      string     `json:"peer_key,omitempty"`
	RemotePath   string     `json:"remote_path,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	LastError    string     `json:"last_error"`
	SyncStatus   string     `json:"sync_status"`
	AddedAt      time.Time  `json:"added_at"`
}

// InitRegistry opens the database and runs migrations. Safe to call repeatedly.
func InitRegistry(dataDir string) error {
	var initErr error
	once.Do(func() {
		dbPath := filepath.Join(dataDir, "registry.db")
		// A busy timeout makes a blocked writer wait-and-retry instead of failing
		// immediately with SQLITE_BUSY, and capping the pool at one connection
		// serializes all access through Go so concurrent federation writes can never
		// collide on the file: an explicit peer add races the reciprocal
		// /-/announce/downstream it triggers, and the announce + peer-sync timers
		// write on their own schedule. SQLite is single-writer anyway, so a
		// one-connection pool costs nothing here while removing the SQLITE_BUSY/
		// CANTOPEN races seen on the directory node during the 3-node live test.
		dsn := dbPath + "?_pragma=busy_timeout(5000)"
		db, initErr = sql.Open("sqlite", dsn)
		if initErr != nil {
			return
		}
		db.SetMaxOpenConns(1)
		initErr = migrate(db)
	})
	return initErr
}

// migrate brings the schema up to registrySchemaVersion. Backward-compatible
// table/index creation is unconditional; breaking changes to peers/startup_sync
// (dropping the url UNIQUE constraint, adding CHECK constraints) are applied once
// for legacy databases by recreating the table and copying rows over.
func migrate(db *sql.DB) error {
	// node_settings holds the schema version, so it must exist first.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS node_settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		return err
	}

	ver := schemaVersionValue(db)

	// peers — fresh databases get the v2 schema directly; legacy ones are recreated.
	var capturedPaths map[int64][]string
	if !tableExists(db, "peers") {
		if err := createPeersTable(db); err != nil {
			return err
		}
	} else if ver < 2 {
		paths, err := migratePeersToV2(db)
		if err != nil {
			return err
		}
		capturedPaths = paths
	}

	// startup_sync — same treatment.
	if !tableExists(db, "startup_sync") {
		if err := createStartupSyncTable(db); err != nil {
			return err
		}
	} else if ver < 2 {
		if err := migrateStartupSyncToV2(db); err != nil {
			return err
		}
	}

	// v6: peer syncs are now anchored to the source peer's identity (peer_key)
	// plus the content subtree (remote_path), so periodic re-pull follows the peer
	// to its current url after a tunnel restart. Add the columns to legacy tables
	// (fresh ones already have them from createStartupSyncTable). Idempotent.
	if err := ensureColumn(db, "startup_sync", "peer_key", "TEXT DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(db, "startup_sync", "remote_path", "TEXT DEFAULT ''"); err != nil {
		return err
	}

	// Auxiliary tables introduced in v2 (safe to create any time).
	if err := createAuxTables(db); err != nil {
		return err
	}

	// Drop the removed branch/taxonomy layer. IF EXISTS makes this a no-op on a
	// fresh database; on an upgrade it deletes the old taxonomy and manual-tag
	// tables for good.
	if _, err := db.Exec(`
		DROP TABLE IF EXISTS taxonomy;
		DROP TABLE IF EXISTS manual_tags;
	`); err != nil {
		return fmt.Errorf("drop branch tables: %w", err)
	}

	// Contributor keyring — the admin-recognized signer keys that make a valid
	// signature read as "verified" rather than "unverified". Create it, migrate any
	// rows from the legacy trusted_authors table (no author identity, just the
	// pubkey + label), then drop that table.
	if err := createContributorsTable(db); err != nil {
		return err
	}
	if tableExists(db, "trusted_authors") {
		if _, err := db.Exec(`
			INSERT OR IGNORE INTO contributors (public_key, name, revoked)
			SELECT public_key, COALESCE(name,''), COALESCE(revoked,0) FROM trusted_authors`); err != nil {
			return fmt.Errorf("migrate trusted_authors: %w", err)
		}
		if _, err := db.Exec(`DROP TABLE trusted_authors`); err != nil {
			return fmt.Errorf("drop trusted_authors: %w", err)
		}
	}

	// v5: drop the abandoned full-text-search / content-index / tags layer — the
	// remnants of the superseded taxonomy+FTS architecture. Search now runs off
	// the in-memory store.BuildSearchIndex, so none of these tables are ever read
	// or written. IF EXISTS makes this a no-op on a fresh database; on an upgrade
	// it removes the dead tables for good. nav_folder_tags is dropped before
	// content_tags to respect the foreign key.
	if _, err := db.Exec(`
		DROP TABLE IF EXISTS content_fts;
		DROP TABLE IF EXISTS content_index;
		DROP TABLE IF EXISTS nav_folder_tags;
		DROP TABLE IF EXISTS content_tags;
	`); err != nil {
		return fmt.Errorf("drop legacy content/fts/tags tables: %w", err)
	}

	// Backfill the exported-paths junction from captured legacy JSON now that
	// both peers and peer_exported_paths exist.
	for peerID, paths := range capturedPaths {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if _, err := db.Exec(
				`INSERT OR IGNORE INTO peer_exported_paths (peer_id, path) VALUES (?, ?)`,
				peerID, p); err != nil {
				return err
			}
		}
	}

	if ver < registrySchemaVersion {
		if err := setSchemaVersionValue(db, registrySchemaVersion); err != nil {
			return err
		}
	}
	return nil
}

func createPeersTable(e execer) error {
	_, err := e.Exec(`
		CREATE TABLE IF NOT EXISTS peers (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			url               TEXT    NOT NULL,
			node_public_key   TEXT    UNIQUE,
			name              TEXT    NOT NULL DEFAULT '',
			peer_type         TEXT    NOT NULL DEFAULT 'upstream'
			                          CHECK(peer_type IN ('upstream','downstream','mirror')),
			description       TEXT    DEFAULT '',
			public_url        TEXT    DEFAULT '',
			tunnel_type       TEXT    DEFAULT ''
			                          CHECK(tunnel_type IN ('','direct','cloudflare_quick','cloudflare_named')),
			is_online         BOOLEAN DEFAULT 0,
			online_checked_at DATETIME,
			last_seen         DATETIME,
			added_at          DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_peers_url    ON peers(url);
		CREATE INDEX IF NOT EXISTS idx_peers_pubkey ON peers(node_public_key);
	`)
	return err
}

func createStartupSyncTable(e execer) error {
	_, err := e.Exec(`
		CREATE TABLE IF NOT EXISTS startup_sync (
			id             INTEGER  PRIMARY KEY AUTOINCREMENT,
			url            TEXT     NOT NULL,
			filename       TEXT     UNIQUE NOT NULL,
			sync_type      TEXT     DEFAULT 'auto'
			                        CHECK(sync_type IN ('auto','git','tar.gz','zip','raw','peer')),
			peer_key       TEXT     DEFAULT '',
			remote_path    TEXT     DEFAULT '',
			last_synced_at DATETIME,
			last_error     TEXT     DEFAULT '',
			sync_status    TEXT     DEFAULT 'pending'
			                        CHECK(sync_status IN ('pending','ok','error','disabled')),
			added_at       DATETIME DEFAULT CURRENT_TIMESTAMP
		)`)
	return err
}

// createContributorsTable creates the contributor keyring: the set of signer
// public keys an admin recognizes on this node. There is no author identity —
// `name` is just an admin-facing label, never shown on the public verification
// badge. A non-revoked key here is what turns a valid signature "verified".
func createContributorsTable(e execer) error {
	_, err := e.Exec(`
		CREATE TABLE IF NOT EXISTS contributors (
			public_key TEXT     PRIMARY KEY,
			name       TEXT     NOT NULL DEFAULT '',
			added_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			revoked    INTEGER  NOT NULL DEFAULT 0,
			revoked_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_contributors_revoked ON contributors(revoked);
	`)
	return err
}

// RecognizedContributorKeys returns the set of non-revoked contributor public
// keys (lowercased hex) this node recognizes. A cryptographically valid signature
// reads as "verified" only when its signer key is in this set; otherwise it is
// "unverified" (intact, but not vouched for by this node).
func RecognizedContributorKeys() map[string]bool {
	out := make(map[string]bool)
	db := GetDB()
	if db == nil {
		return out
	}
	rows, err := db.Query(`SELECT public_key FROM contributors WHERE revoked = 0`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil && k != "" {
			out[strings.ToLower(k)] = true
		}
	}
	return out
}

func createAuxTables(e execer) error {
	_, err := e.Exec(`
		CREATE TABLE IF NOT EXISTS peer_exported_paths (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			peer_id INTEGER NOT NULL REFERENCES peers(id) ON DELETE CASCADE,
			path    TEXT    NOT NULL,
			UNIQUE(peer_id, path)
		);
		CREATE INDEX IF NOT EXISTS idx_peer_paths_peer ON peer_exported_paths(peer_id);

		CREATE TABLE IF NOT EXISTS peer_health_history (
			id         INTEGER  PRIMARY KEY AUTOINCREMENT,
			peer_id    INTEGER  NOT NULL REFERENCES peers(id) ON DELETE CASCADE,
			checked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_online  BOOLEAN  NOT NULL,
			latency_ms INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_health_history_peer ON peer_health_history(peer_id, checked_at DESC);
	`)
	return err
}

// migratePeersToV2 recreates the peers table with the v2 schema, copying legacy
// rows (preserving ids) and returning the exported_paths JSON captured per peer
// id so the caller can populate peer_exported_paths afterwards.
func migratePeersToV2(db *sql.DB) (map[int64][]string, error) {
	type oldPeer struct {
		id                 int64
		url, pk, name, typ string
		paths              []string
		lastSeen, addedAt  sql.NullTime
	}

	rows, err := db.Query(`
		SELECT id, url, COALESCE(public_key,''), COALESCE(name,''),
		       COALESCE(peer_type,'upstream'), COALESCE(exported_paths,''),
		       last_seen, added_at
		FROM peers`)
	if err != nil {
		return nil, err
	}
	var olds []oldPeer
	for rows.Next() {
		var op oldPeer
		var pathsJSON string
		if err := rows.Scan(&op.id, &op.url, &op.pk, &op.name, &op.typ, &pathsJSON, &op.lastSeen, &op.addedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if pathsJSON != "" {
			json.Unmarshal([]byte(pathsJSON), &op.paths)
		}
		op.typ = normalizePeerType(op.typ)
		olds = append(olds, op)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE peers RENAME TO peers_old`); err != nil {
		return nil, err
	}
	if err := createPeersTable(tx); err != nil {
		return nil, err
	}

	captured := make(map[int64][]string, len(olds))
	for _, op := range olds {
		if _, err := tx.Exec(`
			INSERT INTO peers (id, url, node_public_key, name, peer_type, last_seen, added_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			op.id, op.url, nullIfEmpty(op.pk), op.name, op.typ,
			nullTimeVal(op.lastSeen), nullTimeVal(op.addedAt)); err != nil {
			return nil, err
		}
		if len(op.paths) > 0 {
			captured[op.id] = op.paths
		}
	}

	if _, err := tx.Exec(`DROP TABLE peers_old`); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return captured, nil
}

// migrateStartupSyncToV2 recreates startup_sync with the v2 schema, preserving rows.
func migrateStartupSyncToV2(db *sql.DB) error {
	type oldSync struct {
		id            int64
		url, filename string
		addedAt       sql.NullTime
	}

	rows, err := db.Query(`SELECT id, url, filename, added_at FROM startup_sync`)
	if err != nil {
		return err
	}
	var olds []oldSync
	for rows.Next() {
		var os oldSync
		if err := rows.Scan(&os.id, &os.url, &os.filename, &os.addedAt); err != nil {
			rows.Close()
			return err
		}
		olds = append(olds, os)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE startup_sync RENAME TO startup_sync_old`); err != nil {
		return err
	}
	if err := createStartupSyncTable(tx); err != nil {
		return err
	}
	for _, os := range olds {
		if _, err := tx.Exec(`
			INSERT INTO startup_sync (id, url, filename, sync_type, added_at)
			VALUES (?, ?, ?, ?, ?)`,
			os.id, os.url, os.filename, detectSyncType(os.url), nullTimeVal(os.addedAt)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DROP TABLE startup_sync_old`); err != nil {
		return err
	}
	return tx.Commit()
}

// GetSetting reads a node setting by key. Returns "" if the key does not exist.
func GetSetting(key string) string {
	if db == nil {
		return ""
	}
	var val string
	db.QueryRow(`SELECT value FROM node_settings WHERE key = ?`, key).Scan(&val)
	return val
}

// SetSetting writes a node setting, inserting or replacing as needed.
func SetSetting(key, value string) error {
	if db == nil {
		return errNotInit
	}
	_, err := db.Exec(`
		INSERT INTO node_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

// AddPeer inserts or updates a peer. Identity resolves by node_public_key when
// known (the stable anchor), otherwise by URL. Empty fields on update preserve
// the existing value so a lightweight refresh never clobbers richer metadata.
func AddPeer(p Peer) error {
	if db == nil {
		return errNotInit
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var peerID int64
	found := false
	if p.PublicKey != "" {
		err := tx.QueryRow(`SELECT id FROM peers WHERE node_public_key = ?`, p.PublicKey).Scan(&peerID)
		if err == nil {
			found = true
		} else if err != sql.ErrNoRows {
			return err
		}
	}
	if !found {
		err := tx.QueryRow(`SELECT id FROM peers WHERE url = ?`, p.URL).Scan(&peerID)
		if err == nil {
			found = true
		} else if err != sql.ErrNoRows {
			return err
		}
	}

	if found {
		if _, err := tx.Exec(`
			UPDATE peers SET
				url             = ?,
				node_public_key = COALESCE(?, node_public_key),
				name            = CASE WHEN ? = '' THEN name        ELSE ? END,
				peer_type       = CASE WHEN ? = '' THEN peer_type   ELSE ? END,
				description     = CASE WHEN ? = '' THEN description  ELSE ? END,
				public_url      = CASE WHEN ? = '' THEN public_url   ELSE ? END,
				tunnel_type     = CASE WHEN ? = '' THEN tunnel_type  ELSE ? END,
				last_seen       = COALESCE(?, last_seen)
			WHERE id = ?`,
			p.URL, nullIfEmpty(p.PublicKey),
			p.Name, p.Name,
			p.PeerType, p.PeerType,
			p.Description, p.Description,
			p.PublicURL, p.PublicURL,
			p.TunnelType, p.TunnelType,
			nullableTime(p.LastSeen), peerID); err != nil {
			return err
		}
	} else {
		addedAt := p.AddedAt
		if addedAt.IsZero() {
			addedAt = time.Now()
		}
		res, err := tx.Exec(`
			INSERT INTO peers (url, node_public_key, name, peer_type, description, public_url, tunnel_type, last_seen, added_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.URL, nullIfEmpty(p.PublicKey), p.Name, normalizePeerType(p.PeerType),
			p.Description, p.PublicURL, p.TunnelType, nullableTime(p.LastSeen), addedAt)
		if err != nil {
			return err
		}
		peerID, _ = res.LastInsertId()
	}

	// Replace exported paths when the caller supplied them.
	if p.ExportedPaths != nil {
		if _, err := tx.Exec(`DELETE FROM peer_exported_paths WHERE peer_id = ?`, peerID); err != nil {
			return err
		}
		for _, path := range p.ExportedPaths {
			if path == "" {
				continue
			}
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO peer_exported_paths (peer_id, path) VALUES (?, ?)`,
				peerID, path); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

const peerColumns = `id, url, COALESCE(node_public_key,''), name, peer_type,
	COALESCE(description,''), COALESCE(public_url,''), COALESCE(tunnel_type,''),
	is_online, online_checked_at, last_seen, added_at`

func scanPeer(rows interface {
	Scan(dest ...any) error
}) (Peer, error) {
	var p Peer
	var checked, lastSeen, addedAt sql.NullTime
	if err := rows.Scan(&p.ID, &p.URL, &p.PublicKey, &p.Name, &p.PeerType,
		&p.Description, &p.PublicURL, &p.TunnelType,
		&p.IsOnline, &checked, &lastSeen, &addedAt); err != nil {
		return p, err
	}
	if checked.Valid {
		t := checked.Time
		p.OnlineCheckedAt = &t
	}
	if lastSeen.Valid {
		p.LastSeen = lastSeen.Time
	}
	if addedAt.Valid {
		p.AddedAt = addedAt.Time
	}
	return p, nil
}

// ListPeers returns all peers with their exported paths populated.
func ListPeers() ([]Peer, error) {
	if db == nil {
		return nil, errNotInit
	}
	rows, err := db.Query(`SELECT ` + peerColumns + ` FROM peers ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var peers []Peer
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range peers {
		paths, err := loadExportedPaths(peers[i].ID)
		if err != nil {
			return nil, err
		}
		peers[i].ExportedPaths = paths
	}
	return peers, nil
}

// ListPeersByType returns peers whose peer_type is in the given set.
func ListPeersByType(types ...string) ([]Peer, error) {
	if db == nil {
		return nil, errNotInit
	}
	if len(types) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(types)), ",")
	args := make([]any, len(types))
	for i, t := range types {
		args[i] = t
	}
	rows, err := db.Query(
		`SELECT `+peerColumns+` FROM peers WHERE peer_type IN (`+placeholders+`) ORDER BY added_at DESC`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var peers []Peer
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range peers {
		paths, _ := loadExportedPaths(peers[i].ID)
		peers[i].ExportedPaths = paths
	}
	return peers, nil
}

// GetPeerByURL retrieves a single peer by its current URL.
func GetPeerByURL(url string) (*Peer, error) {
	if db == nil {
		return nil, errNotInit
	}
	p, err := scanPeer(db.QueryRow(`SELECT `+peerColumns+` FROM peers WHERE url = ?`, url))
	if err != nil {
		return nil, err
	}
	p.ExportedPaths, _ = loadExportedPaths(p.ID)
	return &p, nil
}

// GetPeerByPublicKey retrieves a peer by its stable Ed25519 public key.
func GetPeerByPublicKey(publicKey string) (*Peer, error) {
	if db == nil {
		return nil, errNotInit
	}
	if publicKey == "" {
		return nil, sql.ErrNoRows
	}
	row := db.QueryRow(`SELECT `+peerColumns+` FROM peers WHERE node_public_key = ?`, publicKey)
	p, err := scanPeer(row)
	if err != nil {
		return nil, err
	}
	p.ExportedPaths, _ = loadExportedPaths(p.ID)
	return &p, nil
}

// loadExportedPaths returns the content paths a peer advertises, from the junction table.
func loadExportedPaths(peerID int) ([]string, error) {
	rows, err := db.Query(`SELECT path FROM peer_exported_paths WHERE peer_id = ? ORDER BY path`, peerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// UpdatePeerURL rewrites a peer's URL, keyed by its stable public key. Used by the
// challenge-response announce flow when a peer's tunnel URL changes.
func UpdatePeerURL(publicKey, newURL string) error {
	if db == nil {
		return errNotInit
	}
	res, err := db.Exec(
		`UPDATE peers SET url = ?, last_seen = CURRENT_TIMESTAMP WHERE node_public_key = ?`,
		newURL, publicKey)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetPeerHealth caches a peer's online status and records a health-history sample.
func SetPeerHealth(peerID int, online bool, latencyMs int) error {
	if db == nil {
		return errNotInit
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE peers SET is_online = ?, online_checked_at = CURRENT_TIMESTAMP WHERE id = ?`,
		online, peerID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO peer_health_history (peer_id, is_online, latency_ms) VALUES (?, ?, ?)`,
		peerID, online, latencyMs); err != nil {
		return err
	}
	// Keep only the 100 most recent rows per peer to prevent unbounded growth
	// (~525,600 rows/year for 10 peers at 1-min intervals without this guard).
	if _, err := tx.Exec(`
		DELETE FROM peer_health_history
		WHERE peer_id = ? AND id NOT IN (
			SELECT id FROM peer_health_history
			WHERE peer_id = ?
			ORDER BY checked_at DESC
			LIMIT 100
		)`, peerID, peerID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePeer removes a peer by URL, cascading its exported paths and health rows.
func DeletePeer(url string) error {
	if db == nil {
		return errNotInit
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var peerID int
	if err := tx.QueryRow(`SELECT id FROM peers WHERE url = ?`, url).Scan(&peerID); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	for _, stmt := range []string{
		`DELETE FROM peer_exported_paths WHERE peer_id = ?`,
		`DELETE FROM peer_health_history WHERE peer_id = ?`,
		`DELETE FROM peers WHERE id = ?`,
	} {
		if _, err := tx.Exec(stmt, peerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneDeadPeers removes peers that have been continuously offline for more than
// offlineDays days. Peers that have never been health-checked are not pruned.
// Child rows (exported paths, health history) are cascaded in the same
// transaction so a later peer that reuses a freed SQLite rowid can never inherit
// a pruned peer's exported paths or health samples.
func PruneDeadPeers(offlineDays int) (int, error) {
	if db == nil {
		return 0, errNotInit
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	const deadFilter = `
		FROM peers
		WHERE is_online = 0
		  AND online_checked_at IS NOT NULL
		  AND online_checked_at < datetime('now', ?)`
	cutoff := fmt.Sprintf("-%d days", offlineDays)

	for _, child := range []string{
		`DELETE FROM peer_exported_paths WHERE peer_id IN (SELECT id ` + deadFilter + `)`,
		`DELETE FROM peer_health_history WHERE peer_id IN (SELECT id ` + deadFilter + `)`,
	} {
		if _, err := tx.Exec(child, cutoff); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(`DELETE `+deadFilter, cutoff)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ListAllExportedPaths returns the distinct set of content paths advertised across
// all known peers — the union of topics reachable through the network.
func ListAllExportedPaths() ([]string, error) {
	if db == nil {
		return nil, errNotInit
	}
	rows, err := db.Query(`SELECT DISTINCT path FROM peer_exported_paths ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// GetDB returns the underlying SQLite database handle.
func GetDB() *sql.DB {
	return db
}

// ListStartupSync returns all startup sync entries ordered by insertion time.
func ListStartupSync() ([]StartupSync, error) {
	if db == nil {
		return nil, errNotInit
	}
	rows, err := db.Query(`
		SELECT id, url, filename, COALESCE(sync_type,'auto'),
		       COALESCE(peer_key,''), COALESCE(remote_path,''), last_synced_at,
		       COALESCE(last_error,''), COALESCE(sync_status,'pending'), added_at
		FROM startup_sync ORDER BY added_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []StartupSync
	for rows.Next() {
		var s StartupSync
		var lastSynced, addedAt sql.NullTime
		if err := rows.Scan(&s.ID, &s.URL, &s.Filename, &s.SyncType,
			&s.PeerKey, &s.RemotePath, &lastSynced,
			&s.LastError, &s.SyncStatus, &addedAt); err != nil {
			return nil, err
		}
		if lastSynced.Valid {
			t := lastSynced.Time
			s.LastSyncedAt = &t
		}
		if addedAt.Valid {
			s.AddedAt = addedAt.Time
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// AddStartupSync inserts or updates a startup sync entry (keyed by filename),
// detecting and storing its sync_type from the URL.
func AddStartupSync(url, filename string) error {
	if db == nil {
		return errNotInit
	}
	_, err := db.Exec(`
		INSERT INTO startup_sync (url, filename, sync_type)
		VALUES (?, ?, ?)
		ON CONFLICT(filename) DO UPDATE SET
			url       = excluded.url,
			sync_type = excluded.sync_type
	`, url, filename, detectSyncType(url))
	return err
}

// AddPeerStartupSync records (or updates) a peer-based startup sync. Unlike a
// URL sync, it is anchored to the source peer's identity (peerKey) plus the
// content subtree (remotePath); the periodic peer-sync loop resolves the peer's
// CURRENT url from that key at pull time, so the content keeps syncing after the
// peer moves to a new (e.g. cloudflared quick) tunnel URL. peerURL is stored only
// as a human-readable hint of where the peer was last seen.
func AddPeerStartupSync(peerKey, peerURL, remotePath, filename string) error {
	if db == nil {
		return errNotInit
	}
	_, err := db.Exec(`
		INSERT INTO startup_sync (url, filename, sync_type, peer_key, remote_path)
		VALUES (?, ?, 'peer', ?, ?)
		ON CONFLICT(filename) DO UPDATE SET
			url         = excluded.url,
			sync_type   = 'peer',
			peer_key    = excluded.peer_key,
			remote_path = excluded.remote_path
	`, peerURL, filename, peerKey, remotePath)
	return err
}

// MarkSyncResult records the outcome of a sync attempt for admin visibility.
func MarkSyncResult(filename, status, errMsg string) error {
	if db == nil {
		return errNotInit
	}
	_, err := db.Exec(`
		UPDATE startup_sync SET
			sync_status    = ?,
			last_error     = ?,
			last_synced_at = CASE WHEN ? = 'ok' THEN CURRENT_TIMESTAMP ELSE last_synced_at END
		WHERE filename = ?`, status, errMsg, status, filename)
	return err
}

// RemoveStartupSync deletes a startup sync entry by filename.
func RemoveStartupSync(filename string) error {
	if db == nil {
		return errNotInit
	}
	_, err := db.Exec(`DELETE FROM startup_sync WHERE filename = ?`, filename)
	return err
}

// ── helpers ──────────────────────────────────────────────────────────────

func tableExists(db *sql.DB, name string) bool {
	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	return err == nil
}

// columnExists reports whether table has a column called col.
func columnExists(db *sql.DB, table, col string) bool {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && name == col {
			return true
		}
	}
	return false
}

// ensureColumn adds col (with the given type/default DDL) to table if missing.
// SQLite's ALTER TABLE ADD COLUMN is the idempotent way to evolve a table in
// place without recreating it; guarding on columnExists keeps it re-runnable.
func ensureColumn(db *sql.DB, table, col, ddl string) error {
	if columnExists(db, table, col) {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` ` + ddl)
	return err
}

func schemaVersionValue(db *sql.DB) int {
	var val string
	db.QueryRow(`SELECT value FROM node_settings WHERE key = 'registry_schema_version'`).Scan(&val)
	if val == "" {
		return 0
	}
	v, _ := strconv.Atoi(val)
	return v
}

func setSchemaVersionValue(db *sql.DB, v int) error {
	_, err := db.Exec(`
		INSERT INTO node_settings (key, value) VALUES ('registry_schema_version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(v))
	return err
}

func normalizePeerType(t string) string {
	switch t {
	case "upstream", "downstream", "mirror":
		return t
	default:
		return "upstream"
	}
}

func detectSyncType(u string) string {
	lower := strings.ToLower(u)
	switch {
	case strings.HasSuffix(lower, ".git"):
		return "git"
	case strings.HasSuffix(lower, ".tar.gz"):
		return "tar.gz"
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	default:
		return "raw"
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullTimeVal(nt sql.NullTime) any {
	if !nt.Valid {
		return nil
	}
	return nt.Time
}
