package registry

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// TestAddPeerConcurrent guards the SQLite busy_timeout/WAL configuration in
// InitRegistry. Federation writes the peers table from several goroutines at once
// (an explicit peer add triggers a reciprocal /-/announce/downstream that also
// writes; the announce and peer-sync loops write on their own timers). Without a
// busy timeout these collide with SQLITE_BUSY and a registration silently 500s —
// exactly the race seen on the directory node during the 3-node live test. With it,
// every concurrent insert succeeds.
func TestAddPeerConcurrent(t *testing.T) {
	// InitRegistry binds the package-global db exactly once (sync.Once), so we must
	// not remove the dir mid-process: under `-test.count>1` later iterations reuse
	// the same handle. AddPeer upserts by key, so re-running is idempotent (still 24
	// rows). The temp dir is left for the OS to reap.
	dir, err := os.MkdirTemp("", "red-reg-conc-*")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	if err := InitRegistry(dir); err != nil {
		t.Fatalf("init registry: %v", err)
	}

	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- AddPeer(Peer{
				PublicKey: fmt.Sprintf("%064x", i+1),
				URL:       fmt.Sprintf("https://peer-%d.example.com", i),
				Name:      fmt.Sprintf("peer-%d", i),
				PeerType:  "mirror",
				LastSeen:  time.Now(),
				AddedAt:   time.Now(),
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent AddPeer failed (SQLITE_BUSY not handled?): %v", err)
		}
	}

	peers, err := ListPeers()
	if err != nil {
		t.Fatalf("list peers: %v", err)
	}
	if len(peers) != n {
		t.Fatalf("want %d peers after concurrent inserts, got %d", n, len(peers))
	}
}
