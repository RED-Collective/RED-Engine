package store

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	"github.com/RED-Collective/red-engine/internal/models"
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
	Title string `json:"title"`
	Path  string `json:"path"`
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

	trustedKeys, allSignatures := s.loadSecurityData()
	newNav := make(map[string]*models.Section)

	err := filepath.WalkDir(s.dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		art, parts, err := s.processArticle(path, trustedKeys, allSignatures)
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
	s.nav = newNav
	return nil
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

		trustedKeys, allSignatures := s.loadSecurityData()

		art, _, err := s.processArticle(p, trustedKeys, allSignatures)
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

func (s *Store) loadSecurityData() (map[string]string, map[string]models.ManifestEntry) {
	trustedKeys := make(map[string]string)

	// FIX: Resolve the trusted keys relative to the data directory's parent (standard repository root)
	trustPath := filepath.Join(filepath.Dir(s.dataDir), "contributors.json")
	if _, err := os.Stat(trustPath); os.IsNotExist(err) {
		trustPath = "contributors.json" // Fallback to process CWD
	}

	if trustData, err := os.ReadFile(trustPath); err == nil {
		var contributors []models.Contributor
		if err := json.Unmarshal(trustData, &contributors); err == nil {
			for _, c := range contributors {
				trustedKeys[strings.ToLower(c.PublicKey)] = c.Name
			}
		}
	}

	allSignatures := make(map[string]models.ManifestEntry)
	filepath.WalkDir(s.dataDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(path) == "manifest.json" {
			if manifestData, err := os.ReadFile(path); err == nil {
				manifest := parseManifestJSON(manifestData)
				relManifestDir, _ := filepath.Rel(s.dataDir, filepath.Dir(path))
				relManifestDir = filepath.ToSlash(relManifestDir)
				for key, entry := range manifest {
					fullKey := filepath.ToSlash(key)
					if relManifestDir != "." && !strings.HasPrefix(fullKey, relManifestDir+"/") && fullKey != relManifestDir {
						fullKey = filepath.ToSlash(filepath.Join(relManifestDir, fullKey))
					}
					allSignatures[fullKey] = entry
				}
			}
		}
		return nil
	})
	return trustedKeys, allSignatures
}

func (s *Store) processArticle(p string, trustedKeys map[string]string, allSignatures map[string]models.ManifestEntry) (*models.Article, []string, error) {
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
	res, err := render.Markdown(string(content))
	if err != nil {
		return nil, nil, err
	}

	isVerified := false
	authorName := "Unverified / Unknown Origin"
	verifyErr := "File signature not found in manifest"

	if entry, exists := allSignatures[relativePath]; exists {
		entryHash := entry.FileHash
		if entryHash == "" {
			entryHash = entry.Hash
		}
		if entryHash == fileHash {
			if trustedAuthor, isTrusted := trustedKeys[strings.ToLower(entry.PublicKey)]; isTrusted {
				pubBytes, err1 := hex.DecodeString(entry.PublicKey)
				sigBytes, err2 := hex.DecodeString(entry.Signature)
				if err1 == nil && err2 == nil && len(pubBytes) == ed25519.PublicKeySize {
					if ed25519.Verify(pubBytes, content, sigBytes) || ed25519.Verify(pubBytes, []byte(fileHash), sigBytes) || ed25519.Verify(pubBytes, hashBytes[:], sigBytes) {
						isVerified = true
						authorName = trustedAuthor
						verifyErr = ""
					} else {
						verifyErr = "Invalid Signature: Cryptographic verification failed"
					}
				} else {
					verifyErr = "Malformed Signature or Public Key data"
				}
			} else {
				verifyErr = "Untrusted Key: The public key is not mapped in contributors.json"
			}
		} else {
			verifyErr = "Hash Mismatch: File content was modified after signing"
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
		Author:            authorName,
		VerificationError: verifyErr,
	}

	return art, parts, nil
}

func parseManifestJSON(data []byte) map[string]models.ManifestEntry {
	result := make(map[string]models.ManifestEntry)
	var wrapped models.Manifest
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Files) > 0 {
		return wrapped.Files
	}
	if err := json.Unmarshal(data, &result); err == nil {
		return result
	}
	return result
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

	if len(parts) == 1 {
		return s.nav[parts[0]]
	} else if len(parts) == 2 {
		if sec, ok := s.nav[parts[0]]; ok {
			return sec.Sub[parts[1]]
		}
	}
	return nil
}

func (s *Store) insertIntoMap(nav map[string]*models.Section, parts []string, art *models.Article) {
	if len(parts) == 1 {
		if nav["root"] == nil {
			nav["root"] = &models.Section{Name: "root"}
		}
		nav["root"].Articles = append(nav["root"].Articles, art)
	} else {
		secName := parts[0]
		if nav[secName] == nil {
			nav[secName] = &models.Section{Name: secName, Sub: make(map[string]*models.Section)}
		}
		sec := nav[secName]
		if len(parts) == 2 {
			sec.Articles = append(sec.Articles, art)
		} else {
			subName := parts[1]
			if sec.Sub[subName] == nil {
				sec.Sub[subName] = &models.Section{Name: subName}
			}
			sec.Sub[subName].Articles = append(sec.Sub[subName].Articles, art)
		}
	}
}

func (s *Store) removeFromMap(nav map[string]*models.Section, parts []string) {
	if len(parts) == 1 {
		if sec, ok := nav["root"]; ok {
			for i, a := range sec.Articles {
				if a.Path == "/"+parts[0] {
					sec.Articles = append(sec.Articles[:i], sec.Articles[i+1:]...)
					break
				}
			}
			if len(sec.Articles) == 0 && len(sec.Sub) == 0 {
				delete(nav, "root")
			}
		}
	} else if len(parts) == 2 {
		if sec, ok := nav[parts[0]]; ok {
			for i, a := range sec.Articles {
				if a.Path == "/"+parts[0]+"/"+parts[1] {
					sec.Articles = append(sec.Articles[:i], sec.Articles[i+1:]...)
					break
				}
			}
			if len(sec.Articles) == 0 && len(sec.Sub) == 0 {
				delete(nav, parts[0])
			}
		}
	} else if len(parts) == 3 {
		if sec, ok := nav[parts[0]]; ok {
			if sub, ok := sec.Sub[parts[1]]; ok {
				for i, a := range sub.Articles {
					if a.Path == "/"+parts[0]+"/"+parts[1]+"/"+parts[2] {
						sub.Articles = append(sub.Articles[:i], sub.Articles[i+1:]...)
						break
					}
				}
				if len(sub.Articles) == 0 && len(sub.Sub) == 0 {
					delete(sec.Sub, parts[1])
				}
			}
			if len(sec.Articles) == 0 && len(sec.Sub) == 0 {
				delete(nav, parts[0])
			}
		}
	}
}

func (s *Store) Get(path string) *models.Article {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")

	if len(parts) == 1 {
		if sec, ok := s.nav["root"]; ok {
			for _, a := range sec.Articles {
				if a.Path == "/"+path {
					return a
				}
			}
		}
	} else if len(parts) == 2 {
		if sec, ok := s.nav[parts[0]]; ok {
			for _, a := range sec.Articles {
				if a.Path == "/"+path {
					return a
				}
			}
		}
	} else if len(parts) == 3 {
		if sec, ok := s.nav[parts[0]]; ok {
			if sub, ok := sec.Sub[parts[1]]; ok {
				for _, a := range sub.Articles {
					if a.Path == "/"+path {
						return a
					}
				}
			}
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
