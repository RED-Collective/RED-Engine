package router

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/fetch"
	"github.com/RED-Collective/red-engine/internal/registry"
)

func (h *handler) importRemote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URL           string `json:"url"`
		Filename      string `json:"filename"`
		SaveToStartup bool   `json:"saveToStartup"`
		PeerURL       string `json:"peer_url"`
		RemotePath    string `json:"remote_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// ---- Peer-based sync ----
	if req.PeerURL != "" && req.RemotePath != "" {
		// The local top-level folder comes from the peer's manifest, so the whole
		// data root is passed; req.Filename is no longer the destination.
		if err := h.pullFromPeer(req.PeerURL, req.RemotePath, h.store.DataDir(), req.Filename); err != nil {
			http.Error(w, "Peer sync failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		if err := h.store.Reload(); err != nil {
			http.Error(w, "Reload after peer sync failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// store.Reload fires the navigation reindex hook (folder tree/counts).
		if req.SaveToStartup {
			// Anchor the recurring sync to the peer's identity, not a frozen URL:
			// the periodic peer-sync loop resolves the peer's current address from
			// its key, so re-pull follows the peer across tunnel-URL changes.
			peerKey := ""
			if p, _ := registry.GetPeerByURL(strings.TrimSuffix(req.PeerURL, "/")); p != nil {
				peerKey = p.PublicKey
			}
			if err := registry.AddPeerStartupSync(peerKey, req.PeerURL, req.RemotePath, req.Filename); err != nil {
				http.Error(w, "Saved content but failed to update database: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Peer sync completed from " + req.PeerURL + "/" + req.RemotePath))
		return
	}

	// ---- Normal URL-based import ----
	if req.URL == "" {
		http.Error(w, "URL required", http.StatusBadRequest)
		return
	}

	// SSRF protection
	parsedURL, err := url.Parse(req.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		http.Error(w, "Invalid URL scheme", http.StatusBadRequest)
		return
	}
	hostname := parsedURL.Hostname()
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		http.Error(w, "Failed to resolve hostname", http.StatusBadRequest)
		return
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		// Unparseable, link-local (incl. 169.254.169.254 metadata) and multicast
		// are ALWAYS forbidden.
		if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			http.Error(w, "Local network imports are strictly forbidden", http.StatusForbidden)
			return
		}
		// Loopback / RFC1918 / unspecified are forbidden unless local-dev
		// federation testing is explicitly enabled.
		if !fetch.AllowPrivateSync() && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified()) {
			http.Error(w, "Local network imports are strictly forbidden", http.StatusForbidden)
			return
		}
	}

	// GitHub URL rewriter
	if parsedURL.Host == "github.com" {
		pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
		if len(pathParts) == 2 {
			repoName := strings.TrimSuffix(pathParts[1], ".git")
			req.URL = "https://github.com/" + pathParts[0] + "/" + repoName + ".git"
			parsedURL, _ = url.Parse(req.URL)
		} else if len(pathParts) > 2 && pathParts[2] == "blob" {
			req.URL = "https://raw.githubusercontent.com/" + pathParts[0] + "/" + pathParts[1] + "/" + strings.Join(pathParts[3:], "/")
			parsedURL, _ = url.Parse(req.URL)
		}
	}

	// Path sanitization and auto-naming
	targetSubPath := filepath.Clean(req.Filename)
	if targetSubPath == "." || targetSubPath == "" {
		pathParts := strings.Split(strings.TrimRight(parsedURL.Path, "/"), "/")
		if len(pathParts) > 0 {
			if parsedURL.Host == "github.com" && len(pathParts) >= 4 && pathParts[3] == "archive" {
				targetSubPath = pathParts[2]
			} else {
				lastPart := pathParts[len(pathParts)-1]
				lastPart = strings.TrimSuffix(lastPart, ".zip")
				lastPart = strings.TrimSuffix(lastPart, ".tar.gz")
				lastPart = strings.TrimSuffix(lastPart, ".tgz")
				lastPart = strings.TrimSuffix(lastPart, ".md")
				lastPart = strings.TrimSuffix(lastPart, ".git")
				if lastPart != "" {
					targetSubPath = lastPart
				}
			}
		}
	}
	if targetSubPath == "." || targetSubPath == "" || strings.HasPrefix(targetSubPath, "..") || filepath.IsAbs(targetSubPath) {
		targetSubPath = "sync-" + time.Now().Format("20060102150405")
	}

	destinationDir := filepath.Join(h.store.DataDir(), targetSubPath)
	lowerURL := strings.ToLower(req.URL)

	srcType := "raw"
	if strings.HasSuffix(lowerURL, ".git") {
		srcType = "git"
	} else if strings.HasSuffix(lowerURL, ".tar.gz") {
		srcType = "tar.gz"
	} else if strings.HasSuffix(lowerURL, ".zip") {
		srcType = "zip"
	}

	if srcType == "git" || srcType == "tar.gz" || srcType == "zip" {
		if err := fetch.Pull(req.URL, srcType, destinationDir); err != nil {
			http.Error(w, "Failed to pull remote repository: "+err.Error(), http.StatusBadGateway)
			return
		}
	} else {
		if !strings.HasSuffix(strings.ToLower(targetSubPath), ".md") {
			targetSubPath += ".md"
		}
		httpReq, err := http.NewRequest(http.MethodGet, req.URL, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		httpReq.Header.Set("User-Agent", "RED-Engine-Sync/1.0")
		// Use the SSRF-hardened client so the actual connection re-resolves and
		// re-checks the host inside DialContext. The manual LookupHost pre-check
		// above is only a fast-fail; on its own it leaves a DNS-rebinding window
		// (resolve-public-at-check-time, connect-private-at-dial-time). SafeClient
		// closes that window. Loopback/RFC1918 stay gated by RED_ALLOW_PRIVATE_SYNC.
		client := fetch.SafeClient()
		resp, err := client.Do(httpReq)
		if err != nil {
			http.Error(w, "Failed to connect to remote server", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			http.Error(w, "Remote server returned non-OK status", http.StatusBadGateway)
			return
		}
		content, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB
		if err != nil {
			http.Error(w, "Failed to read content", http.StatusInternalServerError)
			return
		}
		// File the single note under its own top-level folder named after the import,
		// so the navigation scanner (which only indexes top-level directories) sees it.
		if err := fetch.OrganizeLooseMarkdown(h.store.DataDir(), targetSubPath, filepath.Base(targetSubPath), content); err != nil {
			http.Error(w, "Failed to write content: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := h.store.Reload(); err != nil {
		http.Error(w, "Content updated but failed to update memory index", http.StatusInternalServerError)
		return
	}
	// store.Reload fires the navigation reindex hook, so the folder tree/counts are
	// already rebuilt — no explicit ScanDataDirectories needed here.

	if req.SaveToStartup {
		if err := registry.AddStartupSync(req.URL, targetSubPath); err != nil {
			http.Error(w, "Synced successfully, but database save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	// Content is filed under its own top-level folder (data/<source>/); targetSubPath
	// names that folder and keys the sync ledger.
	w.Write([]byte("Successfully synced and organized under \"" + targetSubPath + "\""))
}

func (h *handler) adminConfig(w http.ResponseWriter, r *http.Request) {
	list, err := registry.ListStartupSync()
	if err != nil {
		http.Error(w, "Failed to read startup sync list", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (h *handler) adminRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Filename         string `json:"filename"`
		DeleteLocalFiles bool   `json:"deleteLocalFiles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Capture this source's URL before we forget the entry, so we can also delete
	// its out-of-data git cache when removing local files.
	var srcURL string
	if entries, err := registry.ListStartupSync(); err == nil {
		for _, e := range entries {
			if e.Filename == req.Filename {
				srcURL = e.URL
				break
			}
		}
	}

	if err := registry.RemoveStartupSync(req.Filename); err != nil {
		http.Error(w, "Failed to remove from database", http.StatusInternalServerError)
		return
	}

	if req.DeleteLocalFiles {
		safeName := filepath.Clean(req.Filename)
		if safeName != "." && safeName != "" && !strings.HasPrefix(safeName, "..") && !filepath.IsAbs(safeName) {
			// Delete exactly the files this source wrote (recorded in its ledger),
			// never the whole shared taxonomy bucket, and drop its git cache — both
			// the relocated out-of-data cache (keyed by URL) and any legacy in-data one.
			fetch.RemoveBySource(h.store.DataDir(), safeName)
			if srcURL != "" {
				if p := fetch.GitCachePath(srcURL); p != "" {
					os.RemoveAll(p)
				}
			}
			os.RemoveAll(filepath.Join(h.store.DataDir(), "."+safeName+".gitsrc"))
		}
	}

	h.store.Reload()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Successfully untracked " + req.Filename))
}
