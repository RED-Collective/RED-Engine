package router

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/registry"
)

// TestMain wires up the package-global registry DB and this node's identity (which
// plays the role of directory "C" / the server under test) in throwaway temp dirs.
func TestMain(m *testing.M) {
	regDir, err := os.MkdirTemp("", "red-resolve-reg-*")
	if err != nil {
		panic(err)
	}
	idDir, err := os.MkdirTemp("", "red-resolve-id-*")
	if err != nil {
		panic(err)
	}
	if err := registry.InitRegistry(regDir); err != nil {
		panic(err)
	}
	if err := node.InitNodeIdentity(idDir); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(regDir)
	os.RemoveAll(idDir)
	os.Exit(code)
}

// genKey returns a fresh ed25519 key pair with the public half hex-encoded.
func genKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	return priv, hex.EncodeToString(pub)
}

func randNonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	return hex.EncodeToString(b)
}

// signResolveRequest builds the JSON body a requestor sends to /-/peer/resolve.
func signResolveRequest(reqKey string, reqPriv ed25519.PrivateKey, targetKey, nonce string) []byte {
	sig := ed25519.Sign(reqPriv, []byte(peerResolveContext+nonce+"|"+targetKey))
	body, _ := json.Marshal(peerResolveRequest{
		RequestorKey: reqKey,
		TargetKey:    targetKey,
		Nonce:        nonce,
		Signature:    hex.EncodeToString(sig),
	})
	return body
}

// addPeer is a tiny helper that registers a peer with a key and url.
func addPeer(t *testing.T, key, url string) {
	t.Helper()
	if err := registry.AddPeer(registry.Peer{
		PublicKey: key,
		URL:       url,
		Name:      "peer-" + shortKey(key),
		PeerType:  "mirror",
		LastSeen:  time.Now(),
		AddedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("add peer: %v", err)
	}
}

// TestPeerResolve_HappyPath: a known, properly-signed requestor gets back the
// target's url with a signature this node (the directory) can be proven to have made.
func TestPeerResolve_HappyPath(t *testing.T) {
	reqPriv, reqKey := genKey(t)
	_, targetKey := genKey(t)
	const targetURL = "https://target-happy.example.trycloudflare.com"
	addPeer(t, reqKey, "https://requestor-happy.example.com")
	addPeer(t, targetKey, targetURL)

	nonce := randNonce(t)
	body := signResolveRequest(reqKey, reqPriv, targetKey, nonce)

	rr := httptest.NewRecorder()
	(&handler{}).peerResolve(rr, httptest.NewRequest(http.MethodPost, "/-/peer/resolve", strings.NewReader(string(body))))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var resp peerResolveResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.URL != targetURL {
		t.Fatalf("url: want %q got %q", targetURL, resp.URL)
	}
	if resp.PublicKey != node.GetNodePublicKey() {
		t.Fatalf("directory key: want %q got %q", node.GetNodePublicKey(), resp.PublicKey)
	}
	// The signature must verify against the directory's (this node's) key, bound to
	// the requestor's nonce + target + url.
	dirPub, _ := hex.DecodeString(resp.PublicKey)
	sig, _ := hex.DecodeString(resp.Signature)
	want := []byte(peerResolveContext + nonce + "|" + targetKey + "|" + targetURL)
	if !ed25519.Verify(ed25519.PublicKey(dirPub), want, sig) {
		t.Fatal("directory signature did not verify against the bound payload")
	}
}

// TestPeerResolve_UnknownRequestor: a key the directory does not know learns nothing.
func TestPeerResolve_UnknownRequestor(t *testing.T) {
	strangerPriv, strangerKey := genKey(t) // never registered
	_, targetKey := genKey(t)
	addPeer(t, targetKey, "https://secret-target.example.com")

	nonce := randNonce(t)
	body := signResolveRequest(strangerKey, strangerPriv, targetKey, nonce)

	rr := httptest.NewRecorder()
	(&handler{}).peerResolve(rr, httptest.NewRequest(http.MethodPost, "/-/peer/resolve", strings.NewReader(string(body))))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown requestor, got %d (%s)", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "secret-target") {
		t.Fatal("directory leaked the target url to an unknown requestor")
	}
}

// TestPeerResolve_BadSignature: a known requestor whose signature does not verify is
// rejected with 403 (cannot fish under a key it does not control).
func TestPeerResolve_BadSignature(t *testing.T) {
	_, reqKey := genKey(t)
	_, targetKey := genKey(t)
	addPeer(t, reqKey, "https://requestor-badsig.example.com")
	addPeer(t, targetKey, "https://target-badsig.example.com")

	nonce := randNonce(t)
	body, _ := json.Marshal(peerResolveRequest{
		RequestorKey: reqKey,
		TargetKey:    targetKey,
		Nonce:        nonce,
		Signature:    hex.EncodeToString(make([]byte, ed25519.SignatureSize)), // all-zero, invalid
	})

	rr := httptest.NewRecorder()
	(&handler{}).peerResolve(rr, httptest.NewRequest(http.MethodPost, "/-/peer/resolve", strings.NewReader(string(body))))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 for bad signature, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// TestPeerResolve_UnknownTarget: a valid requestor asking for an unregistered target
// gets 404 and no url.
func TestPeerResolve_UnknownTarget(t *testing.T) {
	reqPriv, reqKey := genKey(t)
	_, targetKey := genKey(t) // never registered
	addPeer(t, reqKey, "https://requestor-utarget.example.com")

	nonce := randNonce(t)
	body := signResolveRequest(reqKey, reqPriv, targetKey, nonce)

	rr := httptest.NewRecorder()
	(&handler{}).peerResolve(rr, httptest.NewRequest(http.MethodPost, "/-/peer/resolve", strings.NewReader(string(body))))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown target, got %d (%s)", rr.Code, rr.Body.String())
	}
}

// fakeDirectory returns an httptest server that answers /-/peer/resolve by signing
// the response with signPriv (regardless of what it claims), echoing the request's
// nonce + target so a correctly-keyed answer verifies. claimKey, if non-empty, is
// put in the response's public_key field.
func fakeDirectory(t *testing.T, signPriv ed25519.PrivateKey, claimKey, url string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in peerResolveRequest
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
		sig := ed25519.Sign(signPriv, []byte(peerResolveContext+in.Nonce+"|"+in.TargetKey+"|"+url))
		json.NewEncoder(w).Encode(peerResolveResponse{
			TargetKey: in.TargetKey,
			URL:       url,
			PublicKey: claimKey,
			Signature: hex.EncodeToString(sig),
		})
	}))
}

// TestResolvePeerURL_AcceptsValidAnswer: when the directory signs with the key we
// pinned for it, the client accepts the url.
func TestResolvePeerURL_AcceptsValidAnswer(t *testing.T) {
	dirPriv, dirKey := genKey(t)
	const targetURL = "https://fresh-target.example.trycloudflare.com"
	srv := fakeDirectory(t, dirPriv, dirKey, targetURL)
	defer srv.Close()

	_, targetKey := genKey(t)
	got, err := ResolvePeerURL(registry.Peer{Name: "C", URL: srv.URL, PublicKey: dirKey}, targetKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != targetURL {
		t.Fatalf("url: want %q got %q", targetURL, got)
	}
}

// TestResolvePeerURL_RejectsSpoofedAnswer: a response signed by the WRONG key (a
// MITM forging the directory) must be discarded even though it is well-formed.
func TestResolvePeerURL_RejectsSpoofedAnswer(t *testing.T) {
	_, pinnedKey := genKey(t)    // the key we trust for the directory
	attackerPriv, _ := genKey(t) // a different key the MITM actually holds
	srv := fakeDirectory(t, attackerPriv, "", "https://evil-target.example.com")
	defer srv.Close()

	_, targetKey := genKey(t)
	_, err := ResolvePeerURL(registry.Peer{Name: "C", URL: srv.URL, PublicKey: pinnedKey}, targetKey)
	if err == nil {
		t.Fatal("expected spoofed (wrong-key) answer to be rejected, but it was accepted")
	}
}

// TestResolvePeerURL_RejectsKeyMismatch: a response that openly claims a different
// public_key than the one we pinned is discarded before signature checking.
func TestResolvePeerURL_RejectsKeyMismatch(t *testing.T) {
	_, pinnedKey := genKey(t)
	attackerPriv, attackerKey := genKey(t)
	srv := fakeDirectory(t, attackerPriv, attackerKey, "https://evil-target.example.com")
	defer srv.Close()

	_, targetKey := genKey(t)
	_, err := ResolvePeerURL(registry.Peer{Name: "C", URL: srv.URL, PublicKey: pinnedKey}, targetKey)
	if err == nil {
		t.Fatal("expected mismatched directory key to be rejected")
	}
}
