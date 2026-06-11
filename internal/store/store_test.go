package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RED-Collective/red-engine/internal/models"
	"github.com/RED-Collective/red-engine/internal/registry"
)

// TestProcessArticleStripsFrontmatter verifies that the YAML signature header is NOT
// rendered into the page body (engine #3: "the YAML header renders very ugly"), while
// the readable signature date is still surfaced via Article.SignedAt for the UI.
func TestProcessArticleStripsFrontmatter(t *testing.T) {
	dir, err := os.MkdirTemp("", "red-store-render-*")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	const signedAt = "Mon, 08 Jun 2026 05:35:00 +03"
	note := "---\n" +
		"red_author: aabbccdd\n" +
		"red_author_name: Alice\n" +
		"red_signed_at: " + signedAt + "\n" +
		"red_hash: deadbeef\n" +
		"red_sig: cafe\n" +
		"red_tags: [\"Security\"]\n" +
		"---\n" +
		"# Title\n\nBody paragraph.\n"
	notePath := filepath.Join(dir, "note.md")
	if err := os.WriteFile(notePath, []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(dir)
	art, _, err := s.processArticle(notePath, nil)
	if err != nil {
		t.Fatalf("processArticle: %v", err)
	}

	body := string(art.Body)
	for _, leak := range []string{"red_author", "red_signed_at", "red_sig", "red_hash"} {
		if strings.Contains(body, leak) {
			t.Errorf("rendered body leaked frontmatter key %q:\n%s", leak, body)
		}
	}
	if !strings.Contains(body, "Body paragraph") {
		t.Errorf("rendered body missing the actual content:\n%s", body)
	}
	if art.SignedAt != signedAt {
		t.Errorf("SignedAt = %q, want %q", art.SignedAt, signedAt)
	}
}

// TestResolvesObsidianEmbedsAndLinks: after a full Reload builds the basename index,
// a note's ![[image.png]] embed renders to an <img> off the public /content/ route,
// and a [[Other Note]] wikilink renders to an <a> pointing at the target's article
// path — Obsidian syntax goldmark would otherwise emit as literal text.
func TestResolvesObsidianEmbedsAndLinks(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Testing")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	// An embedding note + the image it references + a link target + a linking note.
	mustWriteFile(t, filepath.Join(folder, "Embedder.md"), "![[Pasted image 1.png]]")
	mustWriteFile(t, filepath.Join(folder, "Pasted image 1.png"), "PNGDATA")
	mustWriteFile(t, filepath.Join(folder, "Target.md"), "# Target\n\nbody")
	mustWriteFile(t, filepath.Join(folder, "Linker.md"), "see [[Target]] for more")

	s := New(dir)
	if err := s.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	embedder := s.Get("Testing/Embedder")
	if embedder == nil {
		t.Fatal("embedder note not loaded")
	}
	body := string(embedder.Body)
	if !strings.Contains(body, `src="/content/Testing/Pasted%20image%201.png"`) {
		t.Errorf("embed not rendered as /content image; body=%q", body)
	}
	if strings.Contains(body, "[[") {
		t.Errorf("raw wikilink leaked into rendered body: %q", body)
	}

	linker := s.Get("Testing/Linker")
	if linker == nil {
		t.Fatal("linker note not loaded")
	}
	lbody := string(linker.Body)
	if !strings.Contains(lbody, `href="/Testing/Target"`) {
		t.Errorf("wikilink not rendered as article link; body=%q", lbody)
	}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestReindexHookFiresOnContentChange pins the contract behind the "folder counts
// go stale after a git push" fix: every content-mutating path (Reload and
// UpdateFiles) must invoke the registered reindex hook so the SQLite-backed
// navigation index (folder tree + guide counts) is rebuilt in lockstep with the
// in-memory nav. Before the fix, periodic git sync and the webhook only reloaded
// the in-memory nav, so "recently added" updated but folder cards/counts did not.
func TestReindexHookFiresOnContentChange(t *testing.T) {
	dir := t.TempDir()

	// One top-level folder with a single note so Reload has something to walk.
	folder := filepath.Join(dir, "Guides")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	notePath := filepath.Join(folder, "first.md")
	if err := os.WriteFile(notePath, []byte("# First\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(dir)
	var hits atomic.Int64
	s.SetReindexHook(func() { hits.Add(1) })

	// Reload fires the hook exactly once.
	if err := s.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("Reload should fire the reindex hook once, fired %d times", got)
	}

	// A newly synced note + UpdateFiles fires the hook again (the path the watcher
	// and webhook hot-patch use).
	newNote := filepath.Join(folder, "second.md")
	if err := os.WriteFile(newNote, []byte("# Second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateFiles([]string{newNote}); err != nil {
		t.Fatalf("update files: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("UpdateFiles should fire the reindex hook, total fires = %d, want 2", got)
	}

	// Clearing the hook makes subsequent reloads a no-op for the callback.
	s.SetReindexHook(nil)
	if err := s.Reload(); err != nil {
		t.Fatalf("reload after clear: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("cleared hook must not fire, total fires = %d, want 2", got)
	}
}

// TestRevalidateTrust verifies that adding/revoking a key in the contributor
// keyring re-verifies already-loaded notes in place, with no file re-read — the
// fix for "adding a public key doesn't update; files still show unverified". An
// article that carries a cryptographically valid signature ("unverified") flips
// to "verified" once its key is recognized, and back to "unverified" on revoke.
func TestRevalidateTrust(t *testing.T) {
	dir, err := os.MkdirTemp("", "red-store-trust-*")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	if err := registry.InitRegistry(dir); err != nil {
		t.Fatalf("init registry: %v", err)
	}

	const key = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

	// A note with a valid signature but an unrecognized key starts "unverified".
	art := &models.Article{
		Path:              "/note",
		SignerKey:         key,
		VerificationState: "unverified",
		Verified:          false,
	}
	s := New(dir)
	s.nav = map[string]*models.Section{
		"root": {Name: "root", Articles: []*models.Article{art}},
	}

	// Keyring empty → stays unverified.
	s.RevalidateTrust()
	if art.VerificationState != "unverified" || art.Verified {
		t.Fatalf("before trust: state=%q verified=%v, want unverified/false", art.VerificationState, art.Verified)
	}

	// Recognize the key → flips to verified WITHOUT touching the file.
	if _, err := registry.GetDB().Exec(`INSERT INTO contributors (public_key, name) VALUES (?, ?)`, key, "Alice"); err != nil {
		t.Fatalf("add contributor: %v", err)
	}
	s.RevalidateTrust()
	if art.VerificationState != "verified" || !art.Verified {
		t.Fatalf("after add: state=%q verified=%v, want verified/true", art.VerificationState, art.Verified)
	}
	if art.VerificationError != "" {
		t.Fatalf("after add: verification error should be cleared, got %q", art.VerificationError)
	}

	// Revoke the key → drops back to unverified.
	if _, err := registry.GetDB().Exec(`UPDATE contributors SET revoked = 1 WHERE public_key = ?`, key); err != nil {
		t.Fatalf("revoke contributor: %v", err)
	}
	s.RevalidateTrust()
	if art.VerificationState != "unverified" || art.Verified {
		t.Fatalf("after revoke: state=%q verified=%v, want unverified/false", art.VerificationState, art.Verified)
	}
}

// TestRevalidateTrustLeavesTamperedAndUnsigned ensures RevalidateTrust never
// promotes notes that lack a valid signature, regardless of the keyring.
func TestRevalidateTrustLeavesTamperedAndUnsigned(t *testing.T) {
	dir, err := os.MkdirTemp("", "red-store-trust2-*")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	if err := registry.InitRegistry(dir); err != nil {
		t.Fatalf("init registry: %v", err)
	}

	const key = "11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff"
	if _, err := registry.GetDB().Exec(`INSERT INTO contributors (public_key, name) VALUES (?, ?)`, key, "Mallory"); err != nil {
		t.Fatalf("add contributor: %v", err)
	}

	tampered := &models.Article{Path: "/t", SignerKey: key, VerificationState: "tampered"}
	unsigned := &models.Article{Path: "/u", VerificationState: "unsigned"}
	s := New(dir)
	s.nav = map[string]*models.Section{
		"root": {Name: "root", Articles: []*models.Article{tampered, unsigned}},
	}

	s.RevalidateTrust()
	if tampered.VerificationState != "tampered" || tampered.Verified {
		t.Fatalf("tampered note must stay tampered, got state=%q verified=%v", tampered.VerificationState, tampered.Verified)
	}
	if unsigned.VerificationState != "unsigned" || unsigned.Verified {
		t.Fatalf("unsigned note must stay unsigned, got state=%q verified=%v", unsigned.VerificationState, unsigned.Verified)
	}
}
