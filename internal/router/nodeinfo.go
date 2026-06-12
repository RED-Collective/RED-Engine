package router

import (
	"encoding/json"
	"net/http"

	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/registry"
)

// Version is injected at build time via -ldflags "-X .../router.Version=vX.Y.Z".
// Falls back to "dev" when building without the flag (local dev, tests).
var Version = "dev"

func (h *handler) nodeInfo(w http.ResponseWriter, r *http.Request) {
	exportedPaths, _ := readDataDirEntries(h.store.DataDir())
	if exportedPaths == nil {
		exportedPaths = []string{}
	}

	version := Version

	info := node.GetNodeInfo(h.nodeName(), version, exportedPaths)
	// Self-reported networking metadata, sourced from node_settings.
	info.PublicURL = registry.GetSetting("public_url")
	info.TunnelType = registry.GetSetting("tunnel_type")
	info.Description = registry.GetSetting("node_description")

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(info); err != nil {
		http.Error(w, "Failed to encode node info", http.StatusInternalServerError)
	}
}
