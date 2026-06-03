package router

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// pullFromPeer downloads all files under remotePath from a peer node
func (h *handler) pullFromPeer(peerURL, remotePath, destDir string) error {
	// Normalise URLs
	peerURL = strings.TrimSuffix(peerURL, "/")
	remotePath = strings.TrimPrefix(remotePath, "/")

	// 1. Fetch manifest.json from peer
	manifestURL := peerURL + "/content/" + remotePath + "/manifest.json"
	resp, err := http.Get(manifestURL)
	if err != nil {
		return fmt.Errorf("failed to fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("manifest not found at %s (HTTP %d)", manifestURL, resp.StatusCode)
	}

	// 2. Parse manifest (we need a minimal struct – reuse later)
	var manifest struct {
		Branch    string `json:"branch"`
		SubBranch string `json:"sub_branch"`
		Files     map[string]struct {
			FileHash  string `json:"file_hash"`
			PublicKey string `json:"public_key"`
			Signature string `json:"signature"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return fmt.Errorf("invalid manifest JSON: %w", err)
	}

	// 3. Create local destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("cannot create dest dir: %w", err)
	}

	// 4. Download every file listed in the manifest
	for relPath := range manifest.Files {
		// Guard against path traversal: a malicious or buggy manifest could
		// list relPath values like "../../etc/cron.d/x" that escape destDir.
		clean := filepath.ToSlash(filepath.Clean(relPath))
		if clean == "" || clean == "." || clean == ".." ||
			strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
			log.Printf("pullFromPeer: skipping unsafe path %q", relPath)
			continue
		}

		fileURL := peerURL + "/content/" + remotePath + "/" + relPath
		fileResp, err := http.Get(fileURL)
		if err != nil {
			return fmt.Errorf("failed to download %s: %w", relPath, err)
		}
		defer fileResp.Body.Close()
		if fileResp.StatusCode != http.StatusOK {
			return fmt.Errorf("file %s returned HTTP %d", relPath, fileResp.StatusCode)
		}

		localPath := filepath.Join(destDir, clean)
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			return err
		}
		out, err := os.Create(localPath)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, fileResp.Body)
		out.Close()
		if err != nil {
			return err
		}
		// TODO: optionally verify file hash against manifest
	}
	return nil
}
