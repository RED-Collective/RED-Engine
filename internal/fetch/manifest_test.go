package fetch

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestManifestRoundTrip organizes a vault with one signed note, exports its bucket
// manifest (signatures only, no branch records), then replays it on a fresh data
// root the way a peer would — mirroring each file by its manifest path — and
// asserts the path + signature survive.
func TestManifestRoundTrip(t *testing.T) {
	tmp := t.TempDir()

	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(filepath.Join(src, "networking"), 0o755); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pubHex := hex.EncodeToString(pub)

	body := []byte("the signed body")
	sum := sha256.Sum256(body)
	fileHash := hex.EncodeToString(sum[:])
	fileSig := hex.EncodeToString(ed25519.Sign(priv, sum[:])) // RED-Feather signs sha256(content)

	mustWrite(t, filepath.Join(src, "networking", "TCP.md"), string(body))
	writeSignerDBWithFile(t, src, "networking/TCP.md", fileHash, pubHex, fileSig)

	dataA := filepath.Join(tmp, "dataA")
	if err := OrganizeVault(src, dataA, "rt"); err != nil {
		t.Fatalf("organize: %v", err)
	}

	// The sync's top-level folder is its source ("rt"); the manifest is generated for
	// that folder and keys each file by its path within it.
	m, err := GenerateManifest(dataA, "rt")
	if err != nil {
		t.Fatalf("GenerateManifest: %v", err)
	}
	if m.Bucket != "rt" {
		t.Errorf("manifest bucket = %q, want rt", m.Bucket)
	}
	relKey := "networking/TCP.md"
	mf, ok := m.Files[relKey]
	if !ok {
		t.Fatalf("manifest missing %q; keys=%v", relKey, keysOf(m.Files))
	}
	if mf.PublicKey != pubHex || mf.Signature != fileSig {
		t.Errorf("manifest did not carry the signature for the note")
	}

	// Replay on node B the way pullFromPeer does: mirror each file by its path.
	dataB := filepath.Join(tmp, "dataB")
	for rel := range m.Files {
		if _, err := WriteNote(dataB, m.Bucket, rel, body); err != nil {
			t.Fatalf("mirror %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dataB, "rt", filepath.FromSlash(relKey))); err != nil {
		t.Errorf("peer replay did not reproduce the path: %v", err)
	}
}

// writeSignerDBWithFile creates a signer.db (vault_metadata.vault_type + one files
// row) under src/.red-feather/, the way a RED-Feather vault looks after the
// branch/author removal.
func writeSignerDBWithFile(t *testing.T, src, fpath, fhash, pub, sig string) {
	t.Helper()
	dir := filepath.Join(src, ".red-feather")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "signer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE vault_metadata (id INTEGER PRIMARY KEY, vault_type TEXT DEFAULT 'library');
		CREATE TABLE files (path TEXT PRIMARY KEY, file_hash TEXT, public_key TEXT, signature TEXT);
		INSERT INTO vault_metadata (id, vault_type) VALUES (1, 'library');`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO files (path, file_hash, public_key, signature) VALUES (?,?,?,?)`,
		fpath, fhash, pub, sig); err != nil {
		t.Fatal(err)
	}
}

func keysOf(m map[string]ManifestFile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
