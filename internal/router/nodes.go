package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/registry"
)

// nodeAPIItem is a single entry in the /api/nodes response — used for both
// this node (self) and known peers. IsSelf distinguishes the two.
type nodeAPIItem struct {
	URL           string   `json:"url"`
	Name          string   `json:"name"`
	PublicKey     string   `json:"public_key,omitempty"`
	Description   string   `json:"description,omitempty"`
	TunnelType    string   `json:"tunnel_type,omitempty"`
	IsOnline      bool     `json:"is_online"`
	IsSelf        bool     `json:"is_self"`
	ExportedPaths []string `json:"exported_paths"`
	LastSeen      string   `json:"last_seen,omitempty"`
}

// nodesAPIResponse is the envelope returned by GET /api/nodes.
type nodesAPIResponse struct {
	Self  nodeAPIItem   `json:"self"`
	Nodes []nodeAPIItem `json:"nodes"`
}

// nodesAPI serves GET /api/nodes — a public JSON directory of this node and
// all known peers. Designed for a frontend node-browser UI; does not include
// internal admin-only peer fields. Peers with no contactable URL are omitted.
func (h *handler) nodesAPI(w http.ResponseWriter, r *http.Request) {
	selfKey := node.GetNodePublicKey()
	selfURL := ensureScheme(registry.GetSetting("public_url"))

	self := nodeAPIItem{
		URL:         selfURL,
		Name:        h.nodeName(),
		PublicKey:   selfKey,
		Description: registry.GetSetting("node_description"),
		TunnelType:  registry.GetSetting("tunnel_type"),
		IsOnline:    true,
		IsSelf:      true,
	}

	// Exported paths: top-level directories in data/.
	if entries, err := readDataDirEntries(h.store.DataDir()); err == nil {
		self.ExportedPaths = entries
	}
	if self.ExportedPaths == nil {
		self.ExportedPaths = []string{}
	}

	peers, err := registry.ListPeers()
	if err != nil {
		http.Error(w, "failed to list peers", http.StatusInternalServerError)
		return
	}

	items := make([]nodeAPIItem, 0, len(peers))
	for _, p := range peers {
		contact := p.PublicURL
		if contact == "" {
			contact = p.URL
		}
		if contact == "" {
			continue
		}
		paths := p.ExportedPaths
		if paths == nil {
			paths = []string{}
		}
		item := nodeAPIItem{
			URL:           ensureScheme(contact),
			Name:          p.Name,
			PublicKey:     p.PublicKey,
			Description:   p.Description,
			TunnelType:    p.TunnelType,
			IsOnline:      p.IsOnline,
			IsSelf:        false,
			ExportedPaths: paths,
		}
		if !p.LastSeen.IsZero() {
			item.LastSeen = p.LastSeen.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}

	resp := nodesAPIResponse{Self: self, Nodes: items}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// nodesPageData is the view model for the public /-/nodes directory page.
type nodesPageData struct {
	Self  nodeSelf
	Peers []nodeCard
}

// nodeSelf describes this node's own identity, shown in the page header so
// visitors know which node they are on and can add it as a peer.
type nodeSelf struct {
	Name           string
	PublicKey      string
	PublicKeyShort string
	PublicURL      string
	TunnelLabel    string
	Description    string
}

// nodeCard is a single peer entry rendered on the directory page.
type nodeCard struct {
	Name           string
	URL            string // contactable URL, scheme-normalised
	PublicKey      string
	PublicKeyShort string
	PeerType       string
	Description    string
	TunnelLabel    string
	IsOnline       bool
	ExportedPaths  []string
	LastSeen       string
}

// tunnelLabel maps a stored tunnel_type value to a human-readable badge.
func tunnelLabel(t string) string {
	switch t {
	case "cloudflare_quick":
		return "Cloudflare Quick"
	case "cloudflare_named":
		return "Cloudflare Named"
	case "direct":
		return "Direct"
	default:
		return ""
	}
}

// ensureScheme prepends https:// when a URL has no scheme, so links and the
// copy-URL button always produce something contactable.
func ensureScheme(u string) string {
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "https://" + u
	}
	return u
}

// shortKey returns the first 16 chars of a hex key for compact display.
func shortKey(k string) string {
	if len(k) > 16 {
		return k[:16]
	}
	return k
}

// relativeTime renders a coarse "time ago" string for last-seen timestamps.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%d min ago", m)
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

// nodes serves GET /-/nodes — a human-readable directory of known peers.
// Unauthenticated and server-side rendered. Online status is read from the
// cached health columns, never live-probed on render.
func (h *handler) nodes(w http.ResponseWriter, r *http.Request) {
	peers, err := registry.ListPeers()
	if err != nil {
		http.Error(w, "Failed to list peers", http.StatusInternalServerError)
		return
	}

	cards := make([]nodeCard, 0, len(peers))
	for _, p := range peers {
		contact := p.PublicURL
		if contact == "" {
			contact = p.URL
		}
		paths := p.ExportedPaths
		if paths == nil {
			paths = []string{}
		}
		cards = append(cards, nodeCard{
			Name:           p.Name,
			URL:            ensureScheme(contact),
			PublicKey:      p.PublicKey,
			PublicKeyShort: shortKey(p.PublicKey),
			PeerType:       p.PeerType,
			Description:    p.Description,
			TunnelLabel:    tunnelLabel(p.TunnelType),
			IsOnline:       p.IsOnline,
			ExportedPaths:  paths,
			LastSeen:       relativeTime(p.LastSeen),
		})
	}

	selfKey := node.GetNodePublicKey()
	data := nodesPageData{
		Self: nodeSelf{
			Name:           h.nodeName(),
			PublicKey:      selfKey,
			PublicKeyShort: shortKey(selfKey),
			PublicURL:      ensureScheme(registry.GetSetting("public_url")),
			TunnelLabel:    tunnelLabel(registry.GetSetting("tunnel_type")),
			Description:    registry.GetSetting("node_description"),
		},
		Peers: cards,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.nodesTmpl.ExecuteTemplate(w, "nodes.html", data); err != nil {
		http.Error(w, "Nodes template execution error: "+err.Error(), http.StatusInternalServerError)
	}
}

// readDataDirEntries returns the top-level directory names inside dataDir as
// slash-prefixed exported-path strings (e.g. ["/Vault", "/Archive"]).
func readDataDirEntries(dataDir string) ([]string, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			paths = append(paths, "/"+e.Name())
		}
	}
	return paths, nil
}
