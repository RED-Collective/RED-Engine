// Package backup creates and manages zip snapshots of the data directory so a
// bad sync (corrupted, rolled-back, or unsigned content overwriting good notes)
// can be undone. A snapshot stores files with paths relative to the data dir, so
// recovery is just "unzip data-<timestamp>.zip -d data/" — the data/ directory
// itself is never renamed, the user simply unzips a known-good snapshot back over
// the live tree.
package backup

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupInfo describes a single snapshot zip.
type BackupInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	SizeB   int64     `json:"size_bytes"`
	Created time.Time `json:"created"`
}

// CreateDataBackup zips the contents of dataDir into backupDir/data-<timestamp>.zip
// and returns the resulting snapshot's metadata. Files are stored with paths
// relative to dataDir so "unzip <zip> -d data/" restores them in place. Hidden
// directories (.red-ledger, .git, .meta, …) are skipped: they are either
// regeneratable or separately managed, and excluding them keeps the snapshot to
// the published content. On any failure the partial zip is removed so a backup
// dir never accumulates corrupt archives.
func CreateDataBackup(dataDir, backupDir string) (BackupInfo, error) {
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return BackupInfo{}, fmt.Errorf("backup: create dir: %w", err)
	}
	name := "data-" + time.Now().Format("2006-01-02-15-04-05") + ".zip"
	zipPath := filepath.Join(backupDir, name)

	f, err := os.Create(zipPath)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("backup: create zip: %w", err)
	}
	zw := zip.NewWriter(f)

	walkErr := filepath.WalkDir(dataDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dataDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(dataDir, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		w, err := zw.Create(rel)
		if err != nil {
			return fmt.Errorf("backup: add %s: %w", rel, err)
		}
		src, err := os.Open(p)
		if err != nil {
			return nil // file vanished mid-walk; skip rather than abort the snapshot
		}
		defer src.Close()
		if _, err := io.Copy(w, src); err != nil {
			return fmt.Errorf("backup: copy %s: %w", rel, err)
		}
		return nil
	})

	// Close the writer/file before deciding success so the zip is flushed and the
	// stat below sees the final size.
	zwErr := zw.Close()
	fErr := f.Close()
	if walkErr != nil {
		os.Remove(zipPath)
		return BackupInfo{}, walkErr
	}
	if zwErr != nil {
		os.Remove(zipPath)
		return BackupInfo{}, fmt.Errorf("backup: finalize zip: %w", zwErr)
	}
	if fErr != nil {
		os.Remove(zipPath)
		return BackupInfo{}, fmt.Errorf("backup: close zip: %w", fErr)
	}

	var sz int64
	if info, err := os.Stat(zipPath); err == nil {
		sz = info.Size()
	}
	return BackupInfo{Name: name, Path: zipPath, SizeB: sz, Created: time.Now()}, nil
}

// ListBackups returns the snapshot zips in backupDir, newest first. A missing
// backup dir is not an error — it just means no backups exist yet.
func ListBackups(backupDir string) ([]BackupInfo, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BackupInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		var sz int64
		var mod time.Time
		if info, err := e.Info(); err == nil {
			sz = info.Size()
			mod = info.ModTime()
		}
		out = append(out, BackupInfo{
			Name:    e.Name(),
			Path:    filepath.Join(backupDir, e.Name()),
			SizeB:   sz,
			Created: mod,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// PruneBackups deletes the oldest snapshots in backupDir, keeping the `keep` most
// recent. keep <= 0 means keep everything (no pruning).
func PruneBackups(backupDir string, keep int) error {
	if keep <= 0 {
		return nil
	}
	list, err := ListBackups(backupDir)
	if err != nil {
		return err
	}
	for i := keep; i < len(list); i++ {
		os.Remove(list[i].Path)
	}
	return nil
}
