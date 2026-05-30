package router

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/RED-Collective/red-engine/internal/node"
)

func (h *handler) nodeInfo(w http.ResponseWriter, r *http.Request) {
	// For now, exported paths are empty – later we can fill from registry or config.
	exportedPaths := []string{}
	nodeName := h.cfg.NodeName
	if nodeName == "" {
		nodeName = h.cfg.SiteName
	}
	if nodeName == "" {
		nodeName, _ = os.Hostname()
	}
	version := "v1.2.0" // This is to embed version from build

	info, err := node.GetNodeInfo(nodeName, version, exportedPaths)
	if err != nil {
		http.Error(w, "Node identity not ready", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(info); err != nil {
		http.Error(w, "Failed to encode node info", http.StatusInternalServerError)
	}
}
