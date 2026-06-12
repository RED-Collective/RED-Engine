package fetch

import (
	"os"
	"path/filepath"
	"testing"
)

// signedNote builds a minimal signed-note body: frontmatter carrying a non-empty
// red_sig and a red_signed_at, followed by the body. The exact signature value is
// irrelevant to writeIfChanged — it only checks presence (red_sig) and ordering
// (red_signed_at), not validity.
func signedNote(signedAt, body string) []byte {
	return []byte("---\n" +
		"red_author: abc123\n" +
		"red_signed_at: " + signedAt + "\n" +
		"red_sig: deadbeef\n" +
		"---\n" + body)
}

// TestWriteIfChangedSignedNotClobberedByUnsigned: the core W1 guarantee — an
// unsigned export can never overwrite a signed note already on disk.
func TestWriteIfChangedSignedNotClobberedByUnsigned(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "note.md")
	signed := signedNote("2026-06-01 10:00:00", "the real, signed content")
	if err := writeIfChanged(dest, signed); err != nil {
		t.Fatalf("write signed: %v", err)
	}

	// An unsigned version of the same note must be rejected.
	if err := writeIfChanged(dest, []byte("plain unsigned overwrite attempt")); err != nil {
		t.Fatalf("write unsigned: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(signed) {
		t.Errorf("signed note was clobbered by unsigned content:\n%s", got)
	}
}

// TestWriteIfChangedNewerSignedWins: between two signed notes, the newer
// red_signed_at overwrites; an older one cannot roll the note back.
func TestWriteIfChangedNewerSignedWins(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "note.md")
	older := signedNote("2026-06-01 10:00:00", "older signed body")
	newer := signedNote("2026-06-02 10:00:00", "newer signed body")

	if err := writeIfChanged(dest, older); err != nil {
		t.Fatalf("write older: %v", err)
	}

	// Newer wins.
	if err := writeIfChanged(dest, newer); err != nil {
		t.Fatalf("write newer: %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != string(newer) {
		t.Errorf("newer signed note should have won, got:\n%s", got)
	}

	// Replaying the older one is rejected (no rollback).
	if err := writeIfChanged(dest, older); err != nil {
		t.Fatalf("replay older: %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != string(newer) {
		t.Errorf("older signed note rolled back a newer one, got:\n%s", got)
	}
}

// TestWriteIfChangedUnsignedLastWriterWins: with no signatures involved, behaviour
// is unchanged — last writer wins (e.g. plain notes and images update freely).
func TestWriteIfChangedUnsignedLastWriterWins(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "img.png")
	if err := writeIfChanged(dest, []byte("v1")); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	if err := writeIfChanged(dest, []byte("v2")); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "v2" {
		t.Errorf("unsigned content should update last-writer-wins, got %q", got)
	}
}

// TestWriteIfChangedIdempotent: identical content is a no-op and does not touch
// the file's mtime (so it never needlessly retriggers the file watcher).
func TestWriteIfChangedIdempotent(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "note.md")
	body := signedNote("2026-06-01 10:00:00", "stable body")
	if err := writeIfChanged(dest, body); err != nil {
		t.Fatalf("write: %v", err)
	}
	fi1, _ := os.Stat(dest)

	if err := writeIfChanged(dest, body); err != nil {
		t.Fatalf("rewrite identical: %v", err)
	}
	fi2, _ := os.Stat(dest)
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Error("identical rewrite changed mtime; should have been skipped")
	}
}
