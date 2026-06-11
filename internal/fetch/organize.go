package fetch

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// OrganizeVault mirrors a vault checkout at srcDir verbatim into
// dataDir/<source>/<path-within-vault>. The source's own folder structure IS the
// published structure — there is no taxonomy and no library/manual bucket. Each
// note carries its signature in its own frontmatter (red_author/red_sig/red_hash),
// so the mirrored notes are self-verifying — nothing extra is copied. The tree is
// merged in place (MkdirAll only creates missing segments) and a note is rewritten
// only when its content differs (idempotent). source keys this sync's ledger so a
// later re-sync cleans up notes it no longer provides AND names the top-level
// folder; pass "" only for untracked writes to the data root.
func OrganizeVault(srcDir, dataDir, source string) error {
	// Each sync lands under its own top-level folder named after the source so a
	// repo's notes stay grouped and attributed. A blank/unsafe source writes to
	// the data root.
	top := SafeFolderSegment(source)

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
		// Mirror notes AND the image assets they embed. Obsidian stores attachments
		// (e.g. "Pasted image ….png") alongside or near the note and references them
		// with ![[name]]; copying only .md silently dropped every image, so embeds
		// 404'd and attachment-only folders vanished. Everything else (PDFs, binaries)
		// is still skipped — see IsSyncableAsset.
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") && !IsSyncableAsset(d.Name()) {
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
	// the new set. Signatures live in each note's frontmatter, so there is nothing
	// else to preserve.
	ReconcileLedger(dataDir, source, written)
	return nil
}

// syncableImageExts is the set of image extensions mirrored alongside notes during a
// sync. Restricted to images on purpose: it covers Obsidian's embedded attachments
// without syncing arbitrary binaries from a content repo.
var syncableImageExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".svg":  true,
	".webp": true,
	".bmp":  true,
	".ico":  true,
}

// IsSyncableAsset reports whether name is a non-markdown asset that a sync should
// mirror (currently: images embedded by notes). It is the single source of truth
// shared by OrganizeVault (git) and GenerateManifest (peer) so both paths transfer
// exactly the same set of files.
func IsSyncableAsset(name string) bool {
	return syncableImageExts[strings.ToLower(filepath.Ext(name))]
}

// SafeFolderSegment turns a sync source/label into a safe single path segment used
// as the sync's top-level folder (dataDir/<segment>/…). It returns "" when the
// source is empty or would resolve to a hidden or relative segment. It strips any
// directory part (so "a/b" → "b") and leading dots (so it never creates a hidden
// dir), which is exactly the guard a caller needs before using an admin-supplied
// name as a destination folder.
func SafeFolderSegment(source string) string {
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
	w, err := WriteNote(dataDir, SafeFolderSegment(source), fileName, content)
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

// hasSignature reports whether content carries a non-empty red_sig frontmatter
// field — i.e. it is a signed note rather than plain/unsigned content.
func hasSignature(b []byte) bool {
	return FrontmatterValue(b, "red_sig") != ""
}

// parseSignedAt reads red_signed_at from content. red-feather writes RFC1123
// ("Mon, 02 Jan 2006 15:04:05 MST"); the ISO layout is a fallback for older
// exports. Returns zero when absent or unparseable.
func parseSignedAt(b []byte) time.Time {
	v := FrontmatterValue(b, "red_signed_at")
	if v == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC1123, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// writeIfChanged writes content to dest only when dest is missing or differs,
// with conflict resolution that protects already-published content:
//
//   - Identical content (same SHA256) is a no-op, keeping re-syncs idempotent and
//     avoiding mtime churn (which would needlessly retrigger the file watcher).
//   - A signed note on disk is NEVER overwritten by unsigned content. A federated
//     or re-imported source that ships an older, unsigned export of a note can no
//     longer clobber the signed copy.
//   - Between two signed notes, the one with the newer red_signed_at wins; a stale
//     peer replaying an older signed version cannot roll the note back.
//
// Unsigned-vs-unsigned and any case without usable timestamps fall through to
// last-writer-wins, unchanged from the original behaviour. Images (no frontmatter)
// are treated as unsigned on both sides and so still update on content change.
func writeIfChanged(dest string, content []byte) error {
	if existing, err := os.ReadFile(dest); err == nil {
		if sha256.Sum256(existing) == sha256.Sum256(content) {
			return nil // identical — skip
		}
		// Never overwrite a signed note with an unsigned one.
		if hasSignature(existing) && !hasSignature(content) {
			return nil
		}
		// Between two signed notes, keep the newer red_signed_at.
		existingAt := parseSignedAt(existing)
		incomingAt := parseSignedAt(content)
		if !existingAt.IsZero() && !incomingAt.IsZero() && !incomingAt.After(existingAt) {
			return nil
		}
	}
	return os.WriteFile(dest, content, 0o644)
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
