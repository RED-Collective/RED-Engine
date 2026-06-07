package router

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/RED-Collective/red-engine/internal/navigation"
)

// navAPI serves the navigation tree or a flat list.
//
//	GET /api/navigation                       → all top-level vaults (flat)
//	GET /api/navigation?path=physics          → subtree rooted at "physics"
//	GET /api/navigation?path=physics&flat=1   → flat list under "physics"
//	GET /api/navigation?content_type=physics  → flat list filtered by vault
func (h *handler) navAPI(w http.ResponseWriter, r *http.Request) {
	if h.navService == nil {
		http.Error(w, "navigation service unavailable", http.StatusServiceUnavailable)
		return
	}

	path := r.URL.Query().Get("path")
	contentType := r.URL.Query().Get("content_type")
	flat := r.URL.Query().Get("flat") != "" || contentType != "" || path == ""

	w.Header().Set("Content-Type", "application/json")

	if !flat {
		tree, err := h.navService.GetNavigationTree(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(tree)
		return
	}

	nodes, err := h.navService.GetNavigationFlat(path, contentType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if nodes == nil {
		nodes = []navigation.NavNode{}
	}
	json.NewEncoder(w).Encode(nodes)
}

// tagsAPI serves the tag index.
//
//	GET /api/tags          → [{name,count}] for every tag, by descending count
//	GET /api/tags?tag=<t>   → the notes (guide nodes) carrying tag <t>
func (h *handler) tagsAPI(w http.ResponseWriter, r *http.Request) {
	if h.navService == nil {
		http.Error(w, "navigation service unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if tag := r.URL.Query().Get("tag"); tag != "" {
		notes, err := h.navService.GetNotesByTag(tag)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(notes)
		return
	}

	tags, err := h.navService.GetAllTags()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(tags)
}

// navRescan triggers a fresh filesystem scan. Admin only.
//
//	POST /-/admin/navigation/rescan
func (h *handler) navRescan(w http.ResponseWriter, r *http.Request) {
	if h.navService == nil {
		http.Error(w, "navigation service unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := h.navService.ScanDataDirectories()
	if err != nil {
		http.Error(w, "scan failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// navFolderDescription sets a custom description override for a folder. Admin only.
//
//	PUT /-/admin/navigation/folder/description?folder_id=<id>
func (h *handler) navFolderDescription(w http.ResponseWriter, r *http.Request) {
	if h.navService == nil {
		http.Error(w, "navigation service unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	folderID, err := strconv.ParseInt(r.URL.Query().Get("folder_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid or missing folder_id", http.StatusBadRequest)
		return
	}

	var req struct {
		Description string `json:"description"`
		OverrideBy  string `json:"override_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}
	if req.Description == "" {
		http.Error(w, "description required", http.StatusBadRequest)
		return
	}

	if err := h.navService.SetFolderDescription(folderID, req.Description, req.OverrideBy); err != nil {
		http.Error(w, "failed to set description: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}
