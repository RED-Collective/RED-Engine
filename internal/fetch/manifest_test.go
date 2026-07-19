package fetch

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestManifestRoundTrip organizes a vault with one frontmatter-signed note, exports
// its bucket manifest (file hashes only — the signature lives in each note's
// header), then replays it on a fresh data root the way a peer would (mirroring
// each file by its manifest path) and asserts the path survives AND the mirrored
// note still verifies from its own frontmatter, with no signer.db anywhere.
func TestManifestRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(filepath.Join(src, "networking"), 0o755); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	author := hex.EncodeToString(pub)
	note := buildSignedNote(t, priv, author, "Net Author", "1700000000", "# TCP\n\nthe signed body\n")
	mustWrite(t, filepath.Join(src, "networking", "TCP.md"), note)

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
	sum := sha256.Sum256([]byte(note))
	if mf.FileHash != hex.EncodeToString(sum[:]) {
		t.Errorf("manifest file hash mismatch: got %s want %s", mf.FileHash, hex.EncodeToString(sum[:]))
	}

	// Replay on node B the way pullFromPeer does: mirror each file's bytes by path.
	dataB := filepath.Join(tmp, "dataB")
	for rel := range m.Files {
		if _, err := WriteNote(dataB, m.Bucket, rel, []byte(note)); err != nil {
			t.Fatalf("mirror %s: %v", rel, err)
		}
	}
	mirrored := filepath.Join(dataB, "rt", filepath.FromSlash(relKey))
	if _, err := os.Stat(mirrored); err != nil {
		t.Errorf("peer replay did not reproduce the path: %v", err)
	}
	// The mirrored note is self-verifying from its frontmatter — no signer.db needed.
	got, _ := os.ReadFile(mirrored)
	if v := VerifyNote(got); v.State != "signed" || v.SignerKey != author {
		t.Errorf("mirrored note did not verify from frontmatter: state=%q key=%q err=%s", v.State, v.SignerKey, v.Err)
	}
}

// TestManifestIncludesImages: an image attachment is listed in the manifest (so a
// peer pull transfers it) with a FileHash for transfer integrity and an empty
// signature (images are unsigned content). A non-image binary is not listed.
func TestManifestIncludesImages(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "data")
	mustWrite(t, filepath.Join(data, "vault", "Note.md"), "![[pic.png]]")
	mustWrite(t, filepath.Join(data, "vault", "pic.png"), "PNGDATA")
	mustWrite(t, filepath.Join(data, "vault", "manual.pdf"), "PDF") // non-image: excluded

	m, err := GenerateManifest(data, "vault")
	if err != nil {
		t.Fatalf("GenerateManifest: %v", err)
	}

	img, ok := m.Files["pic.png"]
	if !ok {
		t.Fatalf("manifest missing image entry; keys=%v", keysOf(m.Files))
	}
	sum := sha256.Sum256([]byte("PNGDATA"))
	if img.FileHash != hex.EncodeToString(sum[:]) {
		t.Errorf("image file hash mismatch: got %s want %s", img.FileHash, hex.EncodeToString(sum[:]))
	}
	if img.Signature != "" || img.PublicKey != "" {
		t.Errorf("image entry should carry no signature, got sig=%q key=%q", img.Signature, img.PublicKey)
	}
	if _, ok := m.Files["manual.pdf"]; ok {
		t.Error("non-image manual.pdf should not be in the manifest")
	}
}

func keysOf(m map[string]ManifestFile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
