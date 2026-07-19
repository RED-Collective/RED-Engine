package navigation

import (
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// buildLinkTree lays out a temporary data dir exercising the link graph:
//
//	vaultA/
//	  Hub.md              — links: Target Note (x2 spellings), Missing Note,
//	                        pic.png embed, Other#Section, #Local self-ref,
//	                        Target Note embed
//	  Other.md
//	  sub/
//	    Target Note.md    — duplicate basename winner (vaultA/sub < vaultB)
//	vaultB/
//	  Target Note.md      — duplicate basename loser
//	vaultC/
//	  Front.md            — wikilink only inside frontmatter (must not edge)
func buildLinkTree(t *testing.T) string {
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
	mk("vaultA/Hub.md", "# Hub\n\n[[Target Note]] and [[target note|alias]] and [[Missing Note]]\n"+
		"![[pic.png]] [[Other#Section]] [[#Local]] ![[Target Note]]\n")
	mk("vaultA/Other.md", "# Other\n\nNo links here.\n")
	mk("vaultA/sub/Target Note.md", "# Target\n\nBody.\n")
	mk("vaultB/Target Note.md", "# Shadowed Target\n\nDuplicate basename.\n")
	mk("vaultC/Front.md", "---\nx: \"[[NotALink]]\"\n---\n# Front\n\nClean body.\n")
	return root
}

func scanLinkTree(t *testing.T) (*Service, string) {
	t.Helper()
	db := newTestDB(t)
	root := buildLinkTree(t)
	s := NewService(db, root)
	res, err := s.ScanDataDirectories()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected scan errors: %v", res.Errors)
	}
	return s, root
}

func TestBacklinks(t *testing.T) {
	s, _ := scanLinkTree(t)

	// Hub links AND embeds Target Note; the alias spelling collapses into the
	// link edge, so exactly two rows, both from vaultA/Hub.
	refs, err := s.Backlinks("vaultA/sub/Target Note")
	if err != nil {
		t.Fatalf("backlinks: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("backlinks = %+v, want 2 rows (link + embed)", refs)
	}
	kinds := map[string]bool{}
	for _, r := range refs {
		if r.FilePath != "vaultA/Hub" {
			t.Errorf("backlink from %q, want vaultA/Hub", r.FilePath)
		}
		if r.Title != "Hub" {
			t.Errorf("backlink title %q, want Hub", r.Title)
		}
		kinds[r.Kind] = true
	}
	if !kinds["link"] || !kinds["embed"] {
		t.Errorf("kinds = %v, want both link and embed", kinds)
	}

	// Duplicate basename: vaultB's copy loses first-wins resolution, so nothing
	// points at it.
	refs, err = s.Backlinks("vaultB/Target Note")
	if err != nil {
		t.Fatalf("backlinks vaultB: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("vaultB/Target Note backlinks = %+v, want none (first-wins)", refs)
	}

	// Input normalization: leading slash + .md suffix; heading link counts as a
	// normal edge.
	refs, err = s.Backlinks("/vaultA/Other.md")
	if err != nil {
		t.Fatalf("backlinks Other: %v", err)
	}
	if len(refs) != 1 || refs[0].FilePath != "vaultA/Hub" {
		t.Errorf("Other backlinks = %+v, want one row from vaultA/Hub", refs)
	}
}

func TestGraphDump(t *testing.T) {
	s, _ := scanLinkTree(t)

	g, err := s.GraphDump()
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	// 5 .md fixtures = 5 nodes.
	if len(g.Nodes) != 5 {
		t.Errorf("nodes = %d, want 5: %+v", len(g.Nodes), g.Nodes)
	}
	ids := map[int64]bool{}
	for _, n := range g.Nodes {
		ids[n.ID] = true
		if n.Vault == "" {
			t.Errorf("node %s has empty vault", n.FilePath)
		}
	}
	// Every edge endpoint must be a node; broken links are excluded.
	for _, e := range g.Edges {
		if !ids[e.SourceID] || !ids[e.TargetID] {
			t.Errorf("edge %+v references a non-node id", e)
		}
	}
	// Hub→Target (link), Hub→Target (embed), Hub→Other (link) = 3 edges.
	if len(g.Edges) != 3 {
		t.Errorf("edges = %d, want 3: %+v", len(g.Edges), g.Edges)
	}

	// The image embed must produce no row at all (asset, not a note edge).
	var pngRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nav_links WHERE target_path = 'pic.png'`).Scan(&pngRows); err != nil {
		t.Fatal(err)
	}
	if pngRows != 0 {
		t.Errorf("pic.png produced %d nav_links rows, want 0", pngRows)
	}
}

func TestBrokenLinks(t *testing.T) {
	s, _ := scanLinkTree(t)

	broken, err := s.BrokenLinks()
	if err != nil {
		t.Fatalf("broken: %v", err)
	}
	if len(broken) != 1 {
		t.Fatalf("broken = %+v, want exactly one entry (Missing Note)", broken)
	}
	b := broken[0]
	if b.TargetPath != "Missing Note" || b.Count != 1 {
		t.Errorf("broken[0] = %+v, want {Missing Note 1 …}", b)
	}
	if len(b.Sources) != 1 || b.Sources[0] != "vaultA/Hub" {
		t.Errorf("sources = %v, want [vaultA/Hub]", b.Sources)
	}
	// The frontmatter-only wikilink must never appear anywhere.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nav_links WHERE LOWER(target_path) = 'notalink'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("frontmatter wikilink produced %d rows, want 0", n)
	}
}

func TestLinksRescanIdempotent(t *testing.T) {
	s, _ := scanLinkTree(t)

	var before int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nav_links`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	var after int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nav_links`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("nav_links count changed across rescan: %d → %d", before, after)
	}
}

func TestLinksReresolveAfterDelete(t *testing.T) {
	s, root := scanLinkTree(t)

	// Delete the winning duplicate; edges must re-resolve to the survivor.
	if err := os.Remove(filepath.Join(root, "vaultA/sub/Target Note.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanDataDirectories(); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	refs, err := s.Backlinks("vaultA/sub/Target Note")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Errorf("deleted note still has backlinks: %+v", refs)
	}
	refs, err = s.Backlinks("vaultB/Target Note")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Errorf("survivor backlinks = %+v, want 2 (re-resolved first-wins)", refs)
	}
}
