package backup

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile creates dir tree + file under root.
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCreateDataBackupRoundTrips: a snapshot contains every published file at a
// path relative to the data dir (so unzip -d data/ restores in place), and skips
// hidden dirs.
func TestCreateDataBackupRoundTrips(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	writeFile(t, data, "physics/intro.md", "# Intro")
	writeFile(t, data, "physics/img.png", "PNGDATA")
	writeFile(t, data, ".red-ledger/state.json", "{}") // hidden: must be skipped
	writeFile(t, data, ".git/HEAD", "ref: refs/heads/main")

	backupDir := filepath.Join(t.TempDir(), "backups")
	info, err := CreateDataBackup(data, backupDir)
	if err != nil {
		t.Fatalf("CreateDataBackup: %v", err)
	}
	if info.SizeB <= 0 {
		t.Errorf("size = %d, want > 0", info.SizeB)
	}
	if !strings.HasPrefix(info.Name, "data-") || !strings.HasSuffix(info.Name, ".zip") {
		t.Errorf("name = %q, want data-<timestamp>.zip", info.Name)
	}

	zr, err := zip.OpenReader(info.Path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	if !got["physics/intro.md"] || !got["physics/img.png"] {
		t.Errorf("published files missing from zip: %v", got)
	}
	for name := range got {
		if strings.HasPrefix(name, ".red-ledger/") || strings.HasPrefix(name, ".git/") {
			t.Errorf("hidden dir should be excluded but found %q", name)
		}
	}
}

// TestListAndPruneBackups: ListBackups returns newest-first; PruneBackups keeps
// only the N most recent.
func TestListAndPruneBackups(t *testing.T) {
	backupDir := t.TempDir()
	// Three zips with increasing mtimes so ordering is deterministic.
	names := []string{"data-2026-01-01-00-00-00.zip", "data-2026-02-01-00-00-00.zip", "data-2026-03-01-00-00-00.zip"}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, n := range names {
		p := filepath.Join(backupDir, n)
		if err := os.WriteFile(p, []byte("zip"+n), 0o644); err != nil {
			t.Fatal(err)
		}
		// Newer index → newer mtime, so ordering is deterministic regardless of
		// how fast the files are written.
		mt := base.AddDate(0, i, 0)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	list, err := ListBackups(backupDir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("got %d backups, want 3", len(list))
	}
	if list[0].Name != names[2] {
		t.Errorf("newest-first ordering wrong: first = %q, want %q", list[0].Name, names[2])
	}

	// Keep the 2 most recent; the oldest is deleted.
	if err := PruneBackups(backupDir, 2); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	after, _ := ListBackups(backupDir)
	if len(after) != 2 {
		t.Fatalf("after prune got %d, want 2", len(after))
	}
	if _, err := os.Stat(filepath.Join(backupDir, names[0])); !os.IsNotExist(err) {
		t.Errorf("oldest backup should have been pruned (err=%v)", err)
	}
}

// TestListBackupsMissingDir: a non-existent backup dir is not an error.
func TestListBackupsMissingDir(t *testing.T) {
	list, err := ListBackups(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Errorf("missing dir should not error, got %v", err)
	}
	if len(list) != 0 {
		t.Errorf("missing dir should yield no backups, got %d", len(list))
	}
}
