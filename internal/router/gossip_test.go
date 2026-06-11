package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RED-Collective/red-engine/internal/node"
	"github.com/RED-Collective/red-engine/internal/registry"
)

// TestGossipImportsPeerOfPeer: Node B is already registered. B's /-/peers list
// contains Node C (a third-party node we don't know yet). importPeerGossip(B)
// must add C and return count=1.
func TestGossipImportsPeerOfPeer(t *testing.T) {
	t.Setenv("RED_ALLOW_PRIVATE_SYNC", "true") // httptest servers run on loopback
	_, cKey := genKey(t)

	// Node C: reachable but not yet registered with us.
	cSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(nodeInfoResponse{Name: "node-C-gossip", PublicKey: cKey})
	}))
	defer cSrv.Close()

	_, bKey := genKey(t)

	// Node B: serves /-/nodeinfo for the addPeer flow and /-/peers for gossip.
	bSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/peers" {
			json.NewEncoder(w).Encode([]peerListItem{
				{URL: cSrv.URL, PublicKey: cKey, Name: "node-C-gossip", PeerType: "upstream"},
			})
			return
		}
		json.NewEncoder(w).Encode(nodeInfoResponse{Name: "node-B-gossip", PublicKey: bKey})
	}))
	defer bSrv.Close()

	if err := registry.AddPeer(registry.Peer{
		URL:      bSrv.URL,
		PublicKey: bKey,
		Name:     "node-B-gossip",
		PeerType: "upstream",
		LastSeen: time.Now(),
		AddedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("setup: register B: %v", err)
	}

	if existing, _ := registry.GetPeerByPublicKey(cKey); existing != nil {
		t.Fatal("pre-condition: C must not be in registry before gossip")
	}

	n := importPeerGossip(bSrv.URL)
	if n != 1 {
		t.Fatalf("gossip: imported %d peer(s), want 1", n)
	}

	got, _ := registry.GetPeerByPublicKey(cKey)
	if got == nil {
		t.Fatal("C was not added to registry after gossip")
	}
	if got.URL != cSrv.URL {
		t.Fatalf("C url: want %q got %q", cSrv.URL, got.URL)
	}
	// Gossip-imported peers are always upstream regardless of what B advertised.
	if got.PeerType != "upstream" {
		t.Fatalf("gossip peer type: want %q got %q", "upstream", got.PeerType)
	}
}

// TestGossipSkipsAlreadyKnown: a peer already registered is not imported a second
// time. Gossip must be idempotent — running it twice must not change count.
func TestGossipSkipsAlreadyKnown(t *testing.T) {
	t.Setenv("RED_ALLOW_PRIVATE_SYNC", "true")
	_, dKey := genKey(t)
	const dURL = "https://node-d-known.example.com"
	if err := registry.AddPeer(registry.Peer{
		URL:      dURL,
		PublicKey: dKey,
		Name:     "node-D",
		PeerType: "upstream",
		LastSeen: time.Now(),
		AddedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("setup: register D: %v", err)
	}

	_, b2Key := genKey(t)
	b2Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/peers" {
			json.NewEncoder(w).Encode([]peerListItem{
				{URL: dURL, PublicKey: dKey, Name: "node-D", PeerType: "mirror"},
			})
			return
		}
		json.NewEncoder(w).Encode(nodeInfoResponse{Name: "node-B2-gossip", PublicKey: b2Key})
	}))
	defer b2Srv.Close()

	if err := registry.AddPeer(registry.Peer{
		URL:      b2Srv.URL,
		PublicKey: b2Key,
		Name:     "node-B2-gossip",
		PeerType: "upstream",
		LastSeen: time.Now(),
		AddedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("setup: register B2: %v", err)
	}

	// First run: D is already known, nothing new.
	n := importPeerGossip(b2Srv.URL)
	if n != 0 {
		t.Fatalf("already-known peer was re-imported (want 0 got %d)", n)
	}

	// Second run (idempotent): still zero new peers.
	n2 := importPeerGossip(b2Srv.URL)
	if n2 != 0 {
		t.Fatalf("second gossip run imported %d peer(s), want 0 (not idempotent)", n2)
	}
}

// TestGossipSkipsSelf: if the peer list returned by B includes this node's own
// public key (e.g. because B knows us as a downstream), gossip must not add
// our own node as a peer of itself.
func TestGossipSkipsSelf(t *testing.T) {
	t.Setenv("RED_ALLOW_PRIVATE_SYNC", "true")
	selfKey := node.GetNodePublicKey()

	_, b3Key := genKey(t)
	b3Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/peers" {
			json.NewEncoder(w).Encode([]peerListItem{
				// B reports us back at a plausible URL — gossip must skip it by key.
				{URL: "https://self-loopback.example.com", PublicKey: selfKey, Name: "this-node"},
			})
			return
		}
		json.NewEncoder(w).Encode(nodeInfoResponse{Name: "node-B3-gossip", PublicKey: b3Key})
	}))
	defer b3Srv.Close()

	if err := registry.AddPeer(registry.Peer{
		URL:      b3Srv.URL,
		PublicKey: b3Key,
		Name:     "node-B3-gossip",
		PeerType: "upstream",
		LastSeen: time.Now(),
		AddedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("setup: register B3: %v", err)
	}

	n := importPeerGossip(b3Srv.URL)
	if n != 0 {
		t.Fatalf("self-import was not skipped (want 0 got %d)", n)
	}
}

// TestAddPeerHandlerGossipIsSync: when the addPeer handler receives import_peers=true
// the gossip runs synchronously before the 201 is returned. The response body must
// contain gossip_imported=1 and C must be in the registry in one round-trip —
// proving no second add is required to see the gossip result.
func TestAddPeerHandlerGossipIsSync(t *testing.T) {
	t.Setenv("RED_ALLOW_PRIVATE_SYNC", "true")
	_, fKey := genKey(t)
	// Node F: a third-party peer that B knows about.
	fSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(nodeInfoResponse{Name: "node-F-handler", PublicKey: fKey})
	}))
	defer fSrv.Close()

	_, b4Key := genKey(t)
	// Node B4: serves /-/nodeinfo for addPeer and /-/peers for gossip (returns F).
	b4Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/peers" {
			json.NewEncoder(w).Encode([]peerListItem{
				{URL: fSrv.URL, PublicKey: fKey, Name: "node-F-handler", PeerType: "upstream"},
			})
			return
		}
		json.NewEncoder(w).Encode(nodeInfoResponse{Name: "node-B4-handler", PublicKey: b4Key})
	}))
	defer b4Srv.Close()

	body, _ := json.Marshal(addPeerRequest{
		URL:         b4Srv.URL,
		PeerType:    "upstream",
		ImportPeers: true,
	})
	rr := httptest.NewRecorder()
	(&handler{}).addPeer(rr, httptest.NewRequest(
		http.MethodPost, "/-/admin/peers/add", strings.NewReader(string(body))))

	if rr.Code != http.StatusCreated {
		t.Fatalf("addPeer: want 201 got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]int
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["gossip_imported"] != 1 {
		t.Fatalf("gossip_imported: want 1 got %d — gossip may still be async", resp["gossip_imported"])
	}

	// F must already be in the registry from this single call — no second add.
	got, _ := registry.GetPeerByPublicKey(fKey)
	if got == nil {
		t.Fatal("F not in registry after single add-with-gossip call (sync guarantee broken)")
	}
}
