package store

import (
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	_ "database/sql"
	"encoding/hex"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RED-Collective/red-engine/internal/fetch"
	"github.com/RED-Collective/red-engine/internal/models"
	"github.com/RED-Collective/red-engine/internal/registry"
	"github.com/RED-Collective/red-engine/internal/render"

	"github.com/radovskyb/watcher"
)

type Store struct {
	dataDir          string
	nav              map[string]*models.Section
	mu               sync.RWMutex
	remoteSyncActive atomic.Bool
	remoteSyncEnd    atomic.Int64
}

type SearchItem struct {
	Title string   `json:"title"`
	Path  string   `json:"path"`
	Tags  []string `json:"tags,omitempty"`
}

func New(dataDir string) *Store {
	return &Store{
		dataDir: dataDir,
		nav:     make(map[string]*models.Section),
	}
}

func (s *Store) DataDir() string { return s.dataDir }
func (s *Store) Nav() map[string]*models.Section {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nav
}

// =====================================================================
// FILE WATCHER & CONCURRENCY
// =====================================================================

func (s *Store) Watch() error {
	w := watcher.New()

	w.FilterOps(watcher.Write, watcher.Create, watcher.Remove, watcher.Rename)

	go func() {
		for {
			select {
			case event := <-w.Event:
				if s.ShouldIgnoreLocalEvents() {
					log.Printf("🛡️ Ignored local event for %s (Remote sync active)", event.Path)
					continue
				}
				log.Printf("🔄 Local file change detected: %s", event.Path)
				if err := s.UpdateFiles([]string{event.Path}); err != nil {
					log.Printf("⚠️ Hot-reload failed for %s, falling back to full reload", event.Path)
					s.Reload()
				}
			case err := <-w.Error:
				log.Println("⚠️ Watcher error:", err)
			case <-w.Closed:
				return
			}
		}
	}()

	absDataDir, _ := filepath.Abs(s.dataDir)
	if err := w.AddRecursive(absDataDir); err != nil {
		return err
	}

	log.Printf("[DEBUG] File watcher interval polling started on %s", absDataDir)
	go func() {
		if err := w.Start(2 * time.Second); err != nil {
			log.Fatalln(err)
		}
	}()
	return nil
}

func (s *Store) BeginRemoteSync() { s.remoteSyncActive.Store(true) }
func (s *Store) EndRemoteSync() {
	s.remoteSyncEnd.Store(time.Now().UnixNano())
	s.remoteSyncActive.Store(false)
}

func (s *Store) ShouldIgnoreLocalEvents() bool {
	if s.remoteSyncActive.Load() {
		return true
	}
	lastEnd := s.remoteSyncEnd.Load()
	if lastEnd > 0 && time.Since(time.Unix(0, lastEnd)) < 4*time.Second {
		return true
	}
	return false
}

// =====================================================================
// STATE MANAGEMENT (Reload & Granular Update)
// =====================================================================

func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	allSignatures := s.loadSecurityData()
	recognized := registry.RecognizedContributorKeys()
	newNav := make(map[string]*models.Section)

	err := filepath.WalkDir(s.dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Skip metadata/source dirs (.git, .red-feather, .meta, the hidden
			// .<vault>.gitsrc cache) so the pristine clone is never indexed.
			if path != s.dataDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		art, parts, err := s.processArticle(path, allSignatures, recognized)
		if err == nil && art != nil {
			s.insertIntoMap(newNav, parts, art)
		} else if err != nil {
			log.Printf("⚠️ Failed to parse article %s: %v", path, err)
		}
		return nil
	})

	if err != nil {
		return err
	}
	s.checkMetaFiles(newNav)
	s.nav = newNav
	return nil
}

func (s *Store) checkMetaFiles(nav map[string]*models.Section) {
	for name, sec := range nav {
		if name == "root" {
			continue
		}
		metaDir := filepath.Join(s.dataDir, name, ".meta")
		if _, err := os.Stat(filepath.Join(metaDir, "cover.jpg")); err == nil {
			sec.HasCover = true
		}
		if _, err := os.Stat(filepath.Join(metaDir, "icon.svg")); err == nil {
			sec.HasIcon = true
		}
		for subName, sub := range sec.Sub {
			subMeta := filepath.Join(s.dataDir, name, subName, ".meta")
			if _, err := os.Stat(filepath.Join(subMeta, "cover.jpg")); err == nil {
				sub.HasCover = true
			}
			if _, err := os.Stat(filepath.Join(subMeta, "icon.svg")); err == nil {
				sub.HasIcon = true
			}
		}
	}
}

func (s *Store) UpdateFiles(changedPaths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range changedPaths {
		p = filepath.Clean(p)
		if filepath.Ext(p) != ".md" {
			continue
		}

		// FIX: Bulletproof absolute path resolution for the file watcher
		absP, _ := filepath.Abs(p)
		absData, _ := filepath.Abs(s.dataDir)
		rel, err := filepath.Rel(absData, absP)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // Block traversal escapes
		}

		cleanPath := strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(rel), "/"), ".md")
		parts := strings.Split(filepath.ToSlash(cleanPath), "/")

		s.removeFromMap(s.nav, parts)

		allSignatures := s.loadSecurityData()
		recognized := registry.RecognizedContributorKeys()

		art, _, err := s.processArticle(p, allSignatures, recognized)
		if err == nil && art != nil {
			s.insertIntoMap(s.nav, parts, art)
		} else if err != nil {
			log.Printf("⚠️ Failed to hot-patch article %s: %v", p, err)
		}
	}
	return nil
}

// =====================================================================
// DATA PROCESSING HELPERS
// =====================================================================

func (s *Store) loadSecurityData() map[string]models.ManifestEntry {
	allSignatures := make(map[string]models.ManifestEntry)
	filepath.WalkDir(s.dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "signer.db" {
			return nil
		}
		// Accept any signer.db under a `.red-*` directory. Vaults signed by
		// RED-Feather use `.red-feather/`; older ones use `.red-signer/`; the
		// organizer copies each source's db into `.red-feather--<source>/`.
		if !strings.HasPrefix(filepath.Base(filepath.Dir(path)), ".red-") {
			return nil
		}
		signerDB, err := sql.Open("sqlite", path)
		if err != nil {
			log.Printf("Warning: cannot open signer.db at %s: %v", path, err)
			return nil
		}
		defer signerDB.Close()

		vaultRoot := filepath.Dir(filepath.Dir(path))
		relDir, _ := filepath.Rel(s.dataDir, vaultRoot)
		relDir = filepath.ToSlash(relDir)

		rows, err := signerDB.Query(`SELECT path, file_hash, public_key, signature FROM files`)
		if err != nil {
			log.Printf("Warning: cannot read signer.db at %s: %v", path, err)
			return nil
		}
		defer rows.Close()

		for rows.Next() {
			var filePath, fileHash, pubKey, sig string
			if err := rows.Scan(&filePath, &fileHash, &pubKey, &sig); err != nil {
				continue
			}
			entry := models.ManifestEntry{
				FileHash:  fileHash,
				Hash:      fileHash,
				PublicKey: pubKey,
				Signature: sig,
			}
			fullKey := filepath.ToSlash(filePath)
			if relDir != "." && !strings.HasPrefix(fullKey, relDir+"/") {
				fullKey = filepath.ToSlash(filepath.Join(relDir, filePath))
			}
			allSignatures[fullKey] = entry
			// Also index by content hash so a note still verifies after being
			// moved, where its on-disk path no longer matches signer.db's record.
			if fileHash != "" {
				allSignatures["sha256:"+fileHash] = entry
			}
		}
		return nil
	})
	return allSignatures
}

func (s *Store) processArticle(p string, allSignatures map[string]models.ManifestEntry, recognized map[string]bool) (*models.Article, []string, error) {
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, err
	}

	// FIX: Bulletproof absolute path resolution
	absP, _ := filepath.Abs(p)
	absData, _ := filepath.Abs(s.dataDir)
	rel, err := filepath.Rel(absData, absP)
	if err != nil {
		return nil, nil, err
	}

	relativePath := strings.TrimPrefix(filepath.ToSlash(rel), "/")
	cleanPath := strings.TrimSuffix(relativePath, ".md")
	parts := strings.Split(filepath.ToSlash(cleanPath), "/")

	hashBytes := sha256.Sum256(content)
	fileHash := hex.EncodeToString(hashBytes[:])
	res, err := render.Markdown(string(content), cleanPath)
	if err != nil {
		return nil, nil, err
	}

	isVerified := false
	signerKey := ""
	verifyErr := "File has no signature"
	verificationState := "unsigned"

	entry, exists := allSignatures[relativePath]
	if !exists {
		// Fall back to a content-hash match: a note still verifies after being
		// moved, since its on-disk path may differ from signer.db's record.
		entry, exists = allSignatures["sha256:"+fileHash]
	}
	if exists {
		// Surface the signer's key for transparency. There is no author identity
		// and no trust tier — every signer is simply a contributor.
		signerKey = entry.PublicKey
		entryHash := entry.FileHash
		if entryHash == "" {
			entryHash = entry.Hash
		}
		switch {
		case entryHash != fileHash:
			verifyErr = "Hash mismatch: file content was modified after signing"
			verificationState = "tampered"
		default:
			pubBytes, err1 := hex.DecodeString(entry.PublicKey)
			sigBytes, err2 := hex.DecodeString(entry.Signature)
			if err1 == nil && err2 == nil && len(pubBytes) == ed25519.PublicKeySize &&
				(ed25519.Verify(pubBytes, content, sigBytes) ||
					ed25519.Verify(pubBytes, []byte(fileHash), sigBytes) ||
					ed25519.Verify(pubBytes, hashBytes[:], sigBytes)) {
				if recognized[strings.ToLower(entry.PublicKey)] {
					isVerified = true
					verifyErr = ""
					verificationState = "verified"
				} else {
					verifyErr = "Signed by an unrecognized contributor key"
					verificationState = "unverified"
				}
			} else {
				verifyErr = "Unverified: signature or key is malformed or does not validate"
				verificationState = "unverified"
			}
		}
	}

	title := parts[len(parts)-1]
	title = strings.ReplaceAll(title, "-", " ")
	title = strings.Title(title)

	art := &models.Article{
		Path:              "/" + filepath.ToSlash(cleanPath),
		Title:             title,
		Body:              template.HTML(res.HTMLContent),
		Raw:               string(content),
		Hash:              fileHash,
		Verified:          isVerified,
		SignerKey:         signerKey,
		VerificationError: verifyErr,
		VerificationState: verificationState,
		Tags:              fetch.FrontmatterTags(content),
	}

	return art, parts, nil
}

// =====================================================================
// NAVIGATION TREE HELPERS & SEARCH
// =====================================================================

func (s *Store) BuildSearchIndex() []SearchItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]SearchItem, 0)

	var walk func(sec *models.Section, parentPath string)
	walk = func(sec *models.Section, parentPath string) {
		if sec.Name == "root" {
			for _, art := range sec.Articles {
				items = append(items, SearchItem{
					Title: "📄 " + art.Title,
					Path:  art.Path,
					Tags:  art.Tags,
				})
			}
			return
		}

		currentPath := parentPath + "/" + sec.Name

		title := strings.ReplaceAll(sec.Name, "-", " ")
		title = strings.Title(title)
		items = append(items, SearchItem{
			Title: "📁 " + title,
			Path:  currentPath,
		})

		for _, art := range sec.Articles {
			items = append(items, SearchItem{
				Title: "📄 " + art.Title,
				Path:  art.Path,
				Tags:  art.Tags,
			})
		}

		for _, sub := range sec.Sub {
			walk(sub, currentPath)
		}
	}

	keys := make([]string, 0, len(s.nav))
	for k := range s.nav {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		walk(s.nav[k], "")
	}

	return items
}

func (s *Store) GetSection(path string) *models.Section {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 {
		return nil
	}
	sec, ok := s.nav[parts[0]]
	if !ok {
		return nil
	}
	cur := sec
	for _, subName := range parts[1:] {
		if cur.Sub == nil {
			return nil
		}
		sub, ok := cur.Sub[subName]
		if !ok {
			return nil
		}
		cur = sub
	}
	return cur
}

func (s *Store) insertIntoMap(nav map[string]*models.Section, parts []string, art *models.Article) {
	if len(parts) == 0 {
		return
	}
	if len(parts) == 1 {
		if nav["root"] == nil {
			nav["root"] = &models.Section{Name: "root", Path: "/"}
		}
		nav["root"].Articles = append(nav["root"].Articles, art)
		return
	}

	secName := parts[0]
	if nav[secName] == nil {
		nav[secName] = &models.Section{
			Name: secName,
			Path: "/" + secName,
			Sub:  make(map[string]*models.Section),
		}
	}
	sec := nav[secName]

	if len(parts) == 2 {
		sec.Articles = append(sec.Articles, art)
		return
	}

	// Navigate (or create) sub-sections for parts[1..len-2]; article goes in the last one.
	cur := sec
	pathSoFar := "/" + secName
	for _, subName := range parts[1 : len(parts)-1] {
		pathSoFar += "/" + subName
		if cur.Sub == nil {
			cur.Sub = make(map[string]*models.Section)
		}
		if cur.Sub[subName] == nil {
			cur.Sub[subName] = &models.Section{
				Name: subName,
				Path: pathSoFar,
				Sub:  make(map[string]*models.Section),
			}
		}
		cur = cur.Sub[subName]
	}
	cur.Articles = append(cur.Articles, art)
}

func (s *Store) removeFromMap(nav map[string]*models.Section, parts []string) {
	if len(parts) == 0 {
		return
	}

	artPath := "/" + strings.Join(parts, "/")

	if len(parts) == 1 {
		if sec, ok := nav["root"]; ok {
			for i, a := range sec.Articles {
				if a.Path == artPath {
					sec.Articles = append(sec.Articles[:i], sec.Articles[i+1:]...)
					break
				}
			}
			if len(sec.Articles) == 0 && len(sec.Sub) == 0 {
				delete(nav, "root")
			}
		}
		return
	}

	topName := parts[0]
	sec, ok := nav[topName]
	if !ok {
		return
	}

	if len(parts) == 2 {
		for i, a := range sec.Articles {
			if a.Path == artPath {
				sec.Articles = append(sec.Articles[:i], sec.Articles[i+1:]...)
				break
			}
		}
		if len(sec.Articles) == 0 && len(sec.Sub) == 0 {
			delete(nav, topName)
		}
		return
	}

	// Navigate down to the containing sub-section.
	cur := sec
	for _, subName := range parts[1 : len(parts)-1] {
		if cur.Sub == nil {
			return
		}
		sub, ok := cur.Sub[subName]
		if !ok {
			return
		}
		cur = sub
	}

	for i, a := range cur.Articles {
		if a.Path == artPath {
			cur.Articles = append(cur.Articles[:i], cur.Articles[i+1:]...)
			break
		}
	}
	if len(sec.Articles) == 0 && len(sec.Sub) == 0 {
		delete(nav, topName)
	}
}

func (s *Store) Get(path string) *models.Article {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 {
		return nil
	}

	if len(parts) == 1 {
		if sec, ok := s.nav["root"]; ok {
			for _, a := range sec.Articles {
				if a.Path == "/"+path {
					return a
				}
			}
		}
		return nil
	}

	sec, ok := s.nav[parts[0]]
	if !ok {
		return nil
	}

	if len(parts) == 2 {
		for _, a := range sec.Articles {
			if a.Path == "/"+path {
				return a
			}
		}
		return nil
	}

	// Navigate down sub-sections for parts[1..len-2].
	cur := sec
	for _, subName := range parts[1 : len(parts)-1] {
		if cur.Sub == nil {
			return nil
		}
		sub, ok := cur.Sub[subName]
		if !ok {
			return nil
		}
		cur = sub
	}

	for _, a := range cur.Articles {
		if a.Path == "/"+path {
			return a
		}
	}
	return nil
}

func (s *Store) Root() map[string]*models.Section {
	s.mu.RLock()
	defer s.mu.RUnlock()

	copy := make(map[string]*models.Section, len(s.nav))
	for k, v := range s.nav {
		copy[k] = v
	}
	return copy
}
