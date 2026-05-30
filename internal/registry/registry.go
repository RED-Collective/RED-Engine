package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	db   *sql.DB
	once sync.Once
)

type Peer struct {
	ID            int
	URL           string
	PublicKey     string
	Name          string
	PeerType      string // "upstream", "downstream", "mirror"
	ExportedPaths []string
	LastSeen      time.Time
	Verified      bool
	AddedAt       time.Time
}

// InitRegistry creates the database file and tables if not exist.
func InitRegistry(dataDir string) error {
	var err error
	once.Do(func() {
		dbPath := filepath.Join(dataDir, "registry.db")
		db, err = sql.Open("sqlite", dbPath)
		if err != nil {
			return
		}
		// Create tables
		_, err = db.Exec(`
			CREATE TABLE IF NOT EXISTS peers (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				url TEXT UNIQUE NOT NULL,
				public_key TEXT NOT NULL,
				name TEXT,
				peer_type TEXT DEFAULT 'upstream',
				exported_paths TEXT,  -- JSON array
				last_seen DATETIME,
				verified BOOLEAN DEFAULT 0,
				added_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			CREATE INDEX IF NOT EXISTS idx_peers_url ON peers(url);
			CREATE INDEX IF NOT EXISTS idx_peers_verified ON peers(verified);
		`)
	})
	return err
}

// AddPeer inserts or updates a peer.
func AddPeer(p Peer) error {
	if db == nil {
		return errors.New("registry not initialised")
	}
	pathsJSON, _ := json.Marshal(p.ExportedPaths)
	_, err := db.Exec(`
		INSERT INTO peers(url, public_key, name, peer_type, exported_paths, last_seen, verified, added_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET
			public_key = excluded.public_key,
			name = excluded.name,
			peer_type = excluded.peer_type,
			exported_paths = excluded.exported_paths,
			last_seen = excluded.last_seen,
			verified = excluded.verified
	`, p.URL, p.PublicKey, p.Name, p.PeerType, string(pathsJSON), p.LastSeen, p.Verified, p.AddedAt)
	return err
}

// ListPeers returns all peers.
func ListPeers() ([]Peer, error) {
	if db == nil {
		return nil, errors.New("registry not initialised")
	}
	rows, err := db.Query(`SELECT id, url, public_key, name, peer_type, exported_paths, last_seen, verified, added_at FROM peers ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var peers []Peer
	for rows.Next() {
		var p Peer
		var pathsJSON string
		err := rows.Scan(&p.ID, &p.URL, &p.PublicKey, &p.Name, &p.PeerType, &pathsJSON, &p.LastSeen, &p.Verified, &p.AddedAt)
		if err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(pathsJSON), &p.ExportedPaths)
		peers = append(peers, p)
	}
	return peers, nil
}

// GetPeerByURL retrieves a single peer.
func GetPeerByURL(url string) (*Peer, error) {
	if db == nil {
		return nil, errors.New("registry not initialised")
	}
	var p Peer
	var pathsJSON string
	err := db.QueryRow(`SELECT id, url, public_key, name, peer_type, exported_paths, last_seen, verified, added_at FROM peers WHERE url = ?`, url).
		Scan(&p.ID, &p.URL, &p.PublicKey, &p.Name, &p.PeerType, &pathsJSON, &p.LastSeen, &p.Verified, &p.AddedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(pathsJSON), &p.ExportedPaths)
	return &p, nil
}

// DeletePeer removes a peer by URL.
func DeletePeer(url string) error {
	if db == nil {
		return errors.New("registry not initialised")
	}
	_, err := db.Exec(`DELETE FROM peers WHERE url = ?`, url)
	return err
}
