package navigation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSearchGuidesFindsBodyText: after a scan, SearchGuides matches terms that
// appear in a note's body (via the indexed preview), not just its title — the W7
// fix. It also checks the snippet highlights the match and the .md suffix is
// trimmed from the returned path.
func TestSearchGuidesFindsBodyText(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// "neutrino" appears in the body/preview, not the title.
	mk("physics/particles.md", "# Particle Zoo\n\nThe neutrino is a nearly massless lepton.")
	mk("physics/forces.md", "# Forces\n\nGravity is the weakest fundamental interaction.")

	db := newTestDB(t)
	s := NewService(db, root)
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	results, err := s.SearchGuides("neutrino")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(results), results)
	}
	r := results[0]
	if r.FilePath != "physics/particles" {
		t.Errorf("file_path = %q, want physics/particles (no .md)", r.FilePath)
	}
	if !strings.Contains(r.Snippet, "<mark>") {
		t.Errorf("snippet should highlight the match, got %q", r.Snippet)
	}

	// A term in no note returns nothing (and not an error).
	none, err := s.SearchGuides("quarkxyz")
	if err != nil {
		t.Fatalf("search miss: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected no results, got %+v", none)
	}
}

// TestSearchGuidesReflectsRescan: the FTS index is rebuilt on every scan, so a
// deleted note stops matching and a newly added one starts matching.
func TestSearchGuidesReflectsRescan(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("repo/alpha.md", "# Alpha\n\ncontains the word zebra here")

	db := newTestDB(t)
	s := NewService(db, root)
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("scan1: %v", err)
	}
	if got, _ := s.SearchGuides("zebra"); len(got) != 1 {
		t.Fatalf("zebra should match after scan1, got %d", len(got))
	}

	// Remove the only note carrying "zebra"; rescan must drop it from the index.
	if err := os.Remove(filepath.Join(root, "repo", "alpha.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("scan2: %v", err)
	}
	if got, _ := s.SearchGuides("zebra"); len(got) != 0 {
		t.Errorf("zebra should be gone after deletion+rescan, got %d", len(got))
	}
}
