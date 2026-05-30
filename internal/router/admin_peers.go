package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/registry"
)

type nodeInfoResponse struct {
	NodeID          string   `json:"node_id"`
	PublicKey       string   `json:"public_key"`
	Name            string   `json:"name"`
	SoftwareVersion string   `json:"software_version"`
	ExportedPaths   []string `json:"exported_paths"`
	Signature       string   `json:"signature"`
}

func fetchNodeInfo(baseURL string) (*nodeInfoResponse, error) {
	// Add scheme if missing
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "https://" + baseURL
	}
	url := strings.TrimSuffix(baseURL, "/") + "/-/nodeinfo"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to peer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer returned HTTP %d", resp.StatusCode)
	}

	var info nodeInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("invalid nodeinfo response: %w", err)
	}

	// Basic validation
	if info.PublicKey == "" {
		return nil, fmt.Errorf("peer did not provide a public key")
	}
	if info.Name == "" {
		info.Name = "Unnamed Node"
	}
	
type addPeerRequest struct {
	URL      string `json:"url"`
	PeerType string `json:"peer_type"` // upstream, downstream, mirror
}

func (h *handler) listPeers(w http.ResponseWriter, r *http.Request) {
	peers, err := registry.ListPeers()
	if err != nil {
		http.Error(w, "Failed to list peers", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peers)
}

func (h *handler) addPeer(w http.ResponseWriter, r *http.Request) {
	var req addPeerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, "URL required", http.StatusBadRequest)
		return
	}
	if req.PeerType == "" {
		req.PeerType = "upstream"
	}

	// Fetch nodeinfo from peer
	info, err := fetchNodeInfo(req.URL)
	if err != nil {
		http.Error(w, "Failed to fetch nodeinfo: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Verify signature (optional but recommended)
	// We'll trust HTTPS for now, but can add verification later.

	peer := registry.Peer{
		URL:           req.URL,
		PublicKey:     info.PublicKey,
		Name:          info.Name,
		PeerType:      req.PeerType,
		ExportedPaths: info.ExportedPaths,
		LastSeen:      time.Now(),
		Verified:      true,
		AddedAt:       time.Now(),
	}
	if err := registry.AddPeer(peer); err != nil {
		http.Error(w, "Failed to save peer: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *handler) deletePeer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, "URL required", http.StatusBadRequest)
		return
	}
	if err := registry.DeletePeer(req.URL); err != nil {
		http.Error(w, "Failed to delete peer", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
