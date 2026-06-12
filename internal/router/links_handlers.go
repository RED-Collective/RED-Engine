package router

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/RED-Collective/red-engine/internal/navigation"
)

// backlinksAPI serves GET /api/backlinks?path=<clean note path, no .md> — the
// notes whose wikilinks point at the given note.
//
//	GET /api/backlinks?path=physics/mechanics/intro → [{file_path,title,kind}]
func (h *handler) backlinksAPI(w http.ResponseWriter, r *http.Request) {
	if h.navService == nil {
		http.Error(w, "navigation service unavailable", http.StatusServiceUnavailable)
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		http.Error(w, "path parameter required", http.StatusBadRequest)
		return
	}
	refs, err := h.navService.Backlinks(path)
	if err != nil {
		http.Error(w, "backlinks failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if refs == nil {
		refs = []navigation.BacklinkRef{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(refs)
}

// graphAPI serves GET /api/graph — the full note link graph: every indexed note
// plus every resolved wikilink edge, for graph-view clients.
//
//	GET /api/graph → {nodes:[{id,file_path,title,vault}], edges:[{source_id,target_id,kind}]}
func (h *handler) graphAPI(w http.ResponseWriter, r *http.Request) {
	if h.navService == nil {
		http.Error(w, "navigation service unavailable", http.StatusServiceUnavailable)
		return
	}
	g, err := h.navService.GraphDump()
	if err != nil {
		http.Error(w, "graph failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(g)
}

// brokenLinksAPI serves GET /-/admin/links/broken — wikilink targets that
// resolve to no local note, grouped with counts and referencing notes. Admin
// only: missing-target reports reveal vault gaps, so they are not public.
func (h *handler) brokenLinksAPI(w http.ResponseWriter, r *http.Request) {
	if h.navService == nil {
		http.Error(w, "navigation service unavailable", http.StatusServiceUnavailable)
		return
	}
	broken, err := h.navService.BrokenLinks()
	if err != nil {
		http.Error(w, "broken links failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if broken == nil {
		broken = []navigation.BrokenLink{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(broken)
}
