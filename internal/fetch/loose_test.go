package fetch

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOrganizeLooseMarkdownNestsUnderSource: a loose note is filed under its source
// top-level folder (data/<source>/<fileName>) so the navigation scanner, which only
// indexes top-level directories, can see it. red_type no longer affects placement.
func TestOrganizeLooseMarkdownNestsUnderSource(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "data")
	if err := OrganizeLooseMarkdown(data, "loose1", "Guide.md", []byte("---\nred_type: manual\n---\nbody")); err != nil {
		t.Fatalf("OrganizeLooseMarkdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "loose1", "Guide.md")); err != nil {
		t.Errorf("loose note not filed under its source folder: %v", err)
	}
	// It must NOT be routed to a library/manual bucket anymore.
	if _, err := os.Stat(filepath.Join(data, "manual", "Guide.md")); err == nil {
		t.Error("loose note was routed to a manual/ bucket — buckets should be gone")
	}
}

// TestOrganizeLooseMarkdownRemovable: the loose note is ledger-tracked under its
// source, so RemoveBySource deletes it.
func TestOrganizeLooseMarkdownRemovable(t *testing.T) {
	tmp := t.TempDir()
	data := filepath.Join(tmp, "data")
	if err := OrganizeLooseMarkdown(data, "loose2", "Plain.md", []byte("just text")); err != nil {
		t.Fatalf("OrganizeLooseMarkdown: %v", err)
	}
	note := filepath.Join(data, "loose2", "Plain.md")
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("loose note missing: %v", err)
	}
	RemoveBySource(data, "loose2")
	if _, err := os.Stat(note); !os.IsNotExist(err) {
		t.Errorf("loose note not removed by source (err=%v)", err)
	}
}
