package router

import (
	"encoding/json"
	"log"
	"net/http"
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
