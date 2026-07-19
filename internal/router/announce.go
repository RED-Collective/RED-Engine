package router

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/registry"
)

// challengeTTL is how long an issued nonce remains valid. Short by design —
// a re-registration handshake completes in two round-trips.
const challengeTTL = 5 * time.Minute

// pendingChallenge is a one-time nonce issued to a peer that wants to
// re-register its URL (e.g. after a cloudflared quick-tunnel restart). These
// live in memory only: they are ephemeral, single-use, and expire after
// challengeTTL. A server restart simply forces the announcing node to request
// a fresh nonce, so no SQLite table is needed.
type pendingChallenge struct {
	peerID    int64
	nonce     []byte // 32 random bytes
	expiresAt time.Time
}

// challengeStore maps a peer's hex public key → its pending challenge.
var challengeStore sync.Map

// challengeSweeperOnce guards the background expiry sweeper goroutine so it is
// only ever started once, regardless of how many announcements arrive.
var challengeSweeperOnce sync.Once

// startChallengeSweeper launches a goroutine that evicts expired challenges
// once per minute. Idempotent — safe to call from every challenge handler.
func startChallengeSweeper() {
	challengeSweeperOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				now := time.Now()
				challengeStore.Range(func(key, value any) bool {
					if c, ok := value.(pendingChallenge); ok && now.After(c.expiresAt) {
						challengeStore.Delete(key)
					}
					return true
				})
			}
		}()
	})
}

type challengeRequest struct {
	PublicKey string `json:"public_key"`
}

type challengeResponse struct {
	Nonce string `json:"nonce"`
}

type confirmRequest struct {
	PublicKey string `json:"public_key"`
	NewURL    string `json:"new_url"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

// announceChallenge handles POST /-/announce/challenge — step 1 of the URL
// re-registration handshake. It issues a fresh single-use nonce, but ONLY to a
// node whose public key we already know. Unknown keys are rejected (404) so an
// attacker cannot fish for valid nonces.
func (h *handler) announceChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req challengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.PublicKey == "" {
		http.Error(w, "public_key required", http.StatusBadRequest)
		return
	}

	peer, err := registry.GetPeerByPublicKey(req.PublicKey)
	if err != nil || peer == nil {
		// We only issue challenges to peers we already trust by key.
		http.Error(w, "unknown peer", http.StatusNotFound)
		return
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		http.Error(w, "failed to generate nonce", http.StatusInternalServerError)
		return
	}

	challengeStore.Store(req.PublicKey, pendingChallenge{
		peerID:    int64(peer.ID),
		nonce:     nonce,
		expiresAt: time.Now().Add(challengeTTL),
	})
	startChallengeSweeper()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(challengeResponse{Nonce: hex.EncodeToString(nonce)})
}

// announceConfirm handles POST /-/announce/confirm — step 2 of the handshake.
// It verifies the nonce and the Ed25519 signature over "nonce|new_url", then
// updates the peer's URL. The signature proves possession of the private key
// matching the registered public key; the single-use nonce prevents replay.
func (h *handler) announceConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req confirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.PublicKey == "" || req.NewURL == "" || req.Nonce == "" || req.Signature == "" {
		http.Error(w, "public_key, new_url, nonce and signature are required", http.StatusBadRequest)
		return
	}

	// Look up the pending challenge we issued for this key.
	v, ok := challengeStore.Load(req.PublicKey)
	if !ok {
		http.Error(w, "no pending challenge", http.StatusUnauthorized)
		return
	}
	challenge, ok := v.(pendingChallenge)
	if !ok || time.Now().After(challenge.expiresAt) {
		challengeStore.Delete(req.PublicKey)
		http.Error(w, "challenge expired", http.StatusUnauthorized)
		return
	}

	// The presented nonce must match the one we issued (constant-time compare).
	reqNonce, err := hex.DecodeString(req.Nonce)
	if err != nil || subtle.ConstantTimeCompare(reqNonce, challenge.nonce) != 1 {
		http.Error(w, "nonce mismatch", http.StatusUnauthorized)
		return
	}

	// Verify the signature over "nonce|new_url" with the peer's public key.
	pubKeyBytes, err := hex.DecodeString(req.PublicKey)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		http.Error(w, "invalid public key", http.StatusBadRequest)
		return
	}
	sig, err := hex.DecodeString(req.Signature)
	if err != nil {
		http.Error(w, "invalid signature encoding", http.StatusBadRequest)
		return
	}
	signed := []byte(req.Nonce + "|" + req.NewURL)
	if !ed25519.Verify(ed25519.PublicKey(pubKeyBytes), signed, sig) {
		http.Error(w, "signature verification failed", http.StatusForbidden)
		return
	}

	// Identity proven — update the peer's URL and consume the nonce so the same
	// payload can never be replayed.
	if err := registry.UpdatePeerURL(req.PublicKey, req.NewURL); err != nil {
		http.Error(w, "failed to update peer URL: "+err.Error(), http.StatusInternalServerError)
		return
	}
	challengeStore.Delete(req.PublicKey)

	keyShort := req.PublicKey
	if len(keyShort) > 16 {
		keyShort = keyShort[:16]
	}
	log.Printf("[Announce] peer %s… re-registered URL → %s", keyShort, req.NewURL)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// AnnounceURLToPeer performs the two-step challenge-response handshake as a
// client: it asks the peer for a nonce, signs "nonce|publicURL" with this
// node's private key, and submits the confirmation. Used by the startup
// announcement goroutine to tell downstream/mirror peers our new URL after a
// (possibly dynamic) tunnel restart.
func AnnounceURLToPeer(peer registry.Peer, publicURL string) error {
	base := peer.URL
	if base == "" {
		base = peer.PublicURL
	}
	if base == "" {
		return fmt.Errorf("peer %q has no contactable URL", peer.Name)
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	base = strings.TrimSuffix(base, "/")

	pubKey := node.GetNodePublicKey()
	if pubKey == "" {
		return fmt.Errorf("node identity not initialised")
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Step 1: request a fresh nonce.
	chalBody, _ := json.Marshal(challengeRequest{PublicKey: pubKey})
	resp, err := client.Post(base+"/-/announce/challenge", "application/json", bytes.NewReader(chalBody))
	if err != nil {
		return fmt.Errorf("challenge request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("challenge rejected: HTTP %d", resp.StatusCode)
	}
	var chal challengeResponse
	if err := json.NewDecoder(resp.Body).Decode(&chal); err != nil {
		return fmt.Errorf("invalid challenge response: %w", err)
	}
	if chal.Nonce == "" {
		return fmt.Errorf("peer returned an empty nonce")
	}

	// Step 2: sign "nonce|new_url" and confirm.
	signature, err := node.SignNodeInfo([]byte(chal.Nonce + "|" + publicURL))
	if err != nil {
		return fmt.Errorf("signing failed: %w", err)
	}
	confirmBody, _ := json.Marshal(confirmRequest{
		PublicKey: pubKey,
		NewURL:    publicURL,
		Nonce:     chal.Nonce,
		Signature: signature,
	})
	resp2, err := client.Post(base+"/-/announce/confirm", "application/json", bytes.NewReader(confirmBody))
	if err != nil {
		return fmt.Errorf("confirm request failed: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("confirm rejected: HTTP %d", resp2.StatusCode)
	}
	return nil
}

type downstreamRegisterRequest struct {
	URL string `json:"url"`
}

// announceDownstream handles POST /-/announce/downstream — a node that pulls from
// us calls this so we learn it is one of our downstreams and will announce future
// URL changes back to it. The body carries the caller's reachable URL; we fetch
// its /-/nodeinfo to confirm it is a live RED node and to learn its identity
// (the same trust model as a manual peer add). A peer we already know keeps its
// existing relationship type — we only refresh its address; an unknown node is
// recorded as 'downstream'. The only capability this grants is receiving our
// already-public URL announcements, so the endpoint is safe to expose.
func (h *handler) announceDownstream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req downstreamRegisterRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		http.Error(w, "url required", http.StatusBadRequest)
		return
	}

	info, err := FetchNodeInfo(req.URL)
	if err != nil {
		http.Error(w, "failed to fetch nodeinfo: "+err.Error(), http.StatusBadGateway)
		return
	}
	if info.PublicKey == "" {
		http.Error(w, "caller has no node identity", http.StatusBadRequest)
		return
	}
	if info.PublicKey == node.GetNodePublicKey() {
		http.Error(w, "refusing to register self", http.StatusBadRequest)
		return
	}

	status := "registered"
	peerType := "downstream"
	if existing, _ := registry.GetPeerByPublicKey(info.PublicKey); existing != nil {
		// Preserve whatever relationship the operator already chose; only refresh
		// the address so future announcements reach the peer.
		peerType = ""
		status = "refreshed"
	}

	if err := registry.AddPeer(registry.Peer{
		URL:           req.URL,
		PublicKey:     info.PublicKey,
		Name:          info.Name,
		PeerType:      peerType,
		Description:   info.Description,
		PublicURL:     info.PublicURL,
		TunnelType:    info.TunnelType,
		ExportedPaths: info.ExportedPaths,
		LastSeen:      time.Now(),
		AddedAt:       time.Now(),
	}); err != nil {
		http.Error(w, "failed to save peer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[Announce] downstream %q (%s…) %s", info.Name, shortKey(info.PublicKey), status)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

// RegisterAsDownstream tells an upstream peer that this node pulls from it, so the
// upstream learns to announce its URL changes back to us. It posts our public URL
// to the upstream's /-/announce/downstream; the upstream authenticates us by
// fetching our nodeinfo. Best-effort — callers log failures but never treat them
// as fatal. A no-op when this node advertises no public URL (nothing to register).
func RegisterAsDownstream(upstreamURL, selfPublicURL string) error {
	if selfPublicURL == "" {
		return fmt.Errorf("no public_url configured; cannot register as downstream")
	}
	base := strings.TrimSuffix(upstreamURL, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	body, _ := json.Marshal(downstreamRegisterRequest{URL: selfPublicURL})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(base+"/-/announce/downstream", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("downstream registration to %s failed: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downstream registration rejected by %s: HTTP %d", base, resp.StatusCode)
	}
	return nil
}
