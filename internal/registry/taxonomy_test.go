package registry

import (
	"testing"

	"github.com/RED-Collective/red-engine/internal/taxonomy"
)

func TestTaxonomySeedAndCommunityBranch(t *testing.T) {
	if err := InitRegistry(t.TempDir()); err != nil {
		t.Fatalf("InitRegistry: %v", err)
	}

	// Core taxonomy seeded from embedded JSON.
	var coreCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM taxonomy WHERE source='core'`).Scan(&coreCount); err != nil {
		t.Fatalf("count core: %v", err)
	}
	if coreCount != 135 {
		t.Errorf("core node count = %d, want 135", coreCount)
	}

	var tagCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM manual_tags`).Scan(&tagCount); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if tagCount != 10 {
		t.Errorf("manual tag count = %d, want 10", tagCount)
	}

	// Breadcrumb for CPU Design (id 4113), root-first.
	path, err := TaxonomyPath(taxonomy.CoreUID(4113))
	if err != nil {
		t.Fatalf("TaxonomyPath core: %v", err)
	}
	want := []string{"Applied Sciences", "Engineering", "Computer Engineering", "Digital Systems", "CPU Design"}
	if got := names(path); !equalStrings(got, want) {
		t.Errorf("core path = %v, want %v", got, want)
	}

	// Deterministic community UID under Computer Science (id 4200).
	parent := taxonomy.CoreUID(4200)
	if taxonomy.CommunityUID(parent, "quantum-computing") != taxonomy.CommunityUID(parent, "quantum-computing") {
		t.Fatal("CommunityUID is not deterministic")
	}
	uid := taxonomy.CommunityUID(parent, "quantum-computing")

	branch := CommunityBranch{
		UID:       uid,
		ParentUID: parent,
		Slug:      "quantum-computing",
		Name:      "Quantum Computing",
		CreatedBy: "deadbeef",
		Signature: "00",
		CreatedAt: 1700000000,
		Verified:  true,
	}
	if err := InsertCommunityBranch(branch); err != nil {
		t.Fatalf("InsertCommunityBranch: %v", err)
	}
	// Same deterministic UID again must merge, not duplicate.
	if err := InsertCommunityBranch(branch); err != nil {
		t.Fatalf("InsertCommunityBranch (2nd): %v", err)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM taxonomy WHERE uid=?`, uid).Scan(&rows); err != nil {
		t.Fatalf("count branch: %v", err)
	}
	if rows != 1 {
		t.Errorf("community branch rows = %d, want 1 (merge, not duplicate)", rows)
	}

	// Breadcrumb now walks core -> community.
	cpath, err := TaxonomyPath(uid)
	if err != nil {
		t.Fatalf("TaxonomyPath community: %v", err)
	}
	wantC := []string{"Applied Sciences", "Computer Science", "Quantum Computing"}
	if got := names(cpath); !equalStrings(got, wantC) {
		t.Errorf("community path = %v, want %v", got, wantC)
	}
	if last := cpath[len(cpath)-1]; last.Depth != 2 {
		t.Errorf("community branch depth = %d, want 2", last.Depth)
	}

	// Rejecting a branch whose parent is unknown.
	if err := InsertCommunityBranch(CommunityBranch{UID: "uX", ParentUID: "nope", Slug: "x", Name: "X"}); err == nil {
		t.Error("InsertCommunityBranch with missing parent should fail")
	}
}

func names(ns []TaxonomyNode) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Name
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
