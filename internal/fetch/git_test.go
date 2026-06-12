package fetch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// commitNote writes name into repoDir's worktree and commits it, advancing the
// repo's branch so a downstream clone/pull has something new to fetch.
func commitNote(t *testing.T, repo *git.Repository, repoDir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoDir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("add "+name, &git.CommitOptions{
		Author: &object.Signature{Name: "Tester", Email: "t@example.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestPullGitReloadContract pins the return contract the periodic-sync and webhook
// reload paths depend on: a real change (fresh clone or a pull that merged new
// commits) returns a nil slice ("full reload needed"), while an already-up-to-date
// pull returns a NON-nil empty slice ("nothing changed, skip reload"). Confusing
// these two is exactly the regression that made pushes stop showing up without a
// restart (periodicSync only reloaded when len(changed) > 0, so git's always-nil
// result was read as "nothing to do").
func TestPullGitReloadContract(t *testing.T) {
	remote := t.TempDir()
	repo, err := git.PlainInit(remote, false)
	if err != nil {
		t.Fatalf("init remote: %v", err)
	}
	commitNote(t, repo, remote, "first.md", "# First\n")

	SetGitCacheRoot(t.TempDir())
	t.Cleanup(func() { SetGitCacheRoot("") })

	data := t.TempDir()
	dest := filepath.Join(data, "Vault")

	// 1) First pull = clone → nil (full reload) and the note is mirrored to data/.
	changed, err := pullGit(remote, dest)
	if err != nil {
		t.Fatalf("clone pull: %v", err)
	}
	if changed != nil {
		t.Fatalf("clone should signal a full reload (nil), got %#v", changed)
	}
	if _, err := os.Stat(filepath.Join(dest, "first.md")); err != nil {
		t.Fatalf("first.md was not mirrored into data/: %v", err)
	}

	// 2) No new commits upstream → NON-nil empty slice (no reload), nothing re-written.
	changed, err = pullGit(remote, dest)
	if err != nil {
		t.Fatalf("up-to-date pull: %v", err)
	}
	if changed == nil {
		t.Fatalf("up-to-date pull must NOT signal a reload, got nil")
	}
	if len(changed) != 0 {
		t.Fatalf("up-to-date pull should report no changed files, got %#v", changed)
	}

	// 3) A new commit upstream → nil (full reload) and the new note appears in data/.
	commitNote(t, repo, remote, "second.md", "# Second\n")
	changed, err = pullGit(remote, dest)
	if err != nil {
		t.Fatalf("delta pull: %v", err)
	}
	if changed != nil {
		t.Fatalf("delta pull should signal a full reload (nil), got %#v", changed)
	}
	if _, err := os.Stat(filepath.Join(dest, "second.md")); err != nil {
		t.Fatalf("second.md was not mirrored after the delta pull: %v", err)
	}

	// 4) Simulate a clean restart: delete data/ but leave the cache intact.
	// The cache is still at the remote's head (no new commits), so syncGitCache
	// returns changed=false — but data/ is gone, so we must still mirror.
	if err := os.RemoveAll(dest); err != nil {
		t.Fatalf("remove data dir: %v", err)
	}
	changed, err = pullGit(remote, dest)
	if err != nil {
		t.Fatalf("post-wipe pull: %v", err)
	}
	// A missing data dir must trigger a full reload (nil), not the no-op path.
	if changed != nil {
		t.Fatalf("post-wipe pull must signal full reload (nil), got %#v", changed)
	}
	if _, err := os.Stat(filepath.Join(dest, "first.md")); err != nil {
		t.Fatalf("post-wipe: first.md not re-mirrored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "second.md")); err != nil {
		t.Fatalf("post-wipe: second.md not re-mirrored: %v", err)
	}
}
