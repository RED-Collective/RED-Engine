package fetch

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// makeZip writes a zip at path containing the given name→body entries.
func makeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestExtractZipDoesNotClobberSigned: W10 — archive extraction now routes through
// writeIfChanged, so an unsigned copy of a note inside an archive cannot overwrite
// a signed note already on disk.
func TestExtractZipDoesNotClobberSigned(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	// A signed note is already present at the extraction target.
	signed := signedNote("2026-06-01 10:00:00", "the signed, published body")
	notePath := filepath.Join(dest, "note.md")
	if err := os.WriteFile(notePath, signed, 0o644); err != nil {
		t.Fatal(err)
	}

	// An archive ships an unsigned version of the same note plus a new file.
	zipPath := filepath.Join(tmp, "archive.zip")
	makeZip(t, zipPath, map[string]string{
		"note.md":  "unsigned overwrite from archive",
		"fresh.md": "brand new content",
	})

	if err := extractZip(zipPath, dest); err != nil {
		t.Fatalf("extractZip: %v", err)
	}

	// The signed note is untouched.
	if got, _ := os.ReadFile(notePath); string(got) != string(signed) {
		t.Errorf("signed note clobbered by archive:\n%s", got)
	}
	// The genuinely new file was extracted.
	if got, _ := os.ReadFile(filepath.Join(dest, "fresh.md")); string(got) != "brand new content" {
		t.Errorf("new archive file not extracted, got %q", got)
	}
}

// TestExtractZipIdempotent: re-extracting an identical archive does not rewrite an
// unchanged file (no mtime churn), thanks to the SHA256 short-circuit.
func TestExtractZipIdempotent(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "out")
	zipPath := filepath.Join(tmp, "archive.zip")
	makeZip(t, zipPath, map[string]string{"doc.md": "stable content"})

	if err := extractZip(zipPath, dest); err != nil {
		t.Fatalf("extract1: %v", err)
	}
	fi1, err := os.Stat(filepath.Join(dest, "doc.md"))
	if err != nil {
		t.Fatal(err)
	}

	if err := extractZip(zipPath, dest); err != nil {
		t.Fatalf("extract2: %v", err)
	}
	fi2, _ := os.Stat(filepath.Join(dest, "doc.md"))
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Error("re-extracting identical content changed mtime; should have been skipped")
	}
}
