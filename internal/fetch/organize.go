package fetch

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// OrganizeVault mirrors a vault checkout at srcDir verbatim into
// dataDir/<source>/<path-within-vault>. The source's own folder structure IS the
// published structure — there is no taxonomy and no library/manual bucket. The
// vault's signer.db is copied alongside (under dataDir/<source>/.red-feather--<source>)
// so notes keep verifying (verification is keyed by content hash, so on-disk
// location is irrelevant) and so the content can be re-exported to peers. The tree
// is merged in place (MkdirAll only creates missing segments) and a note is
// rewritten only when its content differs (idempotent). source keys this sync's
// ledger so a later re-sync cleans up notes it no longer provides AND names the
// top-level folder; pass "" only for untracked writes to the data root.
func OrganizeVault(srcDir, dataDir, source string) error {
	signerDBPath := findSignerDB(srcDir)
	hasVault := signerDBPath != ""

	// Each sync lands under its own top-level folder named after the source so a
	// repo's notes stay grouped and attributed. A blank/unsafe source writes to
	// the data root.
	top := sourceFolder(source)

	var written []string
	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != srcDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir // skip .git, .red-feather, .obsidian, .meta, ...
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		content, readErr := os.ReadFile(p)
		if readErr != nil {
			log.Printf("[Organize] read %s: %v", p, readErr)
			return nil
		}
		rel, relErr := filepath.Rel(srcDir, p)
		if relErr != nil {
			return nil
		}
		w, wErr := WriteNote(dataDir, top, filepath.ToSlash(rel), content)
		if wErr != nil {
			log.Printf("[Organize] write %s: %v", rel, wErr)
			return nil
		}
		written = append(written, w)
		return nil
	})
	if err != nil {
		return fmt.Errorf("organize: walk %s: %w", srcDir, err)
	}

	// Clean up notes this source wrote before but no longer provides, then record
	// the new set.
	ReconcileLedger(dataDir, source, written)

	// Preserve the signature data so the mirrored notes still verify and so the
	// content can be re-exported to peers. Keyed by sync source so several vaults
	// under one data root keep their own signer.db.
	if hasVault {
		dir := dataDir
		if top != "" {
			dir = filepath.Join(dataDir, top)
		}
		if cErr := copySignerDB(signerDBPath, dir, source); cErr != nil {
			log.Printf("[Organize] preserve signer.db: %v", cErr)
		}
	}
	return nil
}

// sourceFolder turns a sync source/label into a safe single path segment used as
// the sync's top-level folder (dataDir/<sourceFolder>/…). It returns "" when the
// source is empty or would resolve to a hidden or relative segment, which writes
// to the data root instead.
func sourceFolder(source string) string {
	seg := strings.TrimSpace(filepath.Base(source))
	seg = strings.TrimLeft(seg, ".") // never create a hidden directory
	if seg == "" || seg == ".." {
		return ""
	}
	return seg
}

// OrganizeLooseMarkdown files a single vaultless .md under its source top-level
// folder (dataDir/<source>/<fileName>), preserving fileName, and records it in the
// source's ledger so it is removable like a vault sync. It is nested under a folder
// (not written to the data root) so the navigation scanner, which only indexes
// top-level directories, can see it.
func OrganizeLooseMarkdown(dataDir, source, fileName string, content []byte) error {
	w, err := WriteNote(dataDir, sourceFolder(source), fileName, content)
	if err != nil {
		return err
	}
	ReconcileLedger(dataDir, source, []string{w})
	return nil
}

// WriteNote writes content to dataDir/<top>/<relPath>, creating only the missing
// directory segments and overwriting only when content differs. It returns the
// dataDir-relative slash path it wrote (for the sync ledger).
func WriteNote(dataDir, top, relPath string, content []byte) (string, error) {
	rel := path.Join(top, relPath)
	dest := filepath.Join(dataDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := writeIfChanged(dest, content); err != nil {
		return "", err
	}
	return rel, nil
}

// writeIfChanged writes content to dest only when dest is missing or differs.
// Skipping no-op writes keeps re-syncs idempotent and avoids churning mtimes
// (which would needlessly retrigger the file watcher).
func writeIfChanged(dest string, content []byte) error {
	if existing, err := os.ReadFile(dest); err == nil {
		if sha256.Sum256(existing) == sha256.Sum256(content) {
			return nil
		}
	}
	return os.WriteFile(dest, content, 0o644)
}

// findSignerDB returns the path to the first signer.db located under a `.red-*`
// directory inside srcDir, or "" if none exists.
func findSignerDB(srcDir string) string {
	var found string
	filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" || d.IsDir() {
			return nil
		}
		if d.Name() == "signer.db" && strings.HasPrefix(filepath.Base(filepath.Dir(path)), ".red-") {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// copySignerDB copies the vault's signer.db into a stable hidden directory under
// bucketDir so its signatures keep loading after a temporary checkout is removed.
// The directory is keyed by the sync source so several vaults merged into one
// bucket coexist; the store loads every signer.db under any `.red-*` dir.
func copySignerDB(srcPath, bucketDir, key string) error {
	key = sanitizeKey(key)
	if key == "" {
		key = "default"
	}
	destDir := filepath.Join(bucketDir, ".red-feather--"+key)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	return copyFile(srcPath, filepath.Join(destDir, "signer.db"))
}

// sanitizeKey keeps a source/pubkey usable as a single path segment.
func sanitizeKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
