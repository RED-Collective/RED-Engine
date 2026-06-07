package router

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/fetch"
	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/registry"
)

// contentWithManifest serves the static content tree, but synthesizes
// "<path>/manifest.json" on the fly from the organized bucket so peers can sync.
// Every other request falls through to the static file server.
func (h *handler) contentWithManifest(static http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/content/")
		if rel == "manifest.json" || strings.HasSuffix(rel, "/manifest.json") {
			prefix := strings.Trim(strings.TrimSuffix(rel, "manifest.json"), "/")
			m, err := fetch.GenerateManifest(h.store.DataDir(), prefix)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			if err := json.NewEncoder(w).Encode(m); err != nil {
				log.Printf("[Peer] encode manifest: %v", err)
			}
			return
		}
		static.ServeHTTP(w, r)
	})
}

// peerManifest is the puller's view of a remote ContentManifest.
type peerManifest struct {
	Bucket string `json:"bucket"`
	Files  map[string]struct {
		FileHash  string `json:"file_hash"`
		PublicKey string `json:"public_key"`
		Signature string `json:"signature"`
	} `json:"files"`
}

// pullFromPeer downloads a peer's content under remotePath, mirrors each note into
// data/<bucket>/ verbatim (preserving its path), and persists the signatures so the
// notes verify and can be re-exported. dataDir is the local content root; "bucket"
// here is simply the top-level content folder named in the manifest (there is no
// library/manual taxonomy anymore). source keys this peer's sync ledger so a later
// pull cleans up notes it no longer provides.
func (h *handler) pullFromPeer(peerURL, remotePath, dataDir, source string) error {
	peerURL = strings.TrimSuffix(peerURL, "/")
	remotePath = strings.Trim(remotePath, "/")

	// 0. Authenticate the source node before trusting any of its content. On the
	// first sync this pins the peer's key (TOFU); every later sync must match it.
	// A failure aborts the sync — we never download from an unverified peer.
	if err := verifyPeerIdentity(peerURL); err != nil {
		return fmt.Errorf("peer verification failed: %w", err)
	}

	// 1. Fetch the manifest for the requested path.
	manifestURL := peerURL + "/content/" + remotePath + "/manifest.json"
	resp, err := fetch.SafeClient().Get(manifestURL)
	if err != nil {
		return fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("manifest not found at %s (HTTP %d)", manifestURL, resp.StatusCode)
	}
	var manifest peerManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 50*1024*1024)).Decode(&manifest); err != nil {
		return fmt.Errorf("invalid manifest JSON: %w", err)
	}

	bucket := manifest.Bucket
	if bucket == "" {
		bucket = strings.SplitN(remotePath, "/", 2)[0]
	}
	if bucket == "" || strings.Contains(bucket, "..") {
		return fmt.Errorf("manifest has no usable bucket")
	}

	// 2. Download every file, verify its hash, and mirror it into data/<bucket>/.
	var signerFiles []fetch.SignerFile
	var written []string
	for relPath, meta := range manifest.Files {
		clean := filepath.ToSlash(filepath.Clean(relPath))
		if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
			log.Printf("[Peer] skipping unsafe path %q", relPath)
			continue
		}

		fileURL := peerURL + "/content/" + bucket + "/" + clean
		fr, err := fetch.SafeClient().Get(fileURL)
		if err != nil {
			return fmt.Errorf("download %s: %w", clean, err)
		}
		if fr.StatusCode != http.StatusOK {
			fr.Body.Close()
			return fmt.Errorf("file %s returned HTTP %d", clean, fr.StatusCode)
		}
		content, err := io.ReadAll(io.LimitReader(fr.Body, 100*1024*1024))
		fr.Body.Close()
		if err != nil {
			return fmt.Errorf("read %s: %w", clean, err)
		}

		if meta.FileHash != "" {
			sum := sha256.Sum256(content)
			if hex.EncodeToString(sum[:]) != meta.FileHash {
				log.Printf("[Peer] hash mismatch for %q, skipping", clean)
				continue
			}
		}

		// Mirror the note at its manifest path, verbatim.
		w, err := fetch.WriteNote(dataDir, bucket, clean, content)
		if err != nil {
			log.Printf("[Peer] write %q: %v", clean, err)
			continue
		}
		written = append(written, w)
		signerFiles = append(signerFiles, fetch.SignerFile{
			Path:      clean,
			FileHash:  meta.FileHash,
			PublicKey: meta.PublicKey,
			Signature: meta.Signature,
		})
	}

	// Clean up notes this peer source no longer provides, then record the new set.
	fetch.ReconcileLedger(dataDir, source, written)

	// 3. Persist signatures so the notes verify and can be re-exported.
	key := strings.TrimPrefix(strings.TrimPrefix(peerURL, "https://"), "http://")
	if err := fetch.WriteLocalSignerDB(filepath.Join(dataDir, bucket), key, signerFiles); err != nil {
		log.Printf("[Peer] persist signatures: %v", err)
	}
	return nil
}

// syncVerifyContext domain-separates the sync identity proof so a node-key
// signature produced here can never be replayed against another protocol that
// also signs with the node key (e.g. the announce handshake's "nonce|new_url").
const syncVerifyContext = "red-sync-verify-v1|"

type syncVerifyRequest struct {
	Nonce string `json:"nonce"`
}

type syncVerifyResponse struct {
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

// syncVerify handles POST /-/sync/verify — a puller sends a fresh random nonce and
// we return this node's public key plus an Ed25519 signature over
// "red-sync-verify-v1|<nonce>", proving we hold the private key behind our
// advertised identity. It is stateless: the puller generated the nonce and
// verifies the response itself, so no server-side nonce store is needed. Public on
// purpose — it only signs a domain-separated random value, never attacker-chosen
// structured data.
func (h *handler) syncVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req syncVerifyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	// A real nonce is 32 bytes hex (64 chars); bound it so this is never abused as
	// a general signing oracle for large payloads.
	if l := len(req.Nonce); l < 32 || l > 256 {
		http.Error(w, "invalid nonce", http.StatusBadRequest)
		return
	}
	sig, err := node.SignNodeInfo([]byte(syncVerifyContext + req.Nonce))
	if err != nil {
		http.Error(w, "node identity unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(syncVerifyResponse{
		PublicKey: node.GetNodePublicKey(),
		Signature: sig,
	})
}

// verifyPeerIdentity proves the live node at peerURL controls the private key
// behind its advertised public key before we trust any of its content. It sends a
// fresh random nonce to the peer's /-/sync/verify, checks the Ed25519 signature,
// then pins the key: on the first sync the verified key is recorded (trust on first
// use); on every later sync it must match the pinned key or the sync is aborted
// (possible MITM or key rotation). Every failure is fatal — callers must not
// download on error. Note this uses fetch.SafeClient(), so a loopback/LAN peer
// still requires RED_ALLOW_PRIVATE_SYNC=true, exactly like the content download.
func verifyPeerIdentity(peerURL string) error {
	peerURL = strings.TrimSuffix(peerURL, "/")

	peer, _ := registry.GetPeerByURL(peerURL)
	if peer == nil {
		// Tolerate a stored URL that kept its trailing slash.
		peer, _ = registry.GetPeerByURL(peerURL + "/")
	}
	if peer == nil {
		return fmt.Errorf("peer %s is not registered; add it before syncing", peerURL)
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	nonceHex := hex.EncodeToString(nonce)

	body, _ := json.Marshal(syncVerifyRequest{Nonce: nonceHex})
	resp, err := fetch.SafeClient().Post(peerURL+"/-/sync/verify", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("identity challenge to %s failed: %w", peerURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer %s does not support identity verification (HTTP %d)", peerURL, resp.StatusCode)
	}
	var vr syncVerifyResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&vr); err != nil {
		return fmt.Errorf("invalid verify response from %s: %w", peerURL, err)
	}

	pub, err := hex.DecodeString(vr.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("peer %s returned an invalid public key", peerURL)
	}
	sig, err := hex.DecodeString(vr.Signature)
	if err != nil {
		return fmt.Errorf("peer %s returned an invalid signature encoding", peerURL)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(syncVerifyContext+nonceHex), sig) {
		return fmt.Errorf("peer %s failed the signature challenge", peerURL)
	}

	// Trust on first use, then enforce. Keys are public, but a constant-time
	// compare keeps the check uniform.
	pinned := strings.ToLower(strings.TrimSpace(peer.PublicKey))
	got := strings.ToLower(vr.PublicKey)
	if pinned == "" {
		if err := registry.AddPeer(registry.Peer{URL: peer.URL, PublicKey: vr.PublicKey, LastSeen: time.Now()}); err != nil {
			return fmt.Errorf("pin peer key: %w", err)
		}
		log.Printf("[Sync] pinned identity for peer %s (key %s…)", peerURL, shortKey(got))
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(pinned), []byte(got)) != 1 {
		return fmt.Errorf("peer %s identity mismatch: expected %s… but it proved %s… (possible MITM or key rotation)",
			peerURL, shortKey(pinned), shortKey(got))
	}
	return nil
}
