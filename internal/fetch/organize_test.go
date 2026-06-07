package fetch

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RED-Collective/red-engine/internal/registry"
)

// TestMain initializes the registry once for the whole package (InitRegistry is
// once-guarded). OrganizeVault no longer needs it, but keeping it available means
// any helper that touches the DB still works.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fetch-registry-*")
	if err != nil {
		panic(err)
	}
	if err := registry.InitRegistry(dir); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// TestOrganizeVaultMirrorsStructure: a vault is mirrored verbatim under its own
// top-level folder (data/<source>/), preserving the vault's folder structure —
// no taxonomy, no library/manual bucket. Every .md keeps its path within the vault.
func TestOrganizeVaultMirrorsStructure(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	mustWrite(t, filepath.Join(src, "computer-engineering", "digital-system-design", "Memory.md"), "mem")
	mustWrite(t, filepath.Join(src, "networking", "TCP.md"), "tcp")
	writeSignerDB(t, src)

	data := filepath.Join(tmp, "data")
	if err := OrganizeVault(src, data, "test"); err != nil {
		t.Fatalf("OrganizeVault: %v", err)
	}

	for _, rel := range []string{
		"test/computer-engineering/digital-system-design/Memory.md",
		"test/networking/TCP.md",
	} {
		if _, err := os.Stat(filepath.Join(data, filepath.FromSlash(rel))); err != nil {
			t.Errorf("note not mirrored verbatim at %s: %v", rel, err)
		}
	}
}

// TestOrganizeVaultNestsUnderSource: a sync's notes land under data/<source>/
// (grouped per repo), while a blank source writes to the data root.
func TestOrganizeVaultNestsUnderSource(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	mustWrite(t, filepath.Join(src, "Note.md"), "n")
	writeSignerDB(t, src)

	data := filepath.Join(tmp, "data")
	if err := OrganizeVault(src, data, "My-Repo"); err != nil {
		t.Fatalf("OrganizeVault: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "My-Repo", "Note.md")); err != nil {
		t.Errorf("note should nest under My-Repo/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "Note.md")); err == nil {
		t.Error("note must not also sit loose at the data root")
	}

	// Blank source → writes to the data root.
	data2 := filepath.Join(tmp, "data2")
	if err := OrganizeVault(src, data2, ""); err != nil {
		t.Fatalf("OrganizeVault (blank source): %v", err)
	}
	if _, err := os.Stat(filepath.Join(data2, "Note.md")); err != nil {
		t.Errorf("blank source should write to the data root: %v", err)
	}
}

// TestOrganizeVaultIdempotentFavorsNew: a re-run overwrites with newer content.
func TestOrganizeVaultIdempotentFavorsNew(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	data := filepath.Join(tmp, "data")
	leaf := filepath.Join(data, "test", "Note.md")

	mustWrite(t, filepath.Join(src, "Note.md"), "v1")
	if err := OrganizeVault(src, data, "test"); err != nil {
		t.Fatalf("first organize: %v", err)
	}
	mustWrite(t, filepath.Join(src, "Note.md"), "v2 updated")
	if err := OrganizeVault(src, data, "test"); err != nil {
		t.Fatalf("second organize: %v", err)
	}
	got, err := os.ReadFile(leaf)
	if err != nil {
		t.Fatalf("read leaf: %v", err)
	}
	if !strings.Contains(string(got), "v2 updated") {
		t.Errorf("expected newest content, got %q", string(got))
	}
}

// writeSignerDB creates a minimal signer.db under src/.red-feather/ so the vault is
// detected as signed (findSignerDB) and its signature data is preserved. The engine
// no longer reads vault_type, so only the files table is needed.
func writeSignerDB(t *testing.T, src string) {
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
	if _, err := db.Exec(`CREATE TABLE files (path TEXT PRIMARY KEY, file_hash TEXT, public_key TEXT, signature TEXT)`); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
