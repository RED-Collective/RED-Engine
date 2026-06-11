package fetch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
)

// gitCacheRoot is the directory under which pristine git checkouts are cached,
// keyed by repo URL. Set once at startup (see SetGitCacheRoot) to a path OUTSIDE
// the served data dir so a clone never duplicates content into data/. When unset,
// gitCacheDir falls back to the legacy hidden sibling inside the data dir.
var gitCacheRoot string

// SetGitCacheRoot configures where pristine git checkouts are cached. Pass a
// directory outside the served content dir (e.g. <stateDir>/gitcache) so the
// working clone + .git history never live alongside the published notes.
func SetGitCacheRoot(dir string) { gitCacheRoot = dir }

// GitCachePath returns the cache directory for a repo URL under the configured
// root, or "" when no root is set. Keyed by a hash of the URL so it is stable
// across re-syncs and free of collisions/renames.
func GitCachePath(url string) string {
	if gitCacheRoot == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(gitCacheRoot, hex.EncodeToString(sum[:])[:16])
}

// pullGit syncs a git vault and mirrors it under destDir. The pristine checkout is
// kept in a cache dir OUTSIDE the data dir (see gitCacheRoot) so incremental pulls
// keep working without duplicating the repo into data/, while the served tree under
// destDir is a verbatim copy of the vault's own folders, written by OrganizeVault.
//
// Return contract (consumed by the periodic-sync and webhook reload paths):
//   - nil  → the tree may have been rewritten; caller must do a full reload.
//   - []   → nothing changed (cache already at remote head); caller skips reload.
//
// It never returns a per-file delta list: a git mirror can't cheaply diff which
// notes moved, so a real change always means "reload everything".
func pullGit(url, destDir string) ([]string, error) {
	cacheDir := gitCacheDir(url, destDir)
	// Sweep away any legacy in-data cache from older versions so data/ stops
	// carrying a hidden second copy of the repo.
	if legacy := legacyGitCacheDir(destDir); legacy != cacheDir {
		os.RemoveAll(legacy)
	}
	changed, err := syncGitCache(url, cacheDir)
	if err != nil {
		return nil, err
	}
	// Cache already at the remote's head: skip re-mirror only if the served
	// tree actually exists. On a clean start (data/ was wiped but the cache
	// survived), destDir is absent even though changed=false — we must still
	// mirror so the data directory is populated.
	if !changed {
		if _, statErr := os.Stat(destDir); statErr == nil {
			return []string{}, nil // data is in sync, nothing to do
		}
		// destDir missing: fall through to OrganizeVault below.
	}
	// destDir's base is the source key: it names the top-level content folder and
	// keys the sync ledger (it is the stable startup_sync Filename, shared across
	// every re-sync of this URL).
	dataDir := filepath.Dir(destDir)
	source := filepath.Base(destDir)
	if err := OrganizeVault(cacheDir, dataDir, source); err != nil {
		return nil, fmt.Errorf("organize vault %s: %w", destDir, err)
	}
	return nil, nil
}

// gitCacheDir returns the directory that holds the pristine git checkout for a
// vault. It prefers the configured out-of-data cache root (keyed by URL); if none
// is set it falls back to the legacy hidden sibling inside the data dir.
func gitCacheDir(url, destDir string) string {
	if p := GitCachePath(url); p != "" {
		return p
	}
	return legacyGitCacheDir(destDir)
}

// legacyGitCacheDir is the old in-data cache location: a dot-prefixed sibling of
// the served folder (data/.<name>.gitsrc). Kept so we can detect and delete it.
func legacyGitCacheDir(destDir string) string {
	return filepath.Join(filepath.Dir(destDir), "."+filepath.Base(destDir)+".gitsrc")
}

// syncGitCache clones the repository into cacheDir, or pulls delta updates if it
// already exists. A corrupted/missing checkout is rebuilt from scratch. It reports
// whether the checkout actually changed: true after a fresh clone or a pull that
// merged new commits, false when the cache was already at the remote's head. The
// caller uses this to skip a needless re-mirror + reload when nothing moved.
func syncGitCache(url, cacheDir string) (bool, error) {
	repo, err := git.PlainOpen(cacheDir)
	if err != nil {
		if err == git.ErrRepositoryNotExists {
			log.Printf("📥 Native go-git: Cloning %s into cache %s...", url, cacheDir)
			os.RemoveAll(cacheDir) // clear any leftover garbage so clone is clean
			if err := os.MkdirAll(cacheDir, 0o755); err != nil {
				return false, err
			}
			if _, err := git.PlainClone(cacheDir, false, &git.CloneOptions{
				URL:      url,
				Progress: os.Stdout,
			}); err != nil {
				return false, fmt.Errorf("go-git clone failed: %v", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("failed to check existing repository: %v", err)
	}

	log.Printf("🔄 Native go-git: Pulling delta updates into cache %s...", cacheDir)
	worktree, err := repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("failed to get git worktree: %v", err)
	}
	err = worktree.Pull(&git.PullOptions{
		RemoteName: "origin",
		Force:      true,
		Progress:   os.Stdout,
	})
	if err == git.NoErrAlreadyUpToDate {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("go-git delta pull failed: %v", err)
	}
	return true, nil
}
