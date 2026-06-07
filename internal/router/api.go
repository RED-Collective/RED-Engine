package router

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/models"
)

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		log.Printf("⚠️ health: write error: %v", err)
	}
}

func (h *handler) manifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if _, err := w.Write([]byte(`{"name":"RED Engine","short_name":"RED","start_url":"/","display":"standalone"}`)); err != nil {
		log.Printf("⚠️ manifest: write error: %v", err)
	}
}

// recentFiles returns the most recently modified articles across all sections.
// GET /api/recent-files?limit=N (default 5, max 20)
// RED_KNOWLEDGE directory-default articles are exposed with the directory path.
func (h *handler) recentFiles(w http.ResponseWriter, r *http.Request) {
	limit := 5
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			if n > 20 {
				n = 20
			}
			limit = n
		}
	}

	type candidate struct {
		title             string
		path              string
		signerKey         string
		verificationState string
		mtime             time.Time
	}

	var items []candidate

	// hasHiddenSegment returns true if any path segment starts with '.'.
	hasHiddenSegment := func(p string) bool {
		for _, seg := range strings.Split(strings.Trim(p, "/"), "/") {
			if strings.HasPrefix(seg, ".") {
				return true
			}
		}
		return false
	}

	var walkSection func(sec *models.Section)
	walkSection = func(sec *models.Section) {
		for _, art := range sec.Articles {
			if hasHiddenSegment(art.Path) {
				continue
			}
			displayPath := art.Path
			title := art.Title
			if strings.HasSuffix(art.Path, "/RED_KNOWLEDGE") {
				displayPath = strings.TrimSuffix(art.Path, "/RED_KNOWLEDGE")
				// The RED_KNOWLEDGE file often lacks a heading, leaving the
				// title as the filename. Use the folder name instead.
				if title == "RED_KNOWLEDGE" || title == "" {
					seg := strings.Split(strings.Trim(displayPath, "/"), "/")
					title = capitalize(seg[len(seg)-1])
				}
			}
			filePath := filepath.Join(h.store.DataDir(), strings.TrimPrefix(art.Path, "/")+".md")
			var mtime time.Time
			if info, err := os.Stat(filePath); err == nil {
				mtime = info.ModTime()
			}
			items = append(items, candidate{
				title:             title,
				path:              displayPath,
				signerKey:         art.SignerKey,
				verificationState: art.VerificationState,
				mtime:             mtime,
			})
		}
		for _, sub := range sec.Sub {
			walkSection(sub)
		}
	}

	for _, sec := range h.store.Root() {
		walkSection(sec)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].mtime.After(items[j].mtime)
	})
	if len(items) > limit {
		items = items[:limit]
	}

	type result struct {
		Title             string `json:"title"`
		Path              string `json:"path"`
		SignerKey         string `json:"signer_key,omitempty"`
		VerificationState string `json:"verification_state"`
	}

	out := make([]result, 0, len(items))
	for _, it := range items {
		out = append(out, result{
			Title:             it.title,
			Path:              it.path,
			SignerKey:         it.signerKey,
			VerificationState: it.verificationState,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Printf("recentFiles: encode error: %v", err)
	}
}

func (h *handler) searchIndex(w http.ResponseWriter, r *http.Request) {
	index := h.store.BuildSearchIndex()

	var payload []byte
	var err error

	if index == nil {
		payload = []byte("[]")
	} else {
		payload, err = json.Marshal(index)
		if err != nil {
			http.Error(w, "Failed to generate search index", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(http.StatusOK)

	if _, err = w.Write(payload); err != nil {
		log.Printf("⚠️ searchIndex: write error: %v", err)
	}
}
