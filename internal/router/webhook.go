package router

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/RED-Collective/red-engine/internal/config"
	"github.com/RED-Collective/red-engine/internal/fetch"
)

type githubWebhookPayload struct {
	Repository struct {
		HTMLURL  string `json:"html_url"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

func normalizeURL(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, ".git")
	return u
}

func verifySignature(secret string, payload []byte, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := mac.Sum(nil)
	expectedSignature := "sha256=" + hex.EncodeToString(expectedMAC)

	return hmac.Equal([]byte(signatureHeader), []byte(expectedSignature))
}

func (h *handler) webhookSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	if h.cfg.WebhookSecret == "" {
		log.Println("🚨 Webhook blocked: WebhookSecret is not configured. Webhooks are disabled for security.")
		http.Error(w, "Webhooks disabled: no secret configured", http.StatusForbidden)
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	if !verifySignature(h.cfg.WebhookSecret, bodyBytes, sig) {
		log.Println("🚨 Security Alert: Blocked webhook payload with invalid signature!")
		http.Error(w, "Unauthorized: Invalid Signature", http.StatusUnauthorized)
		return
	}

	var payload githubWebhookPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		log.Printf("⚠️ Failed to decode webhook payload: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	incomingURL := payload.Repository.CloneURL
	if incomingURL == "" {
		incomingURL = payload.Repository.HTMLURL
	}
	if incomingURL == "" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ignored: No repository URL"))
		return
	}

	normalizedIncoming := normalizeURL(incomingURL)
	log.Printf("🔄 Webhook verified for repository: %s", normalizedIncoming)

	go func() {
		h.store.BeginRemoteSync()
		defer h.store.EndRemoteSync()

		successCount := 0
		h.cfg.Mu.RLock()
		syncList := make([]config.RemoteSync, len(h.cfg.StartupSync))
		copy(syncList, h.cfg.StartupSync)
		h.cfg.Mu.RUnlock()
		for _, sync := range syncList {
			normalizedTarget := normalizeURL(sync.URL)
			if !strings.HasPrefix(normalizedTarget, normalizedIncoming) {
				continue
			}

			srcType := "raw"
			if strings.HasSuffix(strings.ToLower(sync.URL), ".git") {
				srcType = "git"
			} else if strings.HasSuffix(strings.ToLower(sync.URL), ".tar.gz") {
				srcType = "tar.gz"
			} else if strings.HasSuffix(strings.ToLower(sync.URL), ".zip") {
				srcType = "zip"
			}

			destDir := filepath.Join(h.store.DataDir(), filepath.Base(filepath.Clean(sync.Filename)))
			log.Printf("📥 Webhook triggering delta pull for: %s", sync.Filename)

			changedFiles, err := fetch.PullDelta(sync.URL, srcType, destDir)
			if err != nil {
				log.Printf("⚠️ Failed to sync %s: %v", sync.Filename, err)
			} else {
				successCount++

				if changedFiles == nil {
					h.store.Reload()
				} else if len(changedFiles) > 0 {
					log.Printf("⚡ Hot-Patching %d modified files...", len(changedFiles))
					if err := h.store.UpdateFiles(changedFiles); err != nil {
						h.store.Reload()
					}
				}
			}
		}

		if successCount == 0 {
			log.Println("⚠️ Webhook finished, but no matching tracked repositories were found.")
		}
	}()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Sync process initiated securely"))
}
