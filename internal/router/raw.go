package router

import (
	"net/http"
	"strings"
)

func (h *handler) source(w http.ResponseWriter, r *http.Request) {
	targetPath := strings.TrimPrefix(r.URL.Path, "/-/source")

	// CHANGE: h.store.Resolve -> h.store.Get
	art := h.store.Get(targetPath)
	if art == nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write([]byte(art.Raw))
}

func (h *handler) download(w http.ResponseWriter, r *http.Request) {
	targetPath := strings.TrimPrefix(r.URL.Path, "/-/download")

	// CHANGE: h.store.Resolve -> h.store.Get
	art := h.store.Get(targetPath)
	if art == nil {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(strings.TrimPrefix(targetPath, "/"), "/")
	filename := parts[len(parts)-1] + ".md"

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write([]byte(art.Raw))

}
