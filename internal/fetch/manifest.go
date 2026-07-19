package fetch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ManifestFile is one entry in a content manifest: the per-file hash and the
// signature data needed to verify it (empty when the file is unsigned).
type ManifestFile struct {
	FileHash  string `json:"file_hash"`
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

// ContentManifest describes a content bucket for peer sync. Files are keyed by
// their path relative to the bucket root, which a partial pull mirrors verbatim.
type ContentManifest struct {
	Bucket string                  `json:"bucket"`
	Files  map[string]ManifestFile `json:"files"`
}

// GenerateManifest builds the manifest for a content path. prefix is the
// requested path under dataDir (a top-level content folder like "My-Repo" or a
// subfolder like "My-Repo/networking"); only files under it are listed, but their
// keys are always full paths from the top-level folder root so a partial pull
// still reconstructs the full tree.
func GenerateManifest(dataDir, prefix string) (ContentManifest, error) {
	prefix = strings.Trim(filepath.ToSlash(prefix), "/")
	if prefix == "" || strings.Contains(prefix, "..") {
		return ContentManifest{}, fmt.Errorf("invalid content path %q", prefix)
	}
	bucket := strings.SplitN(prefix, "/", 2)[0]
	bucketDir := filepath.Join(dataDir, bucket)
	walkRoot := filepath.Join(dataDir, filepath.FromSlash(prefix))

	files := make(map[string]ManifestFile)

	filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != walkRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		// List notes AND the image assets they embed, so a peer pull transfers both.
		// Images carry no signature; the manifest's FileHash gives transfer integrity,
		// the same as for a note (whose authenticity is verified from its frontmatter
		// after pulling).
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") && !IsSyncableAsset(d.Name()) {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(bucketDir, path)
		if relErr != nil {
			return nil
		}
		// The signature/pubkey live inside the note's frontmatter (which the puller
		// downloads verbatim), so the manifest only needs the file hash for transfer
		// integrity — peers verify authenticity from the header after pulling.
		sum := sha256.Sum256(content)
		files[filepath.ToSlash(rel)] = ManifestFile{FileHash: hex.EncodeToString(sum[:])}
		return nil
	})

	return ContentManifest{
		Bucket: bucket,
		Files:  files,
	}, nil
}
