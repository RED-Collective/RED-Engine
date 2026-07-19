package fetch

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLedgerReclassifyCleanup: re-syncing the same source after a note moves within
// the vault removes the stale copy at its old path.
func TestLedgerReclassifyCleanup(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "data")
	src := filepath.Join(tmp, "src")
	os.MkdirAll(src, 0o755)
	writeSignerDB(t, src)

	// Run 1: a note at the vault root.
	mustWrite(t, filepath.Join(src, "Note.md"), "body")
	if err := OrganizeVault(src, data, "vaultA"); err != nil {
		t.Fatalf("run1: %v", err)
	}
	oldPath := filepath.Join(data, "vaultA", "Note.md")
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("run1 note missing: %v", err)
	}

	// Run 2: same source, note moved into a subfolder; the old copy is cleaned.
	os.Remove(filepath.Join(src, "Note.md"))
	mustWrite(t, filepath.Join(src, "moved", "Note.md"), "body")
	if err := OrganizeVault(src, data, "vaultA"); err != nil {
		t.Fatalf("run2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "vaultA", "moved", "Note.md")); err != nil {
		t.Errorf("note not at new path: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("stale copy at old path was not cleaned up (err=%v)", err)
	}
}

// TestLedgerRemoveBySource: removing one source deletes only its files, leaving a
// sibling source intact.
func TestLedgerRemoveBySource(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "data")

	srcA := filepath.Join(tmp, "srcA")
	os.MkdirAll(srcA, 0o755)
	mustWrite(t, filepath.Join(srcA, "A.md"), "a")
	writeSignerDB(t, srcA)
	if err := OrganizeVault(srcA, data, "A"); err != nil {
		t.Fatal(err)
	}

	srcB := filepath.Join(tmp, "srcB")
	os.MkdirAll(srcB, 0o755)
	mustWrite(t, filepath.Join(srcB, "B.md"), "b")
	writeSignerDB(t, srcB)
	if err := OrganizeVault(srcB, data, "B"); err != nil {
		t.Fatal(err)
	}

	aPath := filepath.Join(data, "A", "A.md")
	bPath := filepath.Join(data, "B", "B.md")

	RemoveBySource(data, "A")
	if _, err := os.Stat(aPath); !os.IsNotExist(err) {
		t.Errorf("source A's file was not removed (err=%v)", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Errorf("sibling source B's file should survive: %v", err)
	}
}
