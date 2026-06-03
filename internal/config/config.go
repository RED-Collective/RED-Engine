package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RemoteSync is kept for one-time migration from config.json to the database.
type RemoteSync struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
}

type Config struct {
	// Bootstrap — needed before the database is open.
	Addr    string `json:"addr"`
	DataDir string `json:"dataDir"`

	// StateDir holds private node state (registry.db, identity keys). It must
	// live OUTSIDE DataDir, which is served and synced as public content.
	// Empty means the default ~/.red-engine (see ResolvedStateDir).
	StateDir string `json:"stateDir"`

	// Security credentials — must stay outside the database they protect.
	AdminToken    string `json:"adminToken"`
	WebhookSecret string `json:"webhookSecret"`

	// Deprecated — read-only, migrated to node_settings DB table on first run.
	SiteName string `json:"siteName"`
	NodeName string `json:"nodeName"`

	// Networking — migrated to node_settings DB table on first run. These let a
	// node advertise how it is reachable. PublicURL is this node's externally
	// reachable address (e.g. a cloudflared tunnel URL); TunnelType is one of
	// "direct"|"cloudflare_quick"|"cloudflare_named"; NodeDescription is a short
	// human blurb shown on the /-/nodes directory page.
	PublicURL       string `json:"publicURL"`
	TunnelType      string `json:"tunnelType"`
	NodeDescription string `json:"nodeDescription"`

	// Deprecated — read-only, migrated to startup_sync DB table on first run.
	StartupSync         []RemoteSync `json:"startupSync"`
	TemplateSwitchDepth int          `json:"templateSwitchDepth"` // default 2

}

func Default() Config {
	return Config{
		Addr:                ":8080",
		DataDir:             "./data",
		TemplateSwitchDepth: 2,
	}
}

// ResolvedStateDir returns the directory for private node state (registry.db,
// identity keys). It must stay outside DataDir, which is served as public
// content. Defaults to ~/.red-engine, matching the node identity key location
// in internal/node/identity.go.
func (c *Config) ResolvedStateDir() string {
	if c.StateDir != "" {
		return c.StateDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".red-engine"
	}
	return filepath.Join(home, ".red-engine")
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
