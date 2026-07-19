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
	"time"

	"github.com/RED-Collective/red-engine/internal/fetch"
	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/registry"
)

// peerResolveContext domain-separates the third-party URL-directory proof so a
// node-key signature produced here can never be replayed against another protocol
// that also signs with the node key (the announce handshake signs "nonce|new_url";
// the sync proof signs "red-sync-verify-v1|nonce").
const peerResolveContext = "red-peer-resolve-v1|"

// peerResolveRequest is what a requestor (e.g. node A) sends to a directory peer
// (e.g. node C) to learn the CURRENT url of a third node (e.g. node B) it can no
// longer reach. The signature proves the requestor controls RequestorKey and binds
// the lookup to a fresh nonce + target so the request cannot be replayed or have
// its target swapped.
//
//	signature = Sign_requestor( peerResolveContext + nonce + "|" + target_key )
type peerResolveRequest struct {
	RequestorKey string `json:"requestor_key"`
	TargetKey    string `json:"target_key"`
	Nonce        string `json:"nonce"`
	Signature    string `json:"signature"`
}

// peerResolveResponse is the directory's signed answer. The signature is over the
// requestor's nonce + target + returned url, so the answer is bound to THIS request
// (anti-replay) and to the exact (target, url) pair (anti-tamper). The requestor
// verifies it against the directory's PINNED public key — never the key the body
// claims — so a MITM that strips or forges the signature is rejected.
//
//	signature = Sign_directory( peerResolveContext + nonce + "|" + target_key + "|" + url )
type peerResolveResponse struct {
	TargetKey string `json:"target_key"`
	URL       string `json:"url"`
	PublicKey string `json:"public_key"` // the directory's own key, for cross-checking
	Signature string `json:"signature"`
}

// peerResolve handles POST /-/peer/resolve — the directory side of third-party URL
// rediscovery. After a dual restart where two peers BOTH moved to new tunnel URLs,
// neither can re-announce directly (each holds the other's dead address), but both
// can still reach a third node that stayed put. That third node answers here.
//
// It discloses a peer's url ONLY to a requestor that is itself a known peer and that
// proves control of its key. An unknown or unproven requestor gets a 4xx and learns
// nothing, so this endpoint cannot be used to enumerate the directory's peers.
func (h *handler) peerResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req peerResolveRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.RequestorKey == "" || req.TargetKey == "" || req.Nonce == "" || req.Signature == "" {
		http.Error(w, "requestor_key, target_key, nonce and signature are required", http.StatusBadRequest)
		return
	}
	// Bound the nonce so this is never abused as a general signing/verification oracle.
	if l := len(req.Nonce); l < 32 || l > 256 {
		http.Error(w, "invalid nonce", http.StatusBadRequest)
		return
	}

	// 1. Authorize: we only answer requestors we already trust by key. Unknown keys
	// are rejected before anything is disclosed (no peer enumeration).
	requestor, err := registry.GetPeerByPublicKey(req.RequestorKey)
	if err != nil || requestor == nil {
		log.Printf("[Resolve] DENY unknown requestor %s… (nonce %s…)", shortKey(req.RequestorKey), shortKey(req.Nonce))
		http.Error(w, "unknown peer", http.StatusNotFound)
		return
	}

	// 2. Authenticate: the requestor must prove it controls RequestorKey by signing
	// the nonce + target. This stops anyone from fishing under another node's key.
	reqPub, err := hex.DecodeString(req.RequestorKey)
	if err != nil || len(reqPub) != ed25519.PublicKeySize {
		http.Error(w, "invalid requestor key", http.StatusBadRequest)
		return
	}
	sig, err := hex.DecodeString(req.Signature)
	if err != nil {
		http.Error(w, "invalid signature encoding", http.StatusBadRequest)
		return
	}
	signed := []byte(peerResolveContext + req.Nonce + "|" + req.TargetKey)
	if !ed25519.Verify(ed25519.PublicKey(reqPub), signed, sig) {
		log.Printf("[Resolve] DENY bad signature from requestor %s…", shortKey(req.RequestorKey))
		http.Error(w, "signature verification failed", http.StatusForbidden)
		return
	}

	// 3. Look up the target. An unknown target, or one with no contactable address,
	// yields 404 — same response shape as an unknown requestor, disclosing nothing.
	target, err := registry.GetPeerByPublicKey(req.TargetKey)
	if err != nil || target == nil {
		log.Printf("[Resolve] requestor %s… asked for unknown target %s…", shortKey(req.RequestorKey), shortKey(req.TargetKey))
		http.Error(w, "unknown target", http.StatusNotFound)
		return
	}
	url := target.URL
	if url == "" {
		url = target.PublicURL
	}
	if url == "" {
		http.Error(w, "target has no contactable url", http.StatusNotFound)
		return
	}

	// 4. Sign the answer over nonce|target|url so the requestor can prove it came
	// from us, unmodified, in response to THIS nonce.
	respSig, err := node.SignNodeInfo([]byte(peerResolveContext + req.Nonce + "|" + req.TargetKey + "|" + url))
	if err != nil {
		http.Error(w, "node identity unavailable", http.StatusInternalServerError)
		return
	}

	// Audit log: who asked for whom, with what nonce, and that we answered (signed).
	log.Printf("[Resolve] ANSWER requestor=%s… target=%s… url=%s nonce=%s… sig=%s…",
		shortKey(req.RequestorKey), shortKey(req.TargetKey), url, shortKey(req.Nonce), shortKey(respSig))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peerResolveResponse{
		TargetKey: req.TargetKey,
		URL:       url,
		PublicKey: node.GetNodePublicKey(),
		Signature: respSig,
	})
}

// ResolvePeerURL asks a directory peer for the CURRENT url of targetKey and returns
// it ONLY if the directory's signature verifies against the directory's PINNED key
// (directory.PublicKey). It signs its own request so the directory can authenticate
// us. The returned url is NOT yet trusted for content — the caller must still prove
// the target's identity directly (see authenticatePeerAt); the directory is only a
// hint, never an authority on the target's key.
func ResolvePeerURL(directory registry.Peer, targetKey string) (string, error) {
	base := directory.URL
	if base == "" {
		base = directory.PublicURL
	}
	if base == "" {
		return "", fmt.Errorf("directory peer %q has no contactable url", directory.Name)
	}
	base = strings.TrimSuffix(ensureScheme(base), "/")
	if directory.PublicKey == "" {
		return "", fmt.Errorf("directory peer %q has no pinned key; refusing to trust its answer", directory.Name)
	}

	selfKey := node.GetNodePublicKey()
	if selfKey == "" {
		return "", fmt.Errorf("node identity not initialised")
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	nonceHex := hex.EncodeToString(nonce)

	sig, err := node.SignNodeInfo([]byte(peerResolveContext + nonceHex + "|" + targetKey))
	if err != nil {
		return "", fmt.Errorf("sign request: %w", err)
	}
	body, _ := json.Marshal(peerResolveRequest{
		RequestorKey: selfKey,
		TargetKey:    targetKey,
		Nonce:        nonceHex,
		Signature:    sig,
	})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(base+"/-/peer/resolve", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("resolve request to %s failed: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("directory %s declined resolve: HTTP %d", base, resp.StatusCode)
	}
	var rr peerResolveResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&rr); err != nil {
		return "", fmt.Errorf("invalid resolve response from %s: %w", base, err)
	}
	if rr.URL == "" {
		return "", fmt.Errorf("directory %s returned an empty url", base)
	}

	// Verify the answer against the directory's PINNED key — not the key the body
	// claims. A response that is unsigned, signed by the wrong key, or tampered in
	// transit is discarded here (the directory cannot be impersonated by a MITM).
	pinned := strings.ToLower(strings.TrimSpace(directory.PublicKey))
	if claimed := strings.ToLower(strings.TrimSpace(rr.PublicKey)); claimed != "" && claimed != pinned {
		log.Printf("[Resolve] WARN directory %s answered under key %s… but we pinned %s… — discarding", base, shortKey(claimed), shortKey(pinned))
		return "", fmt.Errorf("directory key mismatch (possible MITM)")
	}
	dirPub, err := hex.DecodeString(pinned)
	if err != nil || len(dirPub) != ed25519.PublicKeySize {
		return "", fmt.Errorf("directory %q has an invalid pinned key", directory.Name)
	}
	rsig, err := hex.DecodeString(rr.Signature)
	if err != nil {
		return "", fmt.Errorf("directory %s returned an invalid signature encoding", base)
	}
	want := []byte(peerResolveContext + nonceHex + "|" + targetKey + "|" + rr.URL)
	if !ed25519.Verify(ed25519.PublicKey(dirPub), want, rsig) {
		log.Printf("[Resolve] WARN directory %s returned an unverifiable signature — discarding answer", base)
		return "", fmt.Errorf("directory %s failed the signature check (spoofed or tampered response)", base)
	}
	return strings.TrimSpace(rr.URL), nil
}

// authenticatePeerAt performs the mandatory direct re-authentication after a url is
// learned from a directory: it sends the target a FRESH random nonce at the given
// url and requires an Ed25519 signature that (a) verifies and (b) proves the target
// controls expectedKey — the key we already pinned for that peer. The directory's
// word is never enough; only the target proving its own key over a fresh nonce lets
// us commit the new url. Uses SafeClient so a malicious directory cannot point us at
// an internal address (SSRF). Mirrors the proof in verifyPeerIdentity but checks
// against a known expected key instead of trust-on-first-use pinning.
func authenticatePeerAt(targetURL, expectedKey string) error {
	targetURL = strings.TrimSuffix(ensureScheme(targetURL), "/")
	expected := strings.ToLower(strings.TrimSpace(expectedKey))
	if expected == "" {
		return fmt.Errorf("no expected key to authenticate against")
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	nonceHex := hex.EncodeToString(nonce)

	body, _ := json.Marshal(syncVerifyRequest{Nonce: nonceHex})
	resp, err := fetch.SafeClient().Post(targetURL+"/-/sync/verify", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("identity challenge to %s failed: %w", targetURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer at %s does not support identity verification (HTTP %d)", targetURL, resp.StatusCode)
	}
	var vr syncVerifyResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&vr); err != nil {
		return fmt.Errorf("invalid verify response from %s: %w", targetURL, err)
	}

	got := strings.ToLower(strings.TrimSpace(vr.PublicKey))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(got)) != 1 {
		return fmt.Errorf("identity mismatch at %s: expected %s… but it proved %s… (directory gave a wrong/forged url)",
			targetURL, shortKey(expected), shortKey(got))
	}
	pub, err := hex.DecodeString(got)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("peer at %s returned an invalid public key", targetURL)
	}
	sig, err := hex.DecodeString(vr.Signature)
	if err != nil {
		return fmt.Errorf("peer at %s returned an invalid signature encoding", targetURL)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(syncVerifyContext+nonceHex), sig) {
		return fmt.Errorf("peer at %s failed the signature challenge", targetURL)
	}
	return nil
}

// peerReachable reports whether a peer answers a quick health probe at its stored
// url. Used to decide which peers need rediscovery and which can serve as live
// directories. A plain client (not SafeClient) is fine: this only reads a fixed
// /-/health path and acts on a boolean.
func peerReachable(rawURL string) bool {
	base := strings.TrimSuffix(ensureScheme(rawURL), "/")
	if base == "" {
		return false
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(base + "/-/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// RediscoverStalePeers heals federation links after a dual restart. For every known
// peer we can no longer reach, it asks each OTHER reachable peer (acting as a signed
// directory) for that peer's current url, verifies the directory's signature against
// its pinned key, then RE-AUTHENTICATES the target directly with a fresh nonce
// before committing the new url. A peer found this way needs no manual
// reconfiguration; the existing announce + peer-sync loops then resume over the
// healed address. Called from the federation heartbeat at boot and on each tick.
func RediscoverStalePeers() {
	peers, err := registry.ListPeers()
	if err != nil {
		log.Printf("[Resolve] list peers: %v", err)
		return
	}

	// Partition into reachable directories and stale targets in one pass.
	reachable := make(map[string]bool, len(peers))
	for _, p := range peers {
		if p.PublicKey == "" {
			continue
		}
		target := p.URL
		if target == "" {
			target = p.PublicURL
		}
		reachable[p.PublicKey] = target != "" && peerReachable(target)
	}

	for _, stale := range peers {
		if stale.PublicKey == "" || reachable[stale.PublicKey] {
			continue // unknown identity or already reachable — nothing to heal
		}
		healed := false
		for _, dir := range peers {
			if dir.PublicKey == "" || dir.PublicKey == stale.PublicKey || !reachable[dir.PublicKey] {
				continue // skip the target itself and any directory we can't reach
			}
			url, err := ResolvePeerURL(dir, stale.PublicKey)
			if err != nil {
				log.Printf("[Resolve] %s… via directory %q: %v", shortKey(stale.PublicKey), dir.Name, err)
				continue
			}
			if url == "" || strings.TrimSuffix(url, "/") == strings.TrimSuffix(stale.URL, "/") {
				continue // directory has nothing newer than what we already hold
			}
			// MANDATORY direct re-auth — never trust the directory's word alone.
			if err := authenticatePeerAt(url, stale.PublicKey); err != nil {
				log.Printf("[Resolve] rediscovered url %s for %s… via %q FAILED direct auth: %v",
					url, shortKey(stale.PublicKey), dir.Name, err)
				continue
			}
			if err := registry.UpdatePeerURL(stale.PublicKey, url); err != nil {
				log.Printf("[Resolve] persist new url for %s…: %v", shortKey(stale.PublicKey), err)
				continue
			}
			log.Printf("[Resolve] HEALED %q (%s…): %s → %s via directory %q (signed + re-authenticated)",
				stale.Name, shortKey(stale.PublicKey), stale.URL, url, dir.Name)
			reachable[stale.PublicKey] = true
			healed = true
			break
		}
		if !healed {
			log.Printf("[Resolve] could not rediscover %q (%s…) via any directory", stale.Name, shortKey(stale.PublicKey))
		}
	}
}
