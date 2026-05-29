package router

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/config"
	"github.com/RED-Collective/red-engine/internal/fetch"
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
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	parsedURL, err := url.Parse(req.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		http.Error(w, "Invalid URL scheme", http.StatusBadRequest)
		return
	}

	// --- SMART GITHUB URL REWRITER ---
	if parsedURL.Host == "github.com" {
		pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
		if len(pathParts) == 2 {
			req.URL = "https://github.com/" + pathParts[0] + "/" + pathParts[1] + "/archive/HEAD.zip"
			parsedURL, _ = url.Parse(req.URL)
		} else if len(pathParts) > 2 && pathParts[2] == "blob" {
			req.URL = "https://raw.githubusercontent.com/" + pathParts[0] + "/" + pathParts[1] + "/" + strings.Join(pathParts[3:], "/")
			parsedURL, _ = url.Parse(req.URL)
		}
	}
	// --------------------------------------

	targetSubPath := filepath.Clean(req.Filename)

	if targetSubPath == "." || targetSubPath == "" {
		pathParts := strings.Split(strings.TrimRight(parsedURL.Path, "/"), "/")
		if len(pathParts) > 0 {
			if parsedURL.Host == "github.com" && len(pathParts) >= 3 && pathParts[3] == "archive" {
				targetSubPath = pathParts[2]
			} else {
				lastPart := pathParts[len(pathParts)-1]
				lastPart = strings.TrimSuffix(lastPart, ".zip")
				lastPart = strings.TrimSuffix(lastPart, ".tar.gz")
				lastPart = strings.TrimSuffix(lastPart, ".tgz")
				lastPart = strings.TrimSuffix(lastPart, ".md")
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
		if err := os.MkdirAll(filepath.Dir(destinationDir), 0755); err != nil {
			http.Error(w, "Failed to create directory structure", http.StatusInternalServerError)
			return
		}

		if !strings.HasSuffix(strings.ToLower(destinationDir), ".md") {
			destinationDir += ".md"
			targetSubPath += ".md"
		}

		httpReq, err := http.NewRequest(http.MethodGet, req.URL, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		httpReq.Header.Set("User-Agent", "RED-Engine-Sync/1.0")

		resp, err := fetch.SafeClient().Do(httpReq)
		if err != nil {
			http.Error(w, "Failed to connect to remote server", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			http.Error(w, "Remote server returned non-OK status", http.StatusBadGateway)
			return
		}

		outFile, err := os.Create(destinationDir)
		if err != nil {
			http.Error(w, "Failed to create file on disk", http.StatusInternalServerError)
			return
		}
		defer outFile.Close()

		if _, err := io.Copy(outFile, io.LimitReader(resp.Body, 10*1024*1024)); err != nil {
			http.Error(w, "Failed to write content", http.StatusInternalServerError)
			return
		}
	}

	if err := h.store.Reload(); err != nil {
		http.Error(w, "Content updated but failed to update memory index", http.StatusInternalServerError)
		return
	}

	if req.SaveToStartup {
		h.cfg.Mu.Lock()
		var newSync []config.RemoteSync
		for _, sync := range h.cfg.StartupSync {
			if sync.Filename != req.Filename {
				newSync = append(newSync, sync)
			}
		}
		h.cfg.StartupSync = newSync
		h.cfg.Mu.Unlock()

		if err := h.cfg.Save(h.cfgPath); err != nil {
			http.Error(w, "Synced successfully, but config save failed", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Successfully synced to data/" + targetSubPath))
}

func (h *handler) adminConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	h.cfg.Mu.RLock()
	defer h.cfg.Mu.RUnlock()
	json.NewEncoder(w).Encode(h.cfg.StartupSync)
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

	h.cfg.Mu.Lock()
	var newSync []config.RemoteSync
	for _, sync := range h.cfg.StartupSync {
		if sync.Filename != req.Filename {
			newSync = append(newSync, sync)
		}
	}
	h.cfg.StartupSync = newSync
	h.cfg.Mu.Unlock()

	if err := h.cfg.Save(h.cfgPath); err != nil {
		http.Error(w, "Failed to save configuration", http.StatusInternalServerError)
		return
	}

	if req.DeleteLocalFiles {
		safeName := filepath.Clean(req.Filename)
		if safeName != "." && safeName != "" && !strings.HasPrefix(safeName, "..") && !filepath.IsAbs(safeName) {
			fullRemovalPath := filepath.Join(h.store.DataDir(), safeName)
			os.RemoveAll(fullRemovalPath) 
		}
	}

	h.store.Reload()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Successfully untracked " + req.Filename))
}
