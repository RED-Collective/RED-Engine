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

	"github.com/RED-Collective/red-engine/internal/taxonomy"
	_ "modernc.org/sqlite"
)

// registrySchemaVersion is the current schema generation. It is stored in
// node_settings under "registry_schema_version" and gates breaking migrations.
const registrySchemaVersion = 2

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
	ID           int        `json:"id"`
	URL          string     `json:"url"`
	Filename     string     `json:"filename"`
	SyncType     string     `json:"sync_type"`
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
		db, initErr = sql.Open("sqlite", dbPath)
		if initErr != nil {
			return
		}
		initErr = migrate(db)
		if initErr != nil {
			return
		}
		initErr = seedTaxonomy(db)
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

	// trusted_authors is unchanged across versions.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS trusted_authors (
			public_key    TEXT PRIMARY KEY,
			name          TEXT NOT NULL,
			imported_from TEXT,
			imported_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			revoked       BOOLEAN DEFAULT 0,
			revoked_at    DATETIME,
			signature     TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_trusted_authors_name          ON trusted_authors(name);
		CREATE INDEX IF NOT EXISTS idx_trusted_authors_imported_from ON trusted_authors(imported_from);
	`); err != nil {
		return err
	}

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

	// Auxiliary tables introduced in v2 (safe to create any time).
	if err := createAuxTables(db); err != nil {
		return err
	}

	// Taxonomy, manual tags, content index, and full-text search.
	if err := createTaxonomyTables(db); err != nil {
		return err
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
			last_synced_at DATETIME,
			last_error     TEXT     DEFAULT '',
			sync_status    TEXT     DEFAULT 'pending'
			                        CHECK(sync_status IN ('pending','ok','error','disabled')),
			added_at       DATETIME DEFAULT CURRENT_TIMESTAMP
		)`)
	return err
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

		CREATE TABLE IF NOT EXISTS content_tags (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT    NOT NULL UNIQUE
		);
		CREATE TABLE IF NOT EXISTS nav_folder_tags (
			folder_id INTEGER NOT NULL,
			tag_id    INTEGER NOT NULL REFERENCES content_tags(id) ON DELETE CASCADE,
			PRIMARY KEY (folder_id, tag_id)
		);
	`)
	return err
}

// createTaxonomyTables creates the fixed-taxonomy, manual-tag, content-index,
// and full-text-search tables. content_fts is a standalone FTS5 table (not an
// external-content table): the content indexer owns its rows directly, so there
// are deliberately no sync triggers here.
func createTaxonomyTables(e execer) error {
	_, err := e.Exec(`
		CREATE TABLE IF NOT EXISTS taxonomy (
			uid        TEXT    PRIMARY KEY,           -- stable federation id: 'c<id>' core, 'u<hash>' community
			id         INTEGER,                       -- permanent integer (core nodes only)
			name       TEXT    NOT NULL,
			slug       TEXT    NOT NULL,
			parent_uid TEXT    REFERENCES taxonomy(uid),
			depth      INTEGER NOT NULL DEFAULT 0,
			source     TEXT    NOT NULL DEFAULT 'core'
			                   CHECK(source IN ('core','community')),
			created_by TEXT,                          -- author pubkey (community)
			signature  TEXT,                          -- ed25519 over the canonical record (community)
			created_at INTEGER,
			verified   BOOLEAN NOT NULL DEFAULT 0,    -- core=1; community=1 when signer is a trusted author
			UNIQUE(parent_uid, slug)
		);
		CREATE INDEX IF NOT EXISTS idx_taxonomy_parent ON taxonomy(parent_uid);

		CREATE TABLE IF NOT EXISTS manual_tags (
			id   INTEGER PRIMARY KEY,
			name TEXT    NOT NULL UNIQUE
		);

		CREATE TABLE IF NOT EXISTS content_index (
			id            INTEGER PRIMARY KEY,
			path          TEXT    UNIQUE NOT NULL,
			title         TEXT    NOT NULL DEFAULT '',
			content_type  TEXT    NOT NULL DEFAULT 'unknown'
			                      CHECK(content_type IN ('library','manual','unknown')),
			taxonomy_uid  TEXT    REFERENCES taxonomy(uid),
			guide_name    TEXT,
			guide_order   INTEGER,
			tag1_id       INTEGER REFERENCES manual_tags(id),
			tag2_id       INTEGER REFERENCES manual_tags(id),
			author_pubkey TEXT,
			author_name   TEXT,
			verified      BOOLEAN NOT NULL DEFAULT 0,
			file_hash     TEXT,
			signed_at     INTEGER,
			indexed_at    INTEGER NOT NULL DEFAULT (strftime('%s','now'))
		);
		CREATE INDEX IF NOT EXISTS idx_ci_taxonomy ON content_index(taxonomy_uid);
		CREATE INDEX IF NOT EXISTS idx_ci_guide    ON content_index(guide_name, guide_order);
		CREATE INDEX IF NOT EXISTS idx_ci_author   ON content_index(author_pubkey);
		CREATE INDEX IF NOT EXISTS idx_ci_type     ON content_index(content_type, verified);
		CREATE INDEX IF NOT EXISTS idx_ci_tags     ON content_index(tag1_id, tag2_id);
		CREATE INDEX IF NOT EXISTS idx_ci_hash     ON content_index(file_hash);

		CREATE VIRTUAL TABLE IF NOT EXISTS content_fts USING fts5(
			path UNINDEXED,
			title,
			body,
			author_name,
			tokenize='porter ascii'
		);
	`)
	return err
}

// seedTaxonomy populates the curated (source='core') taxonomy and manual_tags
// from the embedded JSON. Core nodes are keyed by the permanent UID 'c<id>', so
// the upsert is safe to run on every startup: new nodes are added and renames /
// restructures propagate, while community rows (different UIDs) are untouched.
func seedTaxonomy(db *sql.DB) error {
	tree, err := taxonomy.Tree()
	if err != nil {
		return fmt.Errorf("load taxonomy tree: %w", err)
	}
	tags, err := taxonomy.Tags()
	if err != nil {
		return fmt.Errorf("load manual tags: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var insertNode func(n taxonomy.Node, parentUID string, depth int) error
	insertNode = func(n taxonomy.Node, parentUID string, depth int) error {
		var parent any
		if parentUID != "" {
			parent = parentUID
		}
		if _, err := tx.Exec(`
			INSERT INTO taxonomy (uid, id, name, slug, parent_uid, depth, source, verified)
			VALUES (?, ?, ?, ?, ?, ?, 'core', 1)
			ON CONFLICT(uid) DO UPDATE SET
				name=excluded.name, slug=excluded.slug,
				parent_uid=excluded.parent_uid, depth=excluded.depth`,
			taxonomy.CoreUID(n.ID), n.ID, n.Name, n.Slug, parent, depth); err != nil {
			return err
		}
		for _, c := range n.Children {
			if err := insertNode(c, taxonomy.CoreUID(n.ID), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range tree {
		if err := insertNode(root, "", 0); err != nil {
			return err
		}
	}

	for _, t := range tags {
		if _, err := tx.Exec(`
			INSERT INTO manual_tags (id, name) VALUES (?, ?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name`, t.ID, t.Name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TaxonomyNode is one taxonomy entry, used for ancestor breadcrumbs.
type TaxonomyNode struct {
	UID   string `json:"uid"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Depth int    `json:"depth"`
}

// TaxonomyPath returns the ancestor chain from root to the given uid, ordered
// root-first (e.g. Applied Sciences -> Engineering -> ... -> CPU Design). It
// returns an empty slice if the uid is unknown. It walks both curated and
// community nodes, since both live in the same table linked by parent_uid.
func TaxonomyPath(uid string) ([]TaxonomyNode, error) {
	rows, err := db.Query(`
		WITH RECURSIVE path(uid, name, slug, parent_uid, depth) AS (
			SELECT uid, name, slug, parent_uid, depth FROM taxonomy WHERE uid = ?
			UNION ALL
			SELECT t.uid, t.name, t.slug, t.parent_uid, t.depth
			FROM taxonomy t JOIN path p ON t.uid = p.parent_uid
		)
		SELECT uid, name, slug, depth FROM path ORDER BY depth ASC
	`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TaxonomyNode
	for rows.Next() {
		var n TaxonomyNode
		if err := rows.Scan(&n.UID, &n.Name, &n.Slug, &n.Depth); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CommunityBranch is a user-created taxonomy node (source='community'). Its UID
// is deterministic (taxonomy.CommunityUID of parent+slug) so the same branch
// created on different nodes converges instead of duplicating.
type CommunityBranch struct {
	UID       string
	ParentUID string
	Slug      string
	Name      string
	CreatedBy string // author pubkey (hex)
	Signature string // hex ed25519 over the canonical record
	CreatedAt int64  // unix seconds
	Verified  bool   // signer is a trusted author
}

// InsertCommunityBranch stores a user-created branch under an existing parent.
// It is idempotent by UID, so a branch arriving from several peers (identical
// deterministic UID) merges rather than duplicating. depth is derived from the
// parent. Signature verification is performed by the caller before storage.
func InsertCommunityBranch(b CommunityBranch) error {
	var parentDepth int
	err := db.QueryRow(`SELECT depth FROM taxonomy WHERE uid = ?`, b.ParentUID).Scan(&parentDepth)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("parent branch %q does not exist", b.ParentUID)
	}
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT OR IGNORE INTO taxonomy
			(uid, id, name, slug, parent_uid, depth, source, created_by, signature, created_at, verified)
		VALUES (?, NULL, ?, ?, ?, ?, 'community', ?, ?, ?, ?)`,
		b.UID, b.Name, b.Slug, b.ParentUID, parentDepth+1,
		b.CreatedBy, b.Signature, b.CreatedAt, b.Verified)
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
		SELECT id, url, filename, COALESCE(sync_type,'auto'), last_synced_at,
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
		if err := rows.Scan(&s.ID, &s.URL, &s.Filename, &s.SyncType, &lastSynced,
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
