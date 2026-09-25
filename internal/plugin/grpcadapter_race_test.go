package plugin

import (
	"sync"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// TestGRPCTagSource_AccessorsBeforeHandshake pins REVIEW.md P2-11's nil
// case. SupportsSearch and SupportsLyric dereferenced the cached info
// without a nil guard, while their siblings SupportsId3 / SupportsAudioURL
// did guard. That was safe only under an implicit invariant nothing
// enforced: "no accessor is called before a successful handshake".
//
// Name() breaks that invariant on its own — on a failed ensureConn it
// returns with nothing cached — so a caller that touches Name() then asks
// what the plugin supports lands straight in the nil dereference.
func TestGRPCTagSource_AccessorsBeforeHandshake(t *testing.T) {
	// A source pointed at a dead address: ensureConn cannot succeed, so
	// nothing is ever cached.
	g := NewGRPCTagSource("127.0.0.1:1", DialOptions{})

	if n := g.Name(); n != "" {
		t.Errorf("Name() on an unconnected source = %q, want %q", n, "")
	}
	if d := g.DisplayName(); d != "" {
		t.Errorf("DisplayName() = %q, want %q", d, "")
	}

	// These are the two that used to panic.
	if g.SupportsSearch() {
		t.Error("SupportsSearch() = true with no handshake")
	}
	if g.SupportsLyric() {
		t.Error("SupportsLyric() = true with no handshake")
	}
	if g.SupportsId3() {
		t.Error("SupportsId3() = true with no handshake")
	}
	if g.SupportsAudioURL() {
		t.Error("SupportsAudioURL() = true with no handshake")
	}
}

// TestGRPCTagSource_ConcurrentAccessorsAreRaceFree exercises the accessors
// concurrently against a writer storing handshake results — the shape of
// the reconnect path that produced the race.
//
// Run with -race; without it this is only a smoke test.
func TestGRPCTagSource_ConcurrentAccessorsAreRaceFree(t *testing.T) {
	// Seed a value so the readers below never trigger Name()'s lazy dial:
	// this spec is about concurrent access, and paying ensureConn's 5s
	// blocking-dial timeout four times over would dominate the suite.
	g := NewGRPCTagSource("127.0.0.1:1", DialOptions{})
	g.info.Store(&pb.PluginInfoResponse{Name: "netease", DisplayName: "NetEase"})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: simulates ensureConn republishing the handshake result while
	// requests are in flight.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			g.info.Store(&pb.PluginInfoResponse{
				Name:             "netease",
				DisplayName:      "NetEase",
				SupportsSearch:   i%2 == 0,
				SupportsLyric:    i%3 == 0,
				SupportsId3:      true,
				SupportsAudioUrl: true,
			})
		}
		close(stop)
	}()

	// Readers: every accessor, repeatedly.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = g.Name()
				_ = g.DisplayName()
				_ = g.SupportsSearch()
				_ = g.SupportsLyric()
				_ = g.SupportsId3()
				_ = g.SupportsAudioURL()
			}
		}()
	}
	wg.Wait()
}

// TestGRPCDownloadSource_AccessorsBeforeHandshake is the same nil-safety
// pin for the download adapter.
func TestGRPCDownloadSource_AccessorsBeforeHandshake(t *testing.T) {
	g := NewGRPCDownloadSource("127.0.0.1:1", DialOptions{})

	if n := g.Name(); n != "" {
		t.Errorf("Name() = %q, want %q", n, "")
	}
	if d := g.DisplayName(); d != "" {
		t.Errorf("DisplayName() = %q, want %q", d, "")
	}
}

// TestGRPCDownloadSource_ConcurrentAccessorsAreRaceFree mirrors the tag
// source test for the download adapter.
func TestGRPCDownloadSource_ConcurrentAccessorsAreRaceFree(t *testing.T) {
	// Seeded for the same reason as the tag-source spec: keep the lazy
	// dial out of a concurrency test.
	g := NewGRPCDownloadSource("127.0.0.1:1", DialOptions{})
	g.info.Store(&pb.DownloadPluginInfoResponse{Name: "youtube", DisplayName: "YouTube"})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			g.info.Store(&pb.DownloadPluginInfoResponse{
				Name:        "youtube",
				DisplayName: "YouTube",
			})
		}
		close(stop)
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = g.Name()
				_ = g.DisplayName()
			}
		}()
	}
	wg.Wait()
}

// TestGRPCTagSource_AccessorsSeeStoredInfo pins that the accessors actually
// read the stored value, so the nil-guard did not come at the cost of
// always returning false.
func TestGRPCTagSource_AccessorsSeeStoredInfo(t *testing.T) {
	g := NewGRPCTagSource("127.0.0.1:1", DialOptions{})
	g.info.Store(&pb.PluginInfoResponse{
		Name:             "netease",
		DisplayName:      "NetEase Cloud Music",
		SupportsSearch:   true,
		SupportsLyric:    true,
		SupportsId3:      true,
		SupportsAudioUrl: false,
	})

	if got := g.Name(); got != "netease" {
		t.Errorf("Name() = %q, want %q", got, "netease")
	}
	if got := g.DisplayName(); got != "NetEase Cloud Music" {
		t.Errorf("DisplayName() = %q", got)
	}
	if !g.SupportsSearch() {
		t.Error("SupportsSearch() = false, want true")
	}
	if !g.SupportsLyric() {
		t.Error("SupportsLyric() = false, want true")
	}
	if !g.SupportsId3() {
		t.Error("SupportsId3() = false, want true")
	}
	if g.SupportsAudioURL() {
		t.Error("SupportsAudioURL() = true, want false")
	}
}
