package fetch

import (
	"database/sql"
	"os"
	"path/filepath"
)

// SignerFile is one signature row to persist for peer-pulled content.
type SignerFile struct {
	Path      string
	FileHash  string
	PublicKey string
	Signature string
}

// WriteLocalSignerDB writes a minimal signer.db under bucketDir/.red-feather--<key>/
// holding only the given file signatures. The store loads it (hash-keyed) so
// peer-pulled notes verify, and GenerateManifest reads it back so this node can
// re-export the content. The DB holds nothing but signatures.
func WriteLocalSignerDB(bucketDir, key string, files []SignerFile) error {
	key = sanitizeKey(key)
	if key == "" {
		key = "peer"
	}
	dir := filepath.Join(bucketDir, ".red-feather--"+key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "signer.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY, file_hash TEXT NOT NULL,
			public_key TEXT NOT NULL, signature TEXT NOT NULL);`); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.PublicKey == "" || f.Signature == "" {
			continue // nothing to verify against
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO files (path, file_hash, public_key, signature) VALUES (?, ?, ?, ?)`,
			f.Path, f.FileHash, f.PublicKey, f.Signature); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
