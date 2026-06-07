package fetch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ledgerDirName holds one JSON file per sync source listing the dataDir-relative
// paths that source last wrote. It lets a re-sync clean up notes the source no
// longer provides (e.g. after a vault is reclassified into a different branch)
// and lets removal delete exactly one source's files without touching the shared
// bucket. It is a dot-dir, so store.Reload and the nav scanner skip it.
const ledgerDirName = ".red-ledger"

// ledgerFile returns the ledger path for a sync source. The source key (a sync
// URL or peer ref) is hashed so any string is a safe, collision-free filename.
func ledgerFile(dataDir, source string) string {
	sum := sha256.Sum256([]byte(source))
	name := sanitizeKey(source) + "-" + hex.EncodeToString(sum[:])[:8]
	return filepath.Join(dataDir, ledgerDirName, name+".json")
}

func readLedger(dataDir, source string) []string {
	b, err := os.ReadFile(ledgerFile(dataDir, source))
	if err != nil {
		return nil
	}
	var paths []string
	if json.Unmarshal(b, &paths) != nil {
		return nil
	}
	return paths
}

func writeLedger(dataDir, source string, paths []string) error {
	p := ledgerFile(dataDir, source)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	sort.Strings(paths)
	b, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// ReconcileLedger deletes files a source wrote previously but not in newPaths
// (pruning now-empty directories), then records newPaths. newPaths are
// dataDir-relative slash paths. A blank source disables ledger tracking.
func ReconcileLedger(dataDir, source string, newPaths []string) {
	if source == "" {
		return
	}
	keep := make(map[string]bool, len(newPaths))
	for _, p := range newPaths {
		keep[p] = true
	}
	for _, old := range readLedger(dataDir, source) {
		if !keep[old] {
			full := filepath.Join(dataDir, filepath.FromSlash(old))
			if os.Remove(full) == nil {
				pruneEmptyParents(dataDir, filepath.Dir(full))
			}
		}
	}
	if err := writeLedger(dataDir, source, newPaths); err != nil {
		// Best-effort: a missing ledger only loses stale-cleanup, not content.
		_ = err
	}
}

// RemoveBySource deletes every file a sync source wrote and forgets the ledger.
// Used by the admin "remove + delete local files" action so it targets exactly
// the source's notes instead of the whole shared bucket.
func RemoveBySource(dataDir, source string) {
	if source == "" {
		return
	}
	for _, p := range readLedger(dataDir, source) {
		full := filepath.Join(dataDir, filepath.FromSlash(p))
		if os.Remove(full) == nil {
			pruneEmptyParents(dataDir, filepath.Dir(full))
		}
	}
	os.Remove(ledgerFile(dataDir, source))
}

// pruneEmptyParents removes dir and its now-empty ancestors, stopping at (and
// never removing) stopAt.
func pruneEmptyParents(stopAt, dir string) {
	stopAt = filepath.Clean(stopAt)
	for {
		dir = filepath.Clean(dir)
		if dir == stopAt || !strings.HasPrefix(dir, stopAt) || dir == filepath.Dir(dir) {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
