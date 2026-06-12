package router

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/fetch"
	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/registry"
)

type nodeInfoResponse struct {
	Name            string   `json:"name"`
	PublicKey       string   `json:"public_key"`
	SoftwareVersion string   `json:"software_version"`
	ExportedPaths   []string `json:"exported_paths"`
	PublicURL       string   `json:"public_url"`
	TunnelType      string   `json:"tunnel_type"`
	Description     string   `json:"description"`
}

type addPeerRequest struct {
	URL         string `json:"url"`
	PeerType    string `json:"peer_type"`
	ImportPeers bool   `json:"import_peers"`
}

// FetchNodeInfo retrieves a peer's nodeinfo.
func FetchNodeInfo(baseURL string) (*nodeInfoResponse, error) {
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "https://" + baseURL
	}
	url := strings.TrimSuffix(baseURL, "/") + "/-/nodeinfo"

	client := fetch.SafeClient()
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

	if info.Name == "" {
		info.Name = "Unnamed Node"
	}
	return &info, nil
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

	info, err := FetchNodeInfo(req.URL)
	if err != nil {
		http.Error(w, "Failed to fetch nodeinfo: "+err.Error(), http.StatusBadGateway)
		return
	}

	peer := registry.Peer{
		URL:           req.URL,
		PublicKey:     info.PublicKey,
		Name:          info.Name,
		PeerType:      req.PeerType,
		Description:   info.Description,
		PublicURL:     info.PublicURL,
		TunnelType:    info.TunnelType,
		ExportedPaths: info.ExportedPaths,
		LastSeen:      time.Now(),
		AddedAt:       time.Now(),
	}
	if err := registry.AddPeer(peer); err != nil {
		http.Error(w, "Failed to save peer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Reciprocal registration: when we add an upstream/mirror (a node we pull
	// from), tell it we are now its downstream so it announces future URL changes
	// back to us. Without this the upstream never knows to re-announce, and a
	// dynamic-tunnel upstream becomes unreachable after it restarts. Best-effort.
	if req.PeerType == "upstream" || req.PeerType == "mirror" {
		if self := registry.GetSetting("public_url"); self != "" {
			go func(upstream, selfURL string) {
				if err := RegisterAsDownstream(upstream, selfURL); err != nil {
					log.Printf("[Peer] reciprocal downstream registration with %s failed: %v", upstream, err)
				}
			}(req.URL, self)
		}
	}

	// Optional gossip: run synchronously so the caller sees the imported peers
	// immediately (async fire-and-forget caused a UX bug where peers appeared
	// only after a second add because the goroutine outlived the request).
	gossipCount := 0
	if req.ImportPeers {
		gossipCount = importPeerGossip(req.URL)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]int{"gossip_imported": gossipCount})
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

func (h *handler) checkPeerHealth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	client := fetch.SafeClient()
	resp, err := client.Get(strings.TrimSuffix(req.URL, "/") + "/-/nodeinfo")
	if err != nil || resp.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("down"))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("up"))
}

func (h *handler) checkPeerHealthHandler(w http.ResponseWriter, r *http.Request) {
	peerURL := r.URL.Query().Get("url")
	if peerURL == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}
	client := fetch.SafeClient()
	resp, err := client.Get(strings.TrimSuffix(peerURL, "/") + "/-/nodeinfo")
	status := "down"
	if err == nil && resp.StatusCode == http.StatusOK {
		status = "up"
	}
	if resp != nil {
		resp.Body.Close()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func (h *handler) refreshPeer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, "URL required", http.StatusBadRequest)
		return
	}

	info, err := FetchNodeInfo(req.URL)
	if err != nil {
		http.Error(w, "Failed to fetch nodeinfo: "+err.Error(), http.StatusBadGateway)
		return
	}

	peer := registry.Peer{
		URL:           req.URL,
		PublicKey:     info.PublicKey,
		Name:          info.Name,
		Description:   info.Description,
		PublicURL:     info.PublicURL,
		TunnelType:    info.TunnelType,
		ExportedPaths: info.ExportedPaths,
		LastSeen:      time.Now(),
	}
	if err := registry.AddPeer(peer); err != nil {
		http.Error(w, "Failed to update peer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// RefreshPeer updates the peer record in the database with fresh nodeinfo.
func RefreshPeer(peerURL string) error {
	info, err := FetchNodeInfo(peerURL)
	if err != nil {
		return err
	}
	peer := registry.Peer{
		URL:           peerURL,
		PublicKey:     info.PublicKey,
		Name:          info.Name,
		Description:   info.Description,
		PublicURL:     info.PublicURL,
		TunnelType:    info.TunnelType,
		ExportedPaths: info.ExportedPaths,
		LastSeen:      time.Now(),
	}
	return registry.AddPeer(peer)
}

// peerListItem is the public JSON shape returned by GET /-/peers. It exposes
// only what other nodes need to discover and contact this node's peers — never
// admin-only fields. ExportedPaths is joined from peer_exported_paths.
type peerListItem struct {
	URL           string   `json:"url"`
	PublicKey     string   `json:"public_key,omitempty"`
	Name          string   `json:"name"`
	PeerType      string   `json:"peer_type"`
	Description   string   `json:"description,omitempty"`
	PublicURL     string   `json:"public_url,omitempty"`
	TunnelType    string   `json:"tunnel_type,omitempty"`
	IsOnline      bool     `json:"is_online"`
	ExportedPaths []string `json:"exported_paths"`
	LastSeen      string   `json:"last_seen,omitempty"`
}

// publicPeers serves GET /-/peers — an unauthenticated JSON list of this node's
// known peers, used for gossip discovery by other nodes. The peer URL each entry
// advertises is its self-reported public_url when available, otherwise the URL
// we reach it at. We never leak peers that have no contactable address.
func (h *handler) publicPeers(w http.ResponseWriter, r *http.Request) {
	peers, err := registry.ListPeers()
	if err != nil {
		http.Error(w, "Failed to list peers", http.StatusInternalServerError)
		return
	}

	items := make([]peerListItem, 0, len(peers))
	for _, p := range peers {
		advertised := p.PublicURL
		if advertised == "" {
			advertised = p.URL
		}
		if advertised == "" {
			continue
		}
		item := peerListItem{
			URL:           advertised,
			PublicKey:     p.PublicKey,
			Name:          p.Name,
			PeerType:      p.PeerType,
			Description:   p.Description,
			PublicURL:     p.PublicURL,
			TunnelType:    p.TunnelType,
			IsOnline:      p.IsOnline,
			ExportedPaths: p.ExportedPaths,
		}
		if item.ExportedPaths == nil {
			item.ExportedPaths = []string{}
		}
		if !p.LastSeen.IsZero() {
			item.LastSeen = p.LastSeen.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// importPeerGossip fetches a peer's /-/peers list and registers any unknown
// nodes as upstream ONLY (privacy opt-in — the operator must manually promote
// them to downstream/mirror). Returns the number of newly imported peers.
func importPeerGossip(peerURL string) int {
	base := peerURL
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	endpoint := strings.TrimSuffix(base, "/") + "/-/peers"

	client := fetch.SafeClient()
	resp, err := client.Get(endpoint)
	if err != nil {
		log.Printf("[Gossip] fetch %s: %v", endpoint, err)
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[Gossip] %s returned HTTP %d", endpoint, resp.StatusCode)
		return 0
	}

	var remote []peerListItem
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		log.Printf("[Gossip] decode %s: %v", endpoint, err)
		return 0
	}

	self := registry.GetSetting("public_url")
	selfKey := node.GetNodePublicKey()
	imported := 0
	for _, r := range remote {
		if r.URL == "" || r.URL == self || r.URL == peerURL {
			continue
		}
		// Skip ourselves by identity, never re-import our own node.
		if selfKey != "" && r.PublicKey == selfKey {
			continue
		}
		// Skip peers we already know (by key when present, else by URL).
		if r.PublicKey != "" {
			if existing, _ := registry.GetPeerByPublicKey(r.PublicKey); existing != nil {
				continue
			}
		} else if existing, _ := registry.GetPeerByURL(r.URL); existing != nil {
			continue
		}

		p := registry.Peer{
			URL:           r.URL,
			PublicKey:     r.PublicKey,
			Name:          r.Name,
			PeerType:      "upstream", // always upstream for gossip
			Description:   r.Description,
			PublicURL:     r.PublicURL,
			TunnelType:    r.TunnelType,
			ExportedPaths: r.ExportedPaths,
			LastSeen:      time.Now(),
			AddedAt:       time.Now(),
		}
		if err := registry.AddPeer(p); err != nil {
			log.Printf("[Gossip] add %s: %v", r.URL, err)
			continue
		}
		imported++
	}
	if imported > 0 {
		log.Printf("[Gossip] imported %d new upstream peer(s) from %s", imported, peerURL)
	}
	return imported
}

// RunGossipCycle re-gossips from all known upstream/mirror peers, registering any
// newly discovered nodes. Called periodically from the federation heartbeat so
// gossip is refreshed rather than running only once on peer-add.
func RunGossipCycle() {
	peers, err := registry.ListPeersByType("upstream", "mirror")
	if err != nil {
		log.Printf("[Gossip] heartbeat list failed: %v", err)
		return
	}
	for _, p := range peers {
		target := p.PublicURL
		if target == "" {
			target = p.URL
		}
		if target != "" {
			go importPeerGossip(target)
		}
	}
}
