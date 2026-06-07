package navigation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/fetch"
)

// scanSeen records the folder paths and guide file paths encountered during a
// single scan so rows for content that has since been deleted, renamed, or
// re-bucketed can be pruned afterwards.
type scanSeen struct {
	folders map[string]bool
	guides  map[string]bool
}

// ScanDataDirectories walks every top-level vault under dataDir and rebuilds the
// navigation index inside a single transaction.
func (s *Service) ScanDataDirectories() (*ScanResult, error) {
	start := time.Now()
	result := &ScanResult{StartTime: start.Format(time.RFC3339)}

	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, fmt.Errorf("read data dir %s: %w", s.dataDir, err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin scan tx: %w", err)
	}
	defer tx.Rollback()

	seen := &scanSeen{folders: map[string]bool{}, guides: map[string]bool{}}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		absPath := filepath.Join(s.dataDir, name)
		folders, guides, errs := s.scanDir(tx, absPath, name, name, nil, seen)
		result.FoldersScanned += folders
		result.GuidesIndexed += guides
		result.Errors = append(result.Errors, errs...)
	}

	// Drop folders/guides that no longer exist on disk so the index reflects the
	// current filesystem rather than accumulating every path ever scanned.
	if err := s.pruneStale(tx, seen); err != nil {
		return nil, fmt.Errorf("prune stale nav entries: %w", err)
	}

	if err := s.updateAggregates(tx); err != nil {
		return nil, fmt.Errorf("update aggregates: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit scan tx: %w", err)
	}

	end := time.Now()
	result.EndTime = end.Format(time.RFC3339)
	result.Duration = end.Sub(start).String()
	return result, nil
}

// scanDir indexes a single folder and recurses into its subdirectories.
// relPath is the path stored in the database (relative to dataDir, slash-separated).
// contentType is the top-level vault name and is propagated to every descendant.
func (s *Service) scanDir(dbtx DBTX, absPath, relPath, contentType string, parentID *int64, seen *scanSeen) (int, int, []string) {
	var folders, guides int
	var errs []string

	relPath = filepath.ToSlash(relPath)
	seen.folders[relPath] = true

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return 0, 0, []string{fmt.Sprintf("read %s: %v", relPath, err)}
	}

	var mdFiles []os.DirEntry
	var subDirs []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			subDirs = append(subDirs, e)
		} else if strings.EqualFold(filepath.Ext(name), ".md") {
			mdFiles = append(mdFiles, e)
		}
	}

	// A folder is a leaf when it holds markdown files and no further subfolders.
	isLeaf := len(mdFiles) > 0 && len(subDirs) == 0

	// Derive a description from index.md if present.
	description := ""
	for _, f := range mdFiles {
		if strings.EqualFold(f.Name(), "index.md") {
			if content, err := os.ReadFile(filepath.Join(absPath, f.Name())); err == nil {
				description = ExtractFirstParagraph(string(content))
			}
			break
		}
	}

	displayName := HumanizeFolder(filepath.Base(relPath))
	folderID, err := s.upsertFolder(dbtx, relPath, displayName, description, contentType, parentID, isLeaf)
	if err != nil {
		return 0, 0, []string{err.Error()}
	}

	folders++

	for _, f := range mdFiles {
		fileAbs := filepath.Join(absPath, f.Name())
		fileRel := relPath + "/" + f.Name()
		seen.guides[fileRel] = true
		content, err := os.ReadFile(fileAbs)
		if err != nil {
			errs = append(errs, fmt.Sprintf("read %s: %v", fileRel, err))
			continue
		}
		title := ExtractFirstHeading(string(content))
		if title == "" {
			title = HumanizeFolder(strings.TrimSuffix(f.Name(), filepath.Ext(f.Name())))
		}
		preview := ExtractFirstParagraph(string(content))
		wordCount := CountWords(string(content))

		var modTime time.Time
		if info, err := f.Info(); err == nil {
			modTime = info.ModTime()
		}

		guideID, err := s.upsertGuide(dbtx, folderID, f.Name(), fileRel, title, preview, wordCount, modTime)
		if err != nil {
			errs = append(errs, fmt.Sprintf("index %s: %v", fileRel, err))
			continue
		}
		if err := s.setGuideTags(dbtx, guideID, fetch.FrontmatterTags(content)); err != nil {
			errs = append(errs, fmt.Sprintf("tag %s: %v", fileRel, err))
		}
		guides++
	}

	for _, d := range subDirs {
		childAbs := filepath.Join(absPath, d.Name())
		childRel := relPath + "/" + d.Name()
		fID := folderID
		f, g, e := s.scanDir(dbtx, childAbs, childRel, contentType, &fID, seen)
		folders += f
		guides += g
		errs = append(errs, e...)
	}

	return folders, guides, errs
}

// pruneStale deletes nav_folders and nav_guides rows whose on-disk counterparts
// were not encountered in the current scan, so deleted/renamed/re-bucketed
// content stops appearing as phantom cards and dead links. Foreign-key cascade
// is not enabled on the shared connection, so guides and overrides orphaned by a
// folder deletion are removed explicitly. Surviving folders keep their IDs, so
// description overrides for still-present folders are preserved.
func (s *Service) pruneStale(dbtx DBTX, seen *scanSeen) error {
	staleGuides, err := staleIDs(dbtx, "SELECT id, file_path FROM nav_guides", seen.guides)
	if err != nil {
		return err
	}
	for _, id := range staleGuides {
		if _, err := dbtx.Exec(`DELETE FROM nav_guides WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete stale guide %d: %w", id, err)
		}
	}

	staleFolders, err := staleIDs(dbtx, "SELECT id, path FROM nav_folders", seen.folders)
	if err != nil {
		return err
	}
	for _, id := range staleFolders {
		if _, err := dbtx.Exec(`DELETE FROM nav_folders WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete stale folder %d: %w", id, err)
		}
	}

	// Description overrides and guide tags cascade off the connection, so drop any
	// left dangling by a deleted folder/guide.
	if _, err := dbtx.Exec(`DELETE FROM nav_description_overrides
		WHERE folder_id NOT IN (SELECT id FROM nav_folders)`); err != nil {
		return fmt.Errorf("prune orphan overrides: %w", err)
	}
	if _, err := dbtx.Exec(`DELETE FROM nav_guide_tags
		WHERE guide_id NOT IN (SELECT id FROM nav_guides)`); err != nil {
		return fmt.Errorf("prune orphan tags: %w", err)
	}
	return nil
}

// staleIDs runs query (which must select an int id and a text key) and returns
// the ids whose key is absent from seen.
func staleIDs(dbtx DBTX, query string, seen map[string]bool) ([]int64, error) {
	rows, err := dbtx.Query(query)
	if err != nil {
		return nil, fmt.Errorf("scan for prune: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, fmt.Errorf("scan prune row: %w", err)
		}
		if !seen[key] {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// VerifyNavigationDB reports navigation folders whose parent_id points at a
// non-existent folder.
func (s *Service) VerifyNavigationDB() error {
	var orphans int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM nav_folders
		WHERE parent_id IS NOT NULL
		  AND parent_id NOT IN (SELECT id FROM nav_folders)`).Scan(&orphans)
	if err != nil {
		return fmt.Errorf("verify nav db: %w", err)
	}
	if orphans > 0 {
		return fmt.Errorf("found %d orphaned folder reference(s)", orphans)
	}
	return nil
}
