package router

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"
)

// The engine is frontend-agnostic: it exposes a JSON API (see apiIndex) and
// serves a compiled UI as plain static files. The active static source (h.webFS)
// is, in priority order: the filesystem RED_WEB_DIR, then the binary's embedded
// static/dist, then nil — in which case the legacy Go templates render instead.

// frontend serves the built UI at "/". It is registered as the catch-all, so it
// only receives paths not claimed by a more specific route (/api/..., /-/...,
// /content/...). Behaviour is SPA/MPA agnostic:
//
//   - an existing file (or a directory containing index.html) is served verbatim;
//   - an extension-less path that matches nothing falls back to index.html, so a
//     single-page app's client-side routes resolve;
//   - a missing path WITH a file extension returns 404, so a multi-page build's
//     genuine asset misses are real errors, not silently masked by index.html.
//
// With no static source configured it defers to the legacy template renderer.
func (h *handler) frontend(w http.ResponseWriter, r *http.Request) {
	if h.webFS == nil {
		h.serve(w, r)
		return
	}

	name := path.Clean(r.URL.Path)
	if name == "/" {
		h.appShell(w, r)
		return
	}

	// Does the path resolve to a real file, or a directory with an index.html?
	served := false
	if f, err := h.webFS.Open(name); err == nil {
		if fi, _ := f.Stat(); fi != nil {
			if !fi.IsDir() {
				served = true
			} else if idx, e := h.webFS.Open(path.Join(name, "index.html")); e == nil {
				idx.Close()
				served = true
			}
		}
		f.Close()
	}
	if served {
		http.FileServer(h.webFS).ServeHTTP(w, r)
		return
	}

	// Miss. Extension-less → SPA history fallback to index.html; otherwise 404.
	if path.Ext(name) == "" {
		h.appShell(w, r)
		return
	}
	http.NotFound(w, r)
}

// appShell writes the UI's index.html so a client-side router can take over. It
// backs the catch-all fallback and the explicit client routes (/-/nodes,
// /-/admin). With no static source it defers to the legacy template renderer.
func (h *handler) appShell(w http.ResponseWriter, r *http.Request) {
	if h.webFS != nil {
		if f, err := h.webFS.Open("/index.html"); err == nil {
			defer f.Close()
			if fi, e := f.Stat(); e == nil && !fi.IsDir() {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				http.ServeContent(w, r, "index.html", fi.ModTime(), f)
				return
			}
		}
	}
	h.serve(w, r)
}

// corsMiddleware adds CORS headers when origins is non-empty, so a frontend
// served from a different origin (e.g. a Vite dev server) can call the API.
// origins is a comma-separated allow-list, or "*" for any origin. Auth uses the
// X-Admin-Token header (not cookies), so a wildcard origin is safe.
func corsMiddleware(origins string, next http.Handler) http.Handler {
	origins = strings.TrimSpace(origins)
	if origins == "" {
		return next
	}
	allowAll := origins == "*"
	allowed := map[string]bool{}
	if !allowAll {
		for _, o := range strings.Split(origins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allowed[o] = true
			}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowAll || allowed[origin]) {
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Admin-Token")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiIndex is a self-describing catalog of the HTTP surface, so a frontend
// developer can discover the API from one place: GET /api.
func (h *handler) apiIndex(w http.ResponseWriter, r *http.Request) {
	type ep struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Desc   string `json:"desc"`
	}
	catalog := struct {
		Service string `json:"service"`
		Public  []ep   `json:"public"`
		// Admin endpoints require the X-Admin-Token header.
		Admin []ep `json:"admin"`
	}{
		Service: "red-engine",
		Public: []ep{
			{"GET", "/api", "This catalog."},
			{"GET", "/api/navigation", "Top-level nav nodes (flat). ?path=<p> subtree; &flat=1 flat list; ?content_type=<v> filter."},
			{"GET", "/api/tags", "All tags as [{name,count}]. ?tag=<t> lists the notes carrying that tag."},
			{"GET", "/api/content", "Article or directory at ?path=<p>: rendered body_html, verification, crumbs, prev/next, tags."},
			{"GET", "/api/recent-files", "Most recently modified articles. ?limit=N (default 5, max 20)."},
			{"GET", "/-/search-index.json", "Flat search index: [{title,path,tags}]."},
			{"GET", "/-/nodeinfo", "This node's identity, name, description, public key, exported paths."},
			{"GET", "/-/peers", "Known federation peers."},
			{"GET", "/-/health", "Liveness probe (plain OK)."},
			{"GET", "/-/assets/{dir}/{file}", "Inline image assets referenced by an article body."},
			{"GET", "/-/branch-meta/{path}/cover.jpg|icon.svg", "Optional per-folder cover/icon artwork."},
			{"GET", "/content/{path}", "Raw file bytes from the data directory."},
		},
		Admin: []ep{
			{"GET", "/-/admin/peers", "List peers."},
			{"POST", "/-/admin/peers/add", "Add a peer."},
			{"POST", "/-/admin/peers/delete", "Remove a peer."},
			{"POST", "/-/admin/peers/refresh", "Refresh a peer."},
			{"GET", "/-/admin/peers/health", "Peer health check."},
			{"GET", "/-/admin/contributors", "List trusted contributor keys."},
			{"POST", "/-/admin/contributors/add", "Trust a contributor key."},
			{"POST", "/-/admin/contributors/delete", "Revoke a contributor key."},
			{"GET/POST", "/-/admin/config", "Read/update node settings (name, description, URL)."},
			{"POST", "/-/import", "Import/sync a remote source."},
			{"POST", "/-/admin/remove", "Remove synced content."},
			{"POST", "/-/reload", "Rebuild the content store from disk."},
			{"POST", "/-/admin/navigation/rescan", "Rebuild the navigation index."},
			{"PUT", "/-/admin/navigation/folder/description", "Override a folder description. ?folder_id=<id>."},
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(catalog)
}
