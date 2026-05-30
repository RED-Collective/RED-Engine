package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/config"
	"github.com/RED-Collective/red-engine/internal/fetch"
	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/router"
	"github.com/RED-Collective/red-engine/internal/store"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	pull := flag.Bool("pull", false, "fetch knowledge base before starting")
	flag.Parse()

	cfg := config.Default()
	if _, err := os.Stat(*cfgPath); err == nil {
		loaded, err := config.Load(*cfgPath)
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		cfg = loaded
	}

	if err := node.InitNodeIdentity(); err != nil {
		log.Fatalf("Failed to initialise node identity: %v", err)
	}

	if cfg.AdminToken == "" || cfg.AdminToken == "secret123" {
		log.Println("=================================================================")
		log.Println("⚠️  SECURITY WARNING: Using default or missing Admin Token!    ⚠️")
		log.Println("⚠️  Anyone on the internet can overwrite your markdown files!  ⚠️")
		log.Println("=================================================================")
	}

	if *pull && cfg.SourceURL != "" {
		if err := fetch.Pull(cfg.SourceURL, cfg.SourceType, cfg.DataDir); err != nil {
			log.Fatalf("fetch: %v", err)
		}
	}

	s := store.New(cfg.DataDir)
	if err := s.Reload(); err != nil {
		log.Fatalf("store: %v", err)
	}

	// 2. Startup & Background Sync
	if len(cfg.StartupSync) > 0 {

		// FIX: Wrap the entire boot sequence inside the goroutine and apply locks
		go func() {
			s.BeginRemoteSync() // Lock out the local watcher

			for _, sync := range cfg.StartupSync {
				cleanName := filepath.Base(filepath.Clean(sync.Filename))
				destDir := filepath.Join(cfg.DataDir, cleanName)
				log.Printf("Startup Sync: Fetching %s...", cleanName)

				srcType := "raw"
				lowerURL := strings.ToLower(sync.URL)
				if strings.HasSuffix(lowerURL, ".git") {
					srcType = "git"
				} else if strings.HasSuffix(lowerURL, ".tar.gz") {
					srcType = "tar.gz"
				} else if strings.HasSuffix(lowerURL, ".zip") {
					srcType = "zip"
				}

				if err := fetch.Pull(sync.URL, srcType, destDir); err != nil {
					log.Printf("Startup Sync Error (%s): %v", sync.Filename, err)
				}
			}

			s.EndRemoteSync() // Release the lock

			// FIX: Force memory update AFTER the initial boot downloads finish
			if err := s.Reload(); err != nil {
				log.Printf("⚠️ Startup Reload Error: %v", err)
			} else {
				log.Println("✅ Startup Sync Complete: Memory map populated.")
			}
		}()

		// Background Smart Polling Loop (Runs every 1 minute)
		go func() {
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				cfg.Mu.RLock()
				syncList := make([]config.RemoteSync, len(cfg.StartupSync))
				copy(syncList, cfg.StartupSync)
				cfg.Mu.RUnlock()

				for _, sync := range syncList {
					cleanName := filepath.Base(filepath.Clean(sync.Filename))
					destDir := filepath.Join(cfg.DataDir, cleanName)

					srcType := "raw"
					lowerURL := strings.ToLower(sync.URL)
					if strings.HasSuffix(lowerURL, ".git") {
						srcType = "git"
					} else if strings.HasSuffix(lowerURL, ".tar.gz") {
						srcType = "tar.gz"
					} else if strings.HasSuffix(lowerURL, ".zip") {
						srcType = "zip"
					}

					changedFiles, err := fetch.PullDelta(sync.URL, srcType, destDir)
					if err != nil {
						log.Printf("Background Sync Error (%s): %v", sync.Filename, err)
						continue
					}

					if changedFiles == nil {
						s.Reload()
					} else if len(changedFiles) > 0 {
						log.Printf("⚡ Background Sync: Hot-Patching %d modified files...", len(changedFiles))
						if err := s.UpdateFiles(changedFiles); err != nil {
							log.Printf("⚠️ Partial hot-reload failed, falling back to full reload: %v", err)
							s.Reload()
						}
					}
				}
			}
		}()
	}

	// Start watching local files for live edits
	if err := s.Watch(); err != nil {
		log.Printf("⚠️ Failed to start file watcher: %v", err)
	}

	// 4. Start HTTP Server with the Refactored Router
	h := router.New(s, &cfg, *cfgPath)
	log.Printf("RED listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, h))
}
