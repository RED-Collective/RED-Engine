package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/config"
	"github.com/RED-Collective/red-engine/internal/fetch"
	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/registry"
	"github.com/RED-Collective/red-engine/internal/router"
	"github.com/RED-Collective/red-engine/internal/store"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Printf("Config not found at %s, using defaults: %v", *cfgPath, err)
		cfg = config.Default()
	}

	if v := os.Getenv("RED_TEMPLATE_DEPTH"); v != "" {
		if depth, err := strconv.Atoi(v); err == nil && depth > 0 {
			cfg.TemplateSwitchDepth = depth
		}
	}

	// Environment variables override config.json values.
	// This allows running without a config file by setting RED_* vars
	// (e.g. via docker-compose env_file or container secrets).
	if v := os.Getenv("RED_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := os.Getenv("RED_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	// StateDir holds the node identity + registry.db; two local nodes must not
	// share it, so allow an env override (config.json sets no field by default).
	if v := os.Getenv("RED_STATE_DIR"); v != "" {
		cfg.StateDir = v
	}
	if v := os.Getenv("RED_ADMIN_TOKEN"); v != "" {
		cfg.AdminToken = v
	}
	if v := os.Getenv("RED_WEBHOOK_SECRET"); v != "" {
		cfg.WebhookSecret = v
	}
	// Site/node name env vars are used only for first-boot DB seeding.
	if v := os.Getenv("RED_SITE_NAME"); v != "" && cfg.SiteName == "" {
		cfg.SiteName = v
	}
	if v := os.Getenv("RED_NODE_NAME"); v != "" && cfg.NodeName == "" {
		cfg.NodeName = v
	}
	// Frontend hosting: point the server at a built UI directory and/or allow a
	// cross-origin dev server to call the API.
	if v := os.Getenv("RED_WEB_DIR"); v != "" {
		cfg.WebDir = v
	}
	if v := os.Getenv("RED_CORS_ORIGINS"); v != "" {
		cfg.CORSOrigins = v
	}

	if cfg.AdminToken == "" {
		log.Println("WARNING: No adminToken configured — admin panel is DISABLED.")
		log.Println("  Set RED_ADMIN_TOKEN env var or add adminToken to config.json to restore access.")
	}

	// registry.db holds private state (peers, tokens, trusted authors) and must
	// not sit inside DataDir, which is served and synced as public content.
	stateDir := cfg.ResolvedStateDir()
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		log.Fatalf("Failed to create state dir %s: %v", stateDir, err)
	}
	if err := registry.InitRegistry(stateDir); err != nil {
		log.Fatalf("Failed to initialise registry: %v", err)
	}

	// Load (or generate) this node's Ed25519 identity. The public key is the
	// stable anchor used for federation and the challenge-response handshake;
	// without this, GetNodePublicKey/SignNodeInfo would have no key to use.
	if err := node.InitNodeIdentity(stateDir); err != nil {
		log.Fatalf("Failed to initialise node identity: %v", err)
	}

	// One-time migration: startup_sync entries from config.json → DB.
	existing, _ := registry.ListStartupSync()
	if len(existing) == 0 && len(cfg.StartupSync) > 0 {
		for _, s := range cfg.StartupSync {
			if err := registry.AddStartupSync(s.URL, s.Filename); err != nil {
				log.Printf("Migration: failed to import %q: %v", s.Filename, err)
			}
		}
		log.Printf("Migrated %d startup sync entries from config.json to database", len(cfg.StartupSync))
	}

	// One-time migration: siteName / nodeName from config.json → node_settings DB table.
	if registry.GetSetting("site_name") == "" && cfg.SiteName != "" {
		if err := registry.SetSetting("site_name", cfg.SiteName); err != nil {
			log.Printf("Migration: failed to store site_name: %v", err)
		} else {
			log.Printf("Migrated site_name=%q to database", cfg.SiteName)
		}
	}
	if registry.GetSetting("node_name") == "" && cfg.NodeName != "" {
		if err := registry.SetSetting("node_name", cfg.NodeName); err != nil {
			log.Printf("Migration: failed to store node_name: %v", err)
		} else {
			log.Printf("Migrated node_name=%q to database", cfg.NodeName)
		}
	}

	// One-time migration: networking metadata from config.json → node_settings.
	// These keys are read by /-/nodeinfo, /-/nodes and the announcement client.
	for _, m := range []struct{ key, val string }{
		{"public_url", cfg.PublicURL},
		{"tunnel_type", cfg.TunnelType},
		{"node_description", cfg.NodeDescription},
	} {
		if m.val != "" && registry.GetSetting(m.key) == "" {
			if err := registry.SetSetting(m.key, m.val); err != nil {
				log.Printf("Migration: failed to store %s: %v", m.key, err)
			} else {
				log.Printf("Migrated %s=%q to database", m.key, m.val)
			}
		}
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("Failed to create data dir %s: %v", cfg.DataDir, err)
	}

	s := store.New(cfg.DataDir)

	// Run startup syncs from DB.
	syncList, _ := registry.ListStartupSync()
	for _, entry := range syncList {
		log.Printf("Startup sync: %s", entry.Filename)
		srcType := detectSrcType(entry.URL)
		destDir := filepath.Join(cfg.DataDir, filepath.Base(filepath.Clean(entry.Filename)))
		if err := fetch.Pull(entry.URL, srcType, destDir); err != nil {
			log.Printf("Startup sync failed for %q: %v", entry.Filename, err)
			registry.MarkSyncResult(entry.Filename, "error", err.Error())
		} else {
			registry.MarkSyncResult(entry.Filename, "ok", "")
		}
	}

	if err := s.Reload(); err != nil {
		log.Printf("Initial reload warning: %v", err)
	}

	if err := s.Watch(); err != nil {
		log.Printf("File watcher warning: %v", err)
	}

	go periodicSync(s, cfg.DataDir)

	// Startup announcement: if this node advertises a public URL, tell every
	// downstream/mirror peer about it via the challenge-response handshake. This
	// is what lets a node behind a dynamic (cloudflared quick) tunnel re-register
	// its new URL after a restart. Upstream-only peers are never contacted —
	// they pull from us and do not need to know our address.
	go announceStartupURL()

	// Attribution required by NOTICE (AGPL-3.0 §7(b)) — do not remove.
	log.Printf("Powered by RED Collective — https://github.com/RED-Collective")
	log.Printf("RED Engine listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, router.New(s, &cfg)); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// announceStartupURL notifies downstream and mirror peers of this node's
// current public URL using the signed challenge-response handshake. It is a
// no-op when no public_url is configured (a private, pull-only node stays
// invisible to the network). Failures are logged per-peer, never fatal.
func announceStartupURL() {
	publicURL := registry.GetSetting("public_url")
	if publicURL == "" {
		return
	}
	peers, err := registry.ListPeersByType("downstream", "mirror")
	if err != nil {
		log.Printf("Startup announce: failed to list peers: %v", err)
		return
	}
	for _, peer := range peers {
		if err := router.AnnounceURLToPeer(peer, publicURL); err != nil {
			log.Printf("Startup announce to %q failed: %v", peer.Name, err)
		} else {
			log.Printf("Startup announce: notified %q of URL %s", peer.Name, publicURL)
		}
	}
}

func periodicSync(s *store.Store, dataDir string) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		syncList, err := registry.ListStartupSync()
		if err != nil {
			log.Printf("Periodic sync: failed to read registry: %v", err)
			continue
		}
		s.BeginRemoteSync()
		var allChanged []string
		for _, entry := range syncList {
			srcType := detectSrcType(entry.URL)
			destDir := filepath.Join(dataDir, filepath.Base(filepath.Clean(entry.Filename)))
			changed, err := fetch.PullDelta(entry.URL, srcType, destDir)
			if err != nil {
				log.Printf("Periodic sync: pull failed for %q: %v", entry.Filename, err)
				registry.MarkSyncResult(entry.Filename, "error", err.Error())
				continue
			}
			registry.MarkSyncResult(entry.Filename, "ok", "")
			allChanged = append(allChanged, changed...)
		}
		s.EndRemoteSync()
		if len(allChanged) > 0 {
			if err := s.UpdateFiles(allChanged); err != nil {
				log.Printf("Periodic sync: hot-reload failed, falling back to full reload: %v", err)
				s.Reload()
			}
		}
	}
}

func detectSrcType(u string) string {
	lower := strings.ToLower(u)
	switch {
	case strings.HasSuffix(lower, ".git"):
		return "git"
	case strings.HasSuffix(lower, ".tar.gz"):
		return "tar.gz"
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	default:
		return "raw"
	}
}
