package navigation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// buildTree lays out a temporary data dir:
//
//	physics/            (branch — has a subdir)
//	  index.md
//	  mechanics/        (leaf — only .md files)
//	    intro.md
//	    .hidden.md      (skipped: dotfile)
//	  .obsidian/        (skipped: dot-dir)
//	    config.json
//	.private/           (skipped: top-level dot-dir)
//	  secret.md
func buildTree(t *testing.T) string {
	t.Helper()
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
	mk("physics/index.md", "# Physics\n\nThe study of matter and energy.")
	mk("physics/mechanics/intro.md", "# Intro to Mechanics\n\nNewton's laws and motion.")
	mk("physics/mechanics/.hidden.md", "should be ignored")
	mk("physics/.obsidian/config.json", "{}")
	mk(".private/secret.md", "top-level dot dir, ignored")
	return root
}

func TestScanDataDirectories(t *testing.T) {
	db := newTestDB(t)
	s := NewService(db, buildTree(t))

	res, err := s.ScanDataDirectories()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("unexpected scan errors: %v", res.Errors)
	}
	// physics + physics/mechanics = 2 folders; .private and .obsidian skipped.
	if res.FoldersScanned != 2 {
		t.Errorf("FoldersScanned = %d, want 2", res.FoldersScanned)
	}
	// index.md + intro.md = 2 guides; .hidden.md skipped.
	if res.GuidesIndexed != 2 {
		t.Errorf("GuidesIndexed = %d, want 2", res.GuidesIndexed)
	}

	if err := s.VerifyNavigationDB(); err != nil {
		t.Errorf("verify: %v", err)
	}
}

func TestScanLeafAndBranchFlags(t *testing.T) {
	db := newTestDB(t)
	s := NewService(db, buildTree(t))
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// physics has a subdir → branch; description pulled from index.md.
	branch, err := s.GetNavigationTree("physics")
	if err != nil {
		t.Fatalf("tree physics: %v", err)
	}
	if branch.IsLeaf {
		t.Error("physics should be a branch, not a leaf")
	}
	if branch.ContentType != "physics" {
		t.Errorf("content_type = %q, want physics", branch.ContentType)
	}
	if branch.Description != "The study of matter and energy." {
		t.Errorf("description = %q", branch.Description)
	}
	if branch.ChildCount != 1 {
		t.Errorf("child_count = %d, want 1 (subfolders only)", branch.ChildCount)
	}

	// Children now carry BOTH the subfolder and physics's own index.md as a guide.
	var mechanics, indexGuide *NavNode
	for i := range branch.Children {
		switch c := &branch.Children[i]; {
		case c.IsGuide && c.Path == "physics/index":
			indexGuide = c
		case !c.IsGuide && c.Path == "physics/mechanics":
			mechanics = c
		}
	}
	if indexGuide == nil {
		t.Errorf("physics/index.md should appear as a guide child: %+v", branch.Children)
	}
	if mechanics == nil {
		t.Fatalf("physics/mechanics subfolder missing from children: %+v", branch.Children)
	}

	// physics/mechanics has only .md files → leaf with one guide.
	if !mechanics.IsLeaf {
		t.Error("physics/mechanics should be a leaf")
	}
	if mechanics.GuideCount != 1 {
		t.Errorf("guide_count = %d, want 1", mechanics.GuideCount)
	}
	if mechanics.ContentType != "physics" {
		t.Errorf("leaf content_type = %q, want physics (inherited)", mechanics.ContentType)
	}

	// Navigating directly into the leaf returns its .md files as guide nodes —
	// the fix for folders rendering empty in the UI.
	leafTree, err := s.GetNavigationTree("physics/mechanics")
	if err != nil {
		t.Fatalf("tree physics/mechanics: %v", err)
	}
	if len(leafTree.Children) != 1 || !leafTree.Children[0].IsGuide ||
		leafTree.Children[0].Path != "physics/mechanics/intro" {
		t.Errorf("leaf guides = %+v, want one guide physics/mechanics/intro", leafTree.Children)
	}
}

func TestScanIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	s := NewService(db, buildTree(t))
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	var folderCount, guideCount int
	db.QueryRow(`SELECT COUNT(*) FROM nav_folders`).Scan(&folderCount)
	db.QueryRow(`SELECT COUNT(*) FROM nav_guides`).Scan(&guideCount)
	if folderCount != 2 {
		t.Errorf("after rescan nav_folders = %d, want 2 (no duplicates)", folderCount)
	}
	if guideCount != 2 {
		t.Errorf("after rescan nav_guides = %d, want 2 (no duplicates)", guideCount)
	}
}

func TestScanPrunesDeletedContent(t *testing.T) {
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
	mk("alpha/a.md", "# Alpha")
	mk("beta/b.md", "# Beta")

	db := newTestDB(t)
	s := NewService(db, root)
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("first scan: %v", err)
	}

	// A custom description on a folder that will survive the rescan must persist.
	alpha, err := s.GetNavigationTree("alpha")
	if err != nil {
		t.Fatalf("tree alpha: %v", err)
	}
	if err := s.SetFolderDescription(alpha.ID, "kept across rescans", "tester"); err != nil {
		t.Fatalf("set description: %v", err)
	}

	// beta is deleted and re-bucketed elsewhere; gamma is brand new.
	if err := os.RemoveAll(filepath.Join(root, "beta")); err != nil {
		t.Fatal(err)
	}
	mk("gamma/g.md", "# Gamma")

	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("second scan: %v", err)
	}

	// beta and its guide are gone; alpha and gamma remain (2 folders, 2 guides).
	var folderCount, guideCount int
	db.QueryRow(`SELECT COUNT(*) FROM nav_folders`).Scan(&folderCount)
	db.QueryRow(`SELECT COUNT(*) FROM nav_guides`).Scan(&guideCount)
	if folderCount != 2 {
		t.Errorf("nav_folders = %d, want 2 (beta pruned)", folderCount)
	}
	if guideCount != 2 {
		t.Errorf("nav_guides = %d, want 2 (beta/b.md pruned)", guideCount)
	}
	if _, err := s.GetNavigationTree("beta"); err == nil {
		t.Error("beta should have been pruned but is still queryable")
	}

	// alpha's id and override survived the prune.
	alpha2, err := s.GetNavigationTree("alpha")
	if err != nil {
		t.Fatalf("tree alpha after rescan: %v", err)
	}
	if alpha2.ID != alpha.ID {
		t.Errorf("alpha id changed across rescan: %d -> %d", alpha.ID, alpha2.ID)
	}
	if alpha2.Description != "kept across rescans" {
		t.Errorf("override description lost: %q", alpha2.Description)
	}

	// No description overrides should dangle without a folder.
	var orphans int
	db.QueryRow(`SELECT COUNT(*) FROM nav_description_overrides
		WHERE folder_id NOT IN (SELECT id FROM nav_folders)`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("orphaned description overrides = %d, want 0", orphans)
	}

	if err := s.VerifyNavigationDB(); err != nil {
		t.Errorf("verify: %v", err)
	}
}

func TestScanIndexesTags(t *testing.T) {
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
	// Two notes share "electronics"; one also has "security".
	mk("repo/A.md", "---\nred_tags: [\"electronics\",\"security\"]\n---\n# A")
	mk("repo/B.md", "---\nred_tags: [\"electronics\"]\n---\n# B")
	mk("repo/C.md", "# C (untagged)")

	db := newTestDB(t)
	s := NewService(db, root)
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	tags, err := s.GetAllTags()
	if err != nil {
		t.Fatalf("GetAllTags: %v", err)
	}
	got := map[string]int{}
	for _, tc := range tags {
		got[tc.Name] = tc.Count
	}
	if got["electronics"] != 2 {
		t.Errorf("electronics count = %d, want 2", got["electronics"])
	}
	if got["security"] != 1 {
		t.Errorf("security count = %d, want 1", got["security"])
	}

	notes, err := s.GetNotesByTag("electronics")
	if err != nil {
		t.Fatalf("GetNotesByTag: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("electronics notes = %d, want 2", len(notes))
	}
	for _, n := range notes {
		if !n.IsGuide || strings.HasSuffix(n.Path, ".md") {
			t.Errorf("tagged note node malformed: %+v", n)
		}
	}

	// Deleting B drops its tag membership; electronics falls to 1, B's row is pruned.
	if err := os.Remove(filepath.Join(root, "repo", "B.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	notes, _ = s.GetNotesByTag("electronics")
	if len(notes) != 1 {
		t.Errorf("after delete, electronics notes = %d, want 1", len(notes))
	}
	var orphans int
	db.QueryRow(`SELECT COUNT(*) FROM nav_guide_tags
		WHERE guide_id NOT IN (SELECT id FROM nav_guides)`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("orphaned tag rows = %d, want 0", orphans)
	}
}

func TestScanMissingDataDir(t *testing.T) {
	db := newTestDB(t)
	s := NewService(db, filepath.Join(t.TempDir(), "does-not-exist"))
	if _, err := s.ScanDataDirectories(); err == nil {
		t.Error("expected error scanning a missing data dir")
	}
}
