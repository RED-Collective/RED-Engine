package fetch

import (
	"crypto/sha256"
	"database/sql"
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

	sigs := loadBucketSignatures(bucketDir)
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
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
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
		sum := sha256.Sum256(content)
		h := hex.EncodeToString(sum[:])
		mf := ManifestFile{FileHash: h}
		if s, ok := sigs[h]; ok {
			mf.PublicKey, mf.Signature = s.PublicKey, s.Signature
		}
		files[filepath.ToSlash(rel)] = mf
		return nil
	})

	return ContentManifest{
		Bucket: bucket,
		Files:  files,
	}, nil
}

// loadBucketSignatures aggregates files(file_hash → pubkey/sig) from every
// signer.db under a `.red-*` dir in the bucket, so signatures can be attached to
// notes by content hash regardless of their on-disk path.
func loadBucketSignatures(bucketDir string) map[string]ManifestFile {
	out := make(map[string]ManifestFile)
	filepath.WalkDir(bucketDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() != "signer.db" || !strings.HasPrefix(filepath.Base(filepath.Dir(path)), ".red-") {
			return nil
		}
		db, e := sql.Open("sqlite", path)
		if e != nil {
			return nil
		}
		rows, e := db.Query(`SELECT file_hash, public_key, signature FROM files`)
		if e != nil {
			db.Close()
			return nil
		}
		for rows.Next() {
			var fh, pk, sig string
			if rows.Scan(&fh, &pk, &sig) == nil && fh != "" {
				out[fh] = ManifestFile{FileHash: fh, PublicKey: pk, Signature: sig}
			}
		}
		rows.Close()
		db.Close()
		return nil
	})
	return out
}

