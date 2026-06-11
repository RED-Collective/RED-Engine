package store

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io/fs"
	"log"
	"net/url"
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
	// afterReindex, when set, is run (outside the store lock) after every
	// successful Reload or UpdateFiles. The router registers the navigation
	// service's rescan here so the SQLite-backed folder tree / guide counts stay
	// in lockstep with the in-memory nav — see SetReindexHook.
	afterReindex atomic.Pointer[func()]

	// noteIndex/assetIndex resolve Obsidian [[wikilinks]] and ![[embeds]] by
	// basename (how Obsidian itself resolves, since there is no .obsidian metadata
	// to consult after a vault is pushed to git). noteIndex maps a lowercased note
	// basename WITHOUT ".md" → its article clean-path; assetIndex maps a lowercased
	// image basename WITH extension → its data-relative slash path. Both are rebuilt
	// by reloadLocked and incrementally maintained by updateFilesLocked, and are only
	// read/written while holding s.mu (during processArticle). See buildLinkIndex and
	// linkResolver.
	noteIndex  map[string]string
	assetIndex map[string]string
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

// SetReindexHook registers a callback invoked after every successful Reload or
// UpdateFiles, OUTSIDE the store lock. The navigation service registers its
// ScanDataDirectories here so the SQLite-backed folder tree and guide counts
// (served at /api/navigation) are rebuilt whenever content changes. Without it a
// synced or edited note updates the in-memory nav — which drives article pages,
// "recently added", and search — but leaves the folder cards/counts stale until a
// restart or manual rescan. Safe to call concurrently with reloads; pass nil to
// clear.
func (s *Store) SetReindexHook(fn func()) {
	if fn == nil {
		s.afterReindex.Store(nil)
		return
	}
	s.afterReindex.Store(&fn)
}

// runReindexHook invokes the registered post-reindex callback, if any. Callers
// MUST invoke it after releasing s.mu so the hook (a full filesystem rescan + DB
// transaction) never runs while holding the store lock.
func (s *Store) runReindexHook() {
	if p := s.afterReindex.Load(); p != nil && *p != nil {
		(*p)()
	}
}

// Reload rebuilds the in-memory nav from a full filesystem walk, then fires the
// reindex hook so any secondary index is rebuilt to match.
func (s *Store) Reload() error {
	if err := s.reloadLocked(); err != nil {
		return err
	}
	s.runReindexHook()
	return nil
}

func (s *Store) reloadLocked() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	recognized := registry.RecognizedContributorKeys()
	newNav := make(map[string]*models.Section)

	// Build the wikilink/embed resolution index from a cheap path-only pass BEFORE
	// rendering, so every article resolves links against the full vault.
	s.noteIndex, s.assetIndex = s.buildLinkIndex()

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

		art, parts, err := s.processArticle(path, recognized)
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

// UpdateFiles hot-patches the in-memory nav for the given paths, then fires the
// reindex hook so any secondary index reflects the change too.
func (s *Store) UpdateFiles(changedPaths []string) error {
	s.updateFilesLocked(changedPaths)
	s.runReindexHook()
	return nil
}

func (s *Store) updateFilesLocked(changedPaths []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range changedPaths {
		p = filepath.Clean(p)

		// Keep the link/embed index current for notes AND image assets, so a synced or
		// dropped image is resolvable by the next note render even though only notes
		// become nav articles below.
		s.updateLinkIndexEntry(p)

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

		recognized := registry.RecognizedContributorKeys()

		art, _, err := s.processArticle(p, recognized)
		if err == nil && art != nil {
			s.insertIntoMap(s.nav, parts, art)
		} else if err != nil {
			log.Printf("⚠️ Failed to hot-patch article %s: %v", p, err)
		}
	}
}

// RevalidateTrust re-evaluates the trust of every already-loaded article against a
// fresh contributor keyring WITHOUT re-reading or re-rendering any file. It only
// touches articles that carry a cryptographically valid signature (state
// "verified"/"unverified"), flipping them between those two as the keyring changes,
// and leaves "tampered"/"unsigned" articles untouched. Call it after the keyring is
// modified (a contributor added or revoked) so notes re-verify without a restart.
func (s *Store) RevalidateTrust() {
	s.mu.Lock()
	defer s.mu.Unlock()

	recognized := registry.RecognizedContributorKeys()

	var walk func(sec *models.Section)
	walk = func(sec *models.Section) {
		if sec == nil {
			return
		}
		for _, art := range sec.Articles {
			switch art.VerificationState {
			case "verified", "unverified":
				if recognized[strings.ToLower(art.SignerKey)] {
					art.Verified = true
					art.VerificationState = "verified"
					art.VerificationError = ""
				} else {
					art.Verified = false
					art.VerificationState = "unverified"
					art.VerificationError = "Signed by an unrecognized contributor key"
				}
			}
		}
		for _, sub := range sec.Sub {
			walk(sub)
		}
	}

	for _, sec := range s.nav {
		walk(sec)
	}
}

// =====================================================================
// DATA PROCESSING HELPERS
// =====================================================================

// buildLinkIndex walks the data dir (paths only — no reads) and returns the
// basename→path maps used to resolve [[wikilinks]] and ![[embeds]]. Notes are keyed
// by basename without ".md"; image assets by basename with extension. On a duplicate
// basename the first occurrence wins (and a warning is logged), since without
// per-note metadata the engine cannot replicate Obsidian's shortest-path tie-break.
// Callers hold s.mu.
func (s *Store) buildLinkIndex() (notes, assets map[string]string) {
	notes = make(map[string]string)
	assets = make(map[string]string)

	filepath.WalkDir(s.dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != s.dataDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(s.dataDir, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		name := d.Name()

		if strings.EqualFold(filepath.Ext(name), ".md") {
			base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
			clean := strings.TrimSuffix(relSlash, ".md")
			if existing, ok := notes[base]; ok {
				log.Printf("⚠️ wikilink index: duplicate note basename %q (%s vs %s); keeping first", base, existing, clean)
			} else {
				notes[base] = clean
			}
		} else if fetch.IsSyncableAsset(name) {
			base := strings.ToLower(name)
			if existing, ok := assets[base]; ok {
				log.Printf("⚠️ embed index: duplicate asset basename %q (%s vs %s); keeping first", base, existing, relSlash)
			} else {
				assets[base] = relSlash
			}
		}
		return nil
	})
	return notes, assets
}

// updateLinkIndexEntry keeps the wikilink/embed index current for a single changed
// path (note or image), adding it when present and removing it when deleted, so
// embeds/links resolve on the next render without a full reload. Callers hold s.mu.
func (s *Store) updateLinkIndexEntry(p string) {
	absP, _ := filepath.Abs(p)
	absData, _ := filepath.Abs(s.dataDir)
	rel, err := filepath.Rel(absData, absP)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	relSlash := filepath.ToSlash(rel)
	name := filepath.Base(p)
	_, statErr := os.Stat(p)
	exists := statErr == nil

	if s.noteIndex == nil {
		s.noteIndex = make(map[string]string)
	}
	if s.assetIndex == nil {
		s.assetIndex = make(map[string]string)
	}

	if strings.EqualFold(filepath.Ext(name), ".md") {
		base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		clean := strings.TrimSuffix(relSlash, ".md")
		if exists {
			s.noteIndex[base] = clean
		} else if s.noteIndex[base] == clean {
			delete(s.noteIndex, base) // only drop the mapping this exact file owned
		}
	} else if fetch.IsSyncableAsset(name) {
		base := strings.ToLower(name)
		if exists {
			s.assetIndex[base] = relSlash
		} else if s.assetIndex[base] == relSlash {
			delete(s.assetIndex, base)
		}
	}
}

// linkResolver returns a render.LinkResolver closed over the current index. It looks
// targets up by basename (mirroring Obsidian), serving images from the public
// /content/ route and notes from their article path.
func (s *Store) linkResolver() render.LinkResolver {
	return func(target string, embed bool) (string, bool) {
		base := strings.ToLower(filepath.Base(strings.TrimSpace(target)))
		if base == "" {
			return "", false
		}
		if embed {
			if rel, ok := s.assetIndex[base]; ok {
				return contentURL(rel), true
			}
			return "", false
		}
		base = strings.TrimSuffix(base, ".md") // tolerate an explicit .md in a link
		if clean, ok := s.noteIndex[base]; ok {
			return articleURL(clean), true
		}
		return "", false
	}
}

// contentURL builds the public URL for a synced asset at the given data-relative
// slash path, percent-escaping spaces and other path characters.
// toTitle capitalises the first letter of s. Replaces the deprecated
// strings.Title which mishandles Unicode (not a concern for our ASCII filenames,
// but the deprecation lint warning is noisy and misleading).
func toTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func contentURL(rel string) string {
	u := url.URL{Path: "/content/" + rel}
	return u.String()
}

// articleURL builds the in-app URL for a note at the given clean (no ".md") path.
func articleURL(clean string) string {
	u := url.URL{Path: "/" + clean}
	return u.String()
}

func (s *Store) processArticle(p string, recognized map[string]bool) (*models.Article, []string, error) {
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

	// Hash the FULL file (header + body) for the content hash, but render only the
	// body — goldmark has no frontmatter extension, so passing the raw `---` block
	// would leak `<hr>` + `key: value` lines onto the page. The readable signature
	// date is re-surfaced as proper UI via Article.SignedAt below.
	hashBytes := sha256.Sum256(content)
	fileHash := hex.EncodeToString(hashBytes[:])
	// Resolve Obsidian [[wikilinks]]/![[embeds]] into standard Markdown before render —
	// goldmark treats them as literal text otherwise. The hash above is over the raw
	// file, so this rewrite never affects signature verification.
	body := render.ResolveObsidianLinks(string(fetch.FrontmatterBody(content)), s.linkResolver())
	res, err := render.Markdown(body, cleanPath)
	if err != nil {
		return nil, nil, err
	}

	// Verify straight from the note's frontmatter — the signature, signer key, name
	// and body hash all travel in the header, so there is no signer.db to consult.
	v := fetch.VerifyNote(content)
	isVerified := false
	signerKey := v.SignerKey
	signerName := v.SignerName
	verifyErr := v.Err
	verificationState := v.State
	if v.State == "signed" {
		// A valid signature; trust depends on whether the key is in the keyring.
		if recognized[strings.ToLower(v.SignerKey)] {
			isVerified = true
			verifyErr = ""
			verificationState = "verified"
		} else {
			verifyErr = "Signed by an unrecognized contributor key"
			verificationState = "unverified"
		}
	}

	title := parts[len(parts)-1]
	title = strings.ReplaceAll(title, "-", " ")
	title = toTitle(title)

	art := &models.Article{
		Path:              "/" + filepath.ToSlash(cleanPath),
		Title:             title,
		Body:              template.HTML(res.HTMLContent),
		Raw:               string(content),
		Hash:              fileHash,
		Verified:          isVerified,
		SignerKey:         signerKey,
		SignerName:        signerName,
		VerificationError: verifyErr,
		VerificationState: verificationState,
		SignedAt:          fetch.FrontmatterValue(content, "red_signed_at"),
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
		title = toTitle(title)
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
