package router

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RED-Collective/red-engine/internal/models"
	"github.com/RED-Collective/red-engine/internal/navigation"
	"github.com/RED-Collective/red-engine/internal/registry"
)

func (h *handler) siteName() string {
	if s := registry.GetSetting("site_name"); s != "" {
		return s
	}
	return "RED Engine"
}

func (h *handler) nodeName() string {
	if n := registry.GetSetting("node_name"); n != "" {
		return n
	}
	name, _ := os.Hostname()
	return name
}

func capitalize(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

func (h *handler) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Block any path segment starting with "." (covers .meta, .assets, .git, etc.)
	if path != "/" {
		for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
			if strings.HasPrefix(seg, ".") {
				http.NotFound(w, r)
				return
			}
		}
	}

	// Calculate depth (number of URL path segments).
	var parts []string
	if path == "/" {
		parts = []string{}
	} else {
		parts = strings.Split(strings.Trim(path, "/"), "/")
	}
	depth := len(parts)

	switchDepth := h.cfg.TemplateSwitchDepth
	if switchDepth <= 0 {
		switchDepth = 2
	}

	siteName := h.siteName()
	d := models.PageData{
		Site:     siteName,
		NodeName: h.nodeName(),
		Nav:      h.store.Root(),
		Path:     path,
		Depth:    depth,
		DevMode:  h.devMode,
	}
	if len(parts) > 0 {
		d.TopCat = parts[0]
	}

	// ── Article lookup (takes priority over hub pages) ─────────────────
	if path != "/" {
		art := h.store.Get(path)
		if art == nil && strings.HasSuffix(path, ".md") {
			art = h.store.Get(strings.TrimSuffix(path, ".md"))
		}
		if art != nil {
			d.Title = art.Title
			d.Body = art.Body
			d.Verified = art.Verified
			d.SignerKey = art.SignerKey
			d.Hash = art.Hash
			d.VerificationError = art.VerificationError
			d.VerificationState = art.VerificationState
			d.Crumb = buildCrumbs(parts)
			d.PrevArticle, d.NextArticle = h.findSiblings(path, parts)

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := h.articleTmpl.ExecuteTemplate(w, "article.html", d); err != nil {
				http.Error(w, "template error: "+err.Error(), 500)
			}
			return
		}
	}

	// ── Hub page ────────────────────────────────────────────────────────
	var sec *models.Section
	if path == "/" {
		// Synthetic root section — all top-level sections become cards.
		allSubs := h.store.Root()
		filtered := make(map[string]*models.Section, len(allSubs))
		for k, v := range allSubs {
			if k != "root" {
				filtered[k] = v
			}
		}
		sec = &models.Section{
			Name: siteName,
			Path: "/",
			Sub:  filtered,
		}
	} else {
		sec = h.store.GetSection(path)
	}

	if sec == nil {
		// Last chance: serve non-markdown static files sitting openly in data/.
		http.NotFound(w, r)
		return
	}

	d.Section = sec
	if len(parts) > 0 {
		d.Title = capitalize(parts[len(parts)-1])
		d.Crumb = buildCrumbs(parts)
	} else {
		d.Title = siteName
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if depth < switchDepth {
		if err := h.mainTmpl.ExecuteTemplate(w, "main.html", d); err != nil {
			http.Error(w, "template error: "+err.Error(), 500)
		}
	} else {
		if err := h.sub1Tmpl.ExecuteTemplate(w, "sub1.html", d); err != nil {
			http.Error(w, "template error: "+err.Error(), 500)
		}
	}
}

// findSiblings returns the previous and next articles in the same section as path.
func (h *handler) findSiblings(path string, parts []string) (*models.Article, *models.Article) {
	if len(parts) < 2 {
		return nil, nil
	}
	parentPath := "/" + strings.Join(parts[:len(parts)-1], "/")
	sec := h.store.GetSection(parentPath)
	if sec == nil {
		return nil, nil
	}
	for i, a := range sec.Articles {
		if a.Path == path {
			var prev, next *models.Article
			if i > 0 {
				prev = sec.Articles[i-1]
			}
			if i < len(sec.Articles)-1 {
				next = sec.Articles[i+1]
			}
			return prev, next
		}
	}
	return nil, nil
}

func buildCrumbs(parts []string) []models.Crumb {
	crumbs := make([]models.Crumb, 0, len(parts))
	path := ""
	for _, p := range parts {
		path += "/" + p
		crumbs = append(crumbs, models.Crumb{Label: capitalize(p), Path: path})
	}
	return crumbs
}

// sectionHTML is kept as a fallback for any legacy call sites; unused by main routing.
func sectionHTML(sec *models.Section) string {
	var b strings.Builder
	b.WriteString(`<div class="section-index"><h1>`)
	b.WriteString(capitalize(sec.Name))
	b.WriteString(`</h1><ul>`)
	for _, a := range sec.Articles {
		b.WriteString(`<li><a href="`)
		b.WriteString(a.Path)
		b.WriteString(`">`)
		b.WriteString(a.Title)
		b.WriteString(`</a></li>`)
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

// Ensure sectionHTML is referenced to avoid compile errors.
var _ = sectionHTML

// contentAPI serves article data as JSON for the React SPA.
// GET /api/content?path=<url-path>
func (h *handler) contentAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	path := r.URL.Query().Get("path")
	if path == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"missing path"}`))
		return
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Block hidden segments (.assets, .meta, etc.)
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if strings.HasPrefix(seg, ".") {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"not found"}`))
			return
		}
	}

	isDirectory := false

	// Step 1: direct leaf article lookup.
	art := h.store.Get(path)
	if art == nil && strings.HasSuffix(path, ".md") {
		art = h.store.Get(strings.TrimSuffix(path, ".md"))
	}

	// Step 2: directory default – RED_KNOWLEDGE fallback.
	if art == nil {
		art = h.store.Get(path + "/RED_KNOWLEDGE")
		if art != nil {
			isDirectory = true
		}
	}

	if art == nil {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
		return
	}

	// Rewrite relative asset src to absolute /-/assets/ URLs.
	artPathParts := strings.Split(strings.TrimPrefix(art.Path, "/"), "/")
	dirPath := strings.Join(artPathParts[:len(artPathParts)-1], "/")
	bodyStr := string(art.Body)
	if dirPath != "" {
		bodyStr = strings.ReplaceAll(bodyStr, `src=".assets/`, `src="/-/assets/`+dirPath+`/`)
	}

	// Crumbs use the requested path (not the internal RED_KNOWLEDGE path).
	var pathParts []string
	if path != "/" {
		pathParts = strings.Split(strings.Trim(path, "/"), "/")
	}
	crumbs := buildCrumbs(pathParts)

	prev, next := h.findSiblings(art.Path, artPathParts)

	type articleRef struct {
		Title string `json:"title"`
		Path  string `json:"path"`
	}
	type crumbJSON struct {
		Label string `json:"label"`
		Path  string `json:"path"`
	}
	type response struct {
		Title             string                   `json:"title"`
		BodyHTML          string                   `json:"body_html"`
		VerificationState string                   `json:"verification_state"`
		VerificationError string                   `json:"verification_error,omitempty"`
		SignerKey         string                   `json:"signer_key,omitempty"`
		Author            string                   `json:"author,omitempty"`    // self-asserted signer display name (red_author_name)
		SignedAt          string                   `json:"signed_at,omitempty"` // human-readable signature date (red_signed_at)
		Hash              string                   `json:"hash"`
		Tags              []string                 `json:"tags,omitempty"`
		Crumb             []crumbJSON              `json:"crumb"`
		PrevArticle       *articleRef              `json:"prev_article"`
		NextArticle       *articleRef              `json:"next_article"`
		IsDirectory       bool                     `json:"is_directory"`
		Backlinks         []navigation.BacklinkRef `json:"backlinks"` // always present (possibly empty) so clients can rely on the key
	}

	// For directory defaults (RED_KNOWLEDGE), derive title from the directory name
	// since the file itself may lack a heading, making art.Title just "RED_KNOWLEDGE".
	title := art.Title
	if isDirectory && (title == "RED_KNOWLEDGE" || title == "") {
		title = capitalize(pathParts[len(pathParts)-1])
	}

	resp := response{
		Title:             title,
		BodyHTML:          bodyStr,
		VerificationState: art.VerificationState,
		VerificationError: art.VerificationError,
		SignerKey:         art.SignerKey,
		Author:            art.SignerName,
		SignedAt:          art.SignedAt,
		Hash:              art.Hash,
		Tags:              art.Tags,
		IsDirectory:       isDirectory,
		Crumb:             make([]crumbJSON, 0, len(crumbs)),
		Backlinks:         []navigation.BacklinkRef{},
	}
	// Backlinks use art.Path (not the request path) so the RED_KNOWLEDGE
	// directory fallback gets the backlinks of the note actually served. A
	// backlink failure never fails the note response.
	if h.navService != nil {
		if bl, err := h.navService.Backlinks(strings.TrimPrefix(art.Path, "/")); err != nil {
			log.Printf("contentAPI: backlinks for %s: %v", art.Path, err)
		} else if bl != nil {
			resp.Backlinks = bl
		}
	}
	for _, c := range crumbs {
		resp.Crumb = append(resp.Crumb, crumbJSON{Label: c.Label, Path: c.Path})
	}
	if prev != nil {
		resp.PrevArticle = &articleRef{Title: prev.Title, Path: prev.Path}
	}
	if next != nil {
		resp.NextArticle = &articleRef{Title: next.Title, Path: next.Path}
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("contentAPI: encode error: %v", err)
	}
}
