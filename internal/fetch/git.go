package fetch

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
)

// pullGit syncs a git vault and mirrors it under destDir. The pristine checkout is
// kept in a hidden sibling cache dir so incremental pulls keep working, while the
// served tree under destDir is a verbatim copy of the vault's own folders, written
// by OrganizeVault. It always returns a nil changed-file list, which signals the
// caller to do a full reload (the whole tree may have been rewritten).
func pullGit(url, destDir string) ([]string, error) {
	cacheDir := gitCacheDir(destDir)
	if err := syncGitCache(url, cacheDir); err != nil {
		return nil, err
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

// gitCacheDir returns the hidden directory that holds the pristine git checkout
// for a vault served at destDir. It is a dot-prefixed sibling so the store's
// reload and the navigation scanner (both of which skip dot-directories) never
// index the unorganized source files.
func gitCacheDir(destDir string) string {
	return filepath.Join(filepath.Dir(destDir), "."+filepath.Base(destDir)+".gitsrc")
}

// syncGitCache clones the repository into cacheDir, or pulls delta updates if it
// already exists. A corrupted/missing checkout is rebuilt from scratch.
func syncGitCache(url, cacheDir string) error {
	repo, err := git.PlainOpen(cacheDir)
	if err != nil {
		if err == git.ErrRepositoryNotExists {
			log.Printf("📥 Native go-git: Cloning %s into cache %s...", url, cacheDir)
			os.RemoveAll(cacheDir) // clear any leftover garbage so clone is clean
			if err := os.MkdirAll(cacheDir, 0o755); err != nil {
				return err
			}
			if _, err := git.PlainClone(cacheDir, false, &git.CloneOptions{
				URL:      url,
				Progress: os.Stdout,
			}); err != nil {
				return fmt.Errorf("go-git clone failed: %v", err)
			}
			return nil
		}
		return fmt.Errorf("failed to check existing repository: %v", err)
	}

	log.Printf("🔄 Native go-git: Pulling delta updates into cache %s...", cacheDir)
	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get git worktree: %v", err)
	}
	err = worktree.Pull(&git.PullOptions{
		RemoteName: "origin",
		Force:      true,
		Progress:   os.Stdout,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("go-git delta pull failed: %v", err)
	}
	return nil
}
