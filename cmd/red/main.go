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

	"github.com/RED-Collective/red-engine/internal/backup"
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
	// Cache pristine git checkouts under the state dir, never inside DataDir, so an
	// imported repo's working tree + .git history don't duplicate content into the
	// served folder.
	fetch.SetGitCacheRoot(filepath.Join(stateDir, "gitcache"))
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

	// RED_PUBLIC_URL / RED_TUNNEL_TYPE: unconditional, applied on every boot.
	// Unlike the one-time migrations above, these OVERRIDE the stored value — this
	// is the supported way to feed a dynamic tunnel address into the node. A
	// wrapper that starts cloudflared, scrapes the quick-tunnel URL, and exports
	// RED_PUBLIC_URL ensures the startup announcement advertises the CURRENT URL.
	if v := os.Getenv("RED_PUBLIC_URL"); v != "" {
		if err := registry.SetSetting("public_url", v); err != nil {
			log.Printf("RED_PUBLIC_URL: failed to store public_url: %v", err)
		} else {
			log.Printf("public_url set from RED_PUBLIC_URL=%s", v)
		}
	}
	if v := os.Getenv("RED_TUNNEL_TYPE"); v != "" {
		if err := registry.SetSetting("tunnel_type", v); err != nil {
			log.Printf("RED_TUNNEL_TYPE: failed to store tunnel_type: %v", err)
		}
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("Failed to create data dir %s: %v", cfg.DataDir, err)
	}

	// Snapshot data/ before any startup sync runs, so there is always a known-good
	// zip from before this boot to roll back to if a sync corrupts or overwrites
	// content. Snapshots live under the state dir (not data/) and are capped at 10.
	// A failed backup is logged, not fatal — the node must still start.
	backupDir := filepath.Join(stateDir, "backups")
	if bi, err := backup.CreateDataBackup(cfg.DataDir, backupDir); err != nil {
		log.Printf("Startup backup skipped: %v", err)
	} else {
		log.Printf("Startup backup: %s (%d bytes)", bi.Name, bi.SizeB)
		if err := backup.PruneBackups(backupDir, 10); err != nil {
			log.Printf("Startup backup prune failed: %v", err)
		}
	}

	s := store.New(cfg.DataDir)

	// Run startup syncs from DB. Peer (sync_type='peer') entries are handled by
	// the router's peer-sync loop, which resolves each source's live URL by
	// identity; skip them here so we never fetch a peer's base URL as a raw file.
	syncList, _ := registry.ListStartupSync()
	for _, entry := range syncList {
		if entry.SyncType == "peer" {
			continue
		}
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

	// Federation heartbeat: keep our links healthy at boot and on an interval.
	// It announces our current URL to downstream/mirror peers (so a node behind a
	// dynamic cloudflared tunnel re-registers after a restart) and re-registers us
	// as a downstream of our upstream/mirror peers (so they announce their URL
	// changes back to us). Running periodically — not once at boot — re-links peers
	// that were offline at startup or that changed URL while we were running.
	go federationHeartbeat()

	// Attribution required by NOTICE (AGPL-3.0 §7(b)) — do not remove.
	log.Printf("Powered by RED Collective — https://github.com/RED-Collective")
	log.Printf("RED Engine listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, router.New(s, &cfg)); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// federationHeartbeatInterval is how often the node re-announces its URL to
// downstreams and re-registers itself with its upstreams.
const federationHeartbeatInterval = 10 * time.Minute

// federationHeartbeat keeps this node's federation links healthy. At boot, then
// on a fixed interval, it announces our current URL to downstream/mirror peers
// and (re-)registers us as a downstream of our upstream/mirror peers. Running it
// periodically — not just once — re-links a peer that was offline at startup or
// that changed its URL while we were running.
func federationHeartbeat() {
	interval := federationHeartbeatInterval
	if v := os.Getenv("RED_FEDERATION_HEARTBEAT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = time.Duration(n) * time.Second
		}
	}
	announceStartupURL()
	registerWithUpstreams()
	router.RediscoverStalePeers()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		announceStartupURL()
		registerWithUpstreams()
		// After announcing our own (possibly new) url to reachable peers, heal any
		// peer we can no longer reach by asking a third node that stayed put. This
		// closes the dual-restart gap where both endpoints moved at once and neither
		// can re-announce directly.
		router.RediscoverStalePeers()
		// Re-gossip from all known upstream/mirror peers to pick up any nodes they
		// have discovered since we last connected.
		router.RunGossipCycle()
		// Prune peers that have been continuously offline for >30 days.
		if n, err := registry.PruneDeadPeers(30); err != nil {
			log.Printf("Heartbeat: dead-peer prune failed: %v", err)
		} else if n > 0 {
			log.Printf("Heartbeat: pruned %d peer(s) offline for >30 days", n)
		}
	}
}

// registerWithUpstreams tells every upstream/mirror peer that we pull from it, so
// it will announce its future URL changes back to us. No-op when this node
// advertises no public URL (there is nothing for an upstream to contact).
func registerWithUpstreams() {
	publicURL := registry.GetSetting("public_url")
	if publicURL == "" {
		return
	}
	peers, err := registry.ListPeersByType("upstream", "mirror")
	if err != nil {
		log.Printf("Heartbeat: failed to list upstream peers: %v", err)
		return
	}
	for _, peer := range peers {
		target := peer.URL
		if target == "" {
			target = peer.PublicURL
		}
		if target == "" {
			continue
		}
		if err := router.RegisterAsDownstream(target, publicURL); err != nil {
			log.Printf("Heartbeat: downstream registration with %q failed: %v", peer.Name, err)
		}
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
		// Surface any entries stuck in error state so operators notice in logs.
		for _, entry := range syncList {
			if entry.SyncStatus == "error" {
				log.Printf("Periodic sync: %q stuck in error state (last error: %s)", entry.Filename, entry.LastError)
			}
		}
		s.BeginRemoteSync()
		var allChanged []string
		needFullReload := false
		for _, entry := range syncList {
			if entry.SyncType == "peer" {
				continue // peer sources are re-pulled by the router's peer-sync loop
			}
			srcType := detectSrcType(entry.URL)
			destDir := filepath.Join(dataDir, filepath.Base(filepath.Clean(entry.Filename)))
			changed, err := fetch.PullDelta(entry.URL, srcType, destDir)
			if err != nil {
				log.Printf("Periodic sync: pull failed for %q: %v", entry.Filename, err)
				registry.MarkSyncResult(entry.Filename, "error", err.Error())
				continue
			}
			registry.MarkSyncResult(entry.Filename, "ok", "")
			// A nil changed-file list is the "whole tree may have been rewritten"
			// signal (git mirrors verbatim and can't cheaply diff) → force a full
			// reload. A non-nil list (possibly empty) can be hot-patched or skipped.
			if changed == nil {
				needFullReload = true
			} else {
				allChanged = append(allChanged, changed...)
			}
		}
		s.EndRemoteSync()
		switch {
		case needFullReload:
			log.Printf("Periodic sync: changes pulled — reloading content tree")
			if err := s.Reload(); err != nil {
				log.Printf("Periodic sync: full reload failed: %v", err)
			}
		case len(allChanged) > 0:
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
