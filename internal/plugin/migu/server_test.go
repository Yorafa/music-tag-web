package migu

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// ─── the regression: Song.Id was lyricUrl ──────────────────────────────────

// TestDoSearch_PutsContentIDIntoSongNotLyricURL pins the regression
// that surfaced when /api/stream silently failed for migu: the
// implementation was stuffing the lyricUrl string into Song.Id, so
// /api/stream?id=<lyric URL> routed the upstream audio endpoint with
// a non-numeric songid, which the CDN silently answered with empty
// playUrl. Fix: Song.Id MUST be the contentId (the stable upstream
// track id listen.do accepts; copyrightId can be "0", so no cache
// round trip is needed), with the lyric URL cached separately keyed
// on that id.
func TestDoSearch_PutsContentIDIntoSongNotLyricURL(t *testing.T) {
	upstream := newMiguStub(t)
	defer upstream.Close()
	withBaseURL(t, upstream.URL)

	sv := NewServer()
	songs, _, err := sv.doSearch(context.Background(), "test", 1, 10)
	if err != nil {
		t.Fatalf("doSearch: %v", err)
	}
	if len(songs) != 1 {
		t.Fatalf("len(songs)=%d (want 1)", len(songs))
	}
	const wantID = "C1001" // fixture's contentId field (NOT the legacy numeric id)
	if songs[0].ID != wantID {
		t.Fatalf("Song.Id=%q (want %q — must be contentId, NOT lyric URL)",
			songs[0].ID, wantID)
	}
}

// TestDoSearch_FallsBackLyricURLIntoCache pins that the search pass
// populates lyricCache keyed on the canonical song id, so a subsequent
// FetchLyric(req.SongId=numeric_id) call can resolve the lyric
// endpoint URL without re-running the search. We just check the
// cached URL ends with /lyric because the anchor path is what the
// stub's search payload generated during construction — see
// miguStubSearchPayloadAt.
func TestDoSearch_FallsBackLyricURLIntoCache(t *testing.T) {
	upstream := newMiguStub(t)
	defer upstream.Close()
	withBaseURL(t, upstream.URL)

	sv := NewServer()
	songs, _, err := sv.doSearch(context.Background(), "test", 1, 10)
	if err != nil {
		t.Fatalf("doSearch: %v", err)
	}

	got, ok := sv.lyricCache.Load(songs[0].ID)
	if !ok {
		t.Fatalf("lyricCache miss for id=%q (want lyric URL cached)", songs[0].ID)
	}
	if url, ok := got.(string); !ok || !strings.HasSuffix(url, "/lyric") {
		t.Errorf("lyricCache value=%v (want URL with /lyric suffix)", got)
	}
}

// TestDoSearch_FallsBackToTrcURLWhenLyricURLMissing pins the secondary
// contract: when the search response carries `trcUrl` but no
// `lyricUrl`, lyricCache stores the translation URL rather than
// letting FetchLyric return empty.
func TestDoSearch_FallsBackToTrcURLWhenLyricURLMissing(t *testing.T) {
	upstream := newMiguStub(t)
	defer upstream.Close()
	withBaseURL(t, upstream.URL)

	sv := NewServer()
	// Override: drop lyricUrl, keep trcUrl. We pin the substring against
	// the stub URL so the replace is unambiguous.
	patched := strings.Replace(
		miguStubSearchPayloadAt(upstream.URL),
		fmt.Sprintf(`"lyricUrl": "%s/lyric",`, upstream.URL),
		``, 1)
	upstream.updatePayload(patched)
	t.Cleanup(upstream.resetPayload)

	songs, _, err := sv.doSearch(context.Background(), "test", 1, 10)
	if err != nil {
		t.Fatalf("doSearch: %v", err)
	}
	got, ok := sv.lyricCache.Load(songs[0].ID)
	if !ok {
		t.Fatalf("lyricCache miss (trcUrl fallback should have stored)")
	}
	if url, _ := got.(string); !strings.HasSuffix(url, "/trc") {
		t.Errorf("lyricCache value=%v (want URL with /trc suffix)", url)
	}
}

// ─── FetchLyric cache-resolution contract ──────────────────────────────────

// TestFetchLyric_ResolvesCachedURL pins the new FetchLyric contract:
// req.SongId is a numeric song id, FetchLyric resolves it via the
// cache, GETs that endpoint URL, returns the body. This is the test
// that regression-failed before the dynamic-payload fix: the cached
// URL must point back at the stub server, not a literal
// upstream.example (which would 404 in tests).
func TestFetchLyric_ResolvesCachedURL(t *testing.T) {
	upstream := newMiguStub(t)
	defer upstream.Close()
	withBaseURL(t, upstream.URL)

	sv := NewServer()
	songs, _, err := sv.doSearch(context.Background(), "test", 1, 10)
	if err != nil {
		t.Fatalf("doSearch: %v", err)
	}

	resp, err := sv.FetchLyric(context.Background(), &pb.FetchLyricRequest{
		SongId: songs[0].ID, // numeric song id (NOT the lyric URL itself)
	})
	if err != nil {
		t.Fatalf("FetchLyric: %v", err)
	}
	const want = "[00:00.00]hello world"
	if resp.GetLyric() != want {
		t.Errorf("lyric=%q (want %q)", resp.GetLyric(), want)
	}
}

// TestFetchLyric_EmptyOrUncachedSongIDReturnsEmpty pins the no-mis-GET
// contract: empty song id OR a numeric id that wasn't cached (e.g.
// server restart, FetchLyric called before Search) returns an empty
// lyric rather than GETting the song id against the lyric endpoint
// (which would surface arbitrary binary as "lyric text").
func TestFetchLyric_EmptyOrUncachedSongIDReturnsEmpty(t *testing.T) {
	sv := NewServer()

	if resp, _ := sv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: ""}); resp.GetLyric() != "" {
		t.Errorf("empty song id → lyric=%q (want empty)", resp.GetLyric())
	}
	if resp, _ := sv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "1"}); resp.GetLyric() != "" {
		t.Errorf("uncached numeric id=1 → lyric=%q (want empty)", resp.GetLyric())
	}
}

// ─── field-name fallback across API cohorts ────────────────────────────────

// TestDoSearch_FallsBackToLegacyIDWhenContentIDMissing pins the cohort-
// drift contract: if a future migu API version drops `contentId` (or
// renames it), Search should fall back to the legacy `id` field rather
// than returning empty Song.Id and breaking playback.
func TestDoSearch_FallsBackToLegacyIDWhenContentIDMissing(t *testing.T) {
	upstream := newMiguStub(t)
	defer upstream.Close()
	withBaseURL(t, upstream.URL)

	sv := NewServer()
	patched := strings.Replace(
		miguStubSearchPayloadAt(upstream.URL),
		`"id": "1123458789",
				"contentId": "C1001",`,
		`"id": "1123458789",`, 1)
	upstream.updatePayload(patched)
	t.Cleanup(upstream.resetPayload)

	songs, _, err := sv.doSearch(context.Background(), "test", 1, 10)
	if err != nil {
		t.Fatalf("doSearch: %v", err)
	}
	if songs[0].ID != "1123458789" {
		t.Errorf("Song.Id=%q (want legacy id fallback when contentId missing)", songs[0].ID)
	}
} // ─── SetAPIBase listen.do derivation ────────────────────────────────────────

// TestSetAPIBase_DerivesMIGUM3ListenURL pins the SetAPIBase fix that
// replaced the old path-traversal concatenation (apiBase+"/../MIGUM3.0/...")
// which produced a wrong URL against mirror roots. Derivation: strip a
// trailing /MIGUM2.0/v1.0/content suffix, then append the MIGUM3.0 listen
// path; bare roots get the full MIGUM3.0 path appended.
func TestSetAPIBase_DerivesMIGUM3ListenURL(t *testing.T) {
	sv := NewServer()
	// SetAPIBase mutates package-level vars; capture the pre-mutation values
	// and restore them so later tests (and -shuffle/reordered runs) never
	// resolve the mirror.example.com hosts used here. Capture-and-restore is
	// also robust if the production defaults ever change.
	oldBase, oldListen := baseURL, miguListenURL
	t.Cleanup(func() {
		baseURL, miguListenURL = oldBase, oldListen
	})

	sv.SetAPIBase("https://mirror.example.com/MIGUM2.0/v1.0/content")
	want := "https://mirror.example.com/MIGUM3.0/v1.0/content/sub/listen.do"
	if miguListenURL != want {
		t.Errorf("miguListenURL=%q (want %q)", miguListenURL, want)
	}
	if baseURL != "https://mirror.example.com/MIGUM2.0/v1.0/content" {
		t.Errorf("baseURL=%q (must stay the override verbatim)", baseURL)
	}

	// Bare root: no MIGUM2.0 suffix to strip → full MIGUM3.0 path appended.
	sv.SetAPIBase("https://cdn.example.com")
	want = "https://cdn.example.com/MIGUM3.0/v1.0/content/sub/listen.do"
	if miguListenURL != want {
		t.Errorf("bare-root miguListenURL=%q (want %q)", miguListenURL, want)
	}
}

// ─── GetAudioURL contract: direct contentId → listen.do ────────────────────

// TestGetAudioURL_ResolvesDirectViaListenDo pins the 2026-era contract:
// the /audio_only/data route was retired upstream (299996) and the MIGUM3.0
// listen.do endpoint serves a url from contentId + copyrightId=0 (no
// in-process search cache — the id travels in Song.Id). GetAudioURL calls
// listen.do directly and returns the first songListens[].url.
func TestGetAudioURL_ResolvesDirectViaListenDo(t *testing.T) {
	upstream := newMiguStub(t)
	defer upstream.Close()
	withListenURL(t, upstream.URL+"/listen.do")

	sv := NewServer()
	// No prior doSearch needed — the contentId alone must resolve a url.
	resp, err := sv.GetAudioURL(context.Background(), &pb.GetAudioRequest{
		Id: "C1001",
	})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	const want = "https://listen.example/audio.mp3"
	if resp.GetUrl() != want {
		t.Errorf("url=%q (want listen.do songListens[0].url %q)", resp.GetUrl(), want)
	}
}

// TestGetAudioURL_ForwardsContentIDWithZeroCopyright pins the request
// shape: the contentId from req.Id must reach listen.do verbatim and
// copyrightId must default to 0 (verified 2026-08 to return a playable
// url without the search-row copyrightId).
func TestGetAudioURL_ForwardsContentIDWithZeroCopyright(t *testing.T) {
	upstream := newMiguStub(t)
	defer upstream.Close()
	withListenURL(t, upstream.URL+"/listen.do")

	sv := NewServer()
	var sawQuery string
	oldURL := miguListenURL
	// Point at a path we can intercept via the stub handler's query check.
	_ = oldURL
	upstream.setListenPayload(`{"code":"000000","songListens":[{"url":"https://x/a.mp3"}]}`)
	t.Cleanup(upstream.resetListenPayload)
	// The stub handler records the raw query for the /listen.do path.
	upstream.captureListenQuery(&sawQuery)

	resp, err := sv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "C1001"})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if resp.GetUrl() == "" {
		t.Fatal("expected a url from the stubbed listen.do")
	}
	if !strings.Contains(sawQuery, "contentId=C1001") {
		t.Errorf("listen.do query=%q (want contentId=C1001 forwarded verbatim)", sawQuery)
	}
	if !strings.Contains(sawQuery, "copyrightId=0") {
		t.Errorf("listen.do query=%q (want copyrightId=0)", sawQuery)
	}
}

// TestGetAudioURL_EmptyListenReturnsEmpty pins the paid/region-locked
// contract: listen.do returns code 000000 but an empty songListens array
// (or empty url) → GetAudioURL returns ("", nil), never a broken URL.
func TestGetAudioURL_EmptyListenReturnsEmpty(t *testing.T) {
	upstream := newMiguStub(t)
	defer upstream.Close()
	withBaseURL(t, upstream.URL)
	upstream.setListenPayload(`{"code":"000000","songListens":[]}`)
	t.Cleanup(upstream.resetListenPayload)
	withListenURL(t, upstream.URL+"/listen.do")

	sv := NewServer()
	songs, _, err := sv.doSearch(context.Background(), "test", 1, 10)
	if err != nil {
		t.Fatalf("doSearch: %v", err)
	}
	resp, err := sv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: songs[0].ID})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if resp.GetUrl() != "" {
		t.Errorf("url=%q (want empty when songListens empty)", resp.GetUrl())
	}
}

// TestGetAudioURL_EmptySongIDReturnsEmpty pins the input-validation
// short-circuit independently of the upstream behaviour: when req.Id is
// empty, GetAudioURL returns ("", nil) WITHOUT touching the network —
// the same contract as before. Without this test, a future refactor
// moving the empty-id check below the network call would silently
// require an httptest.Server in every test that asserts the empty
// short-circuit.
func TestGetAudioURL_EmptySongIDReturnsEmpty(t *testing.T) {
	sv := NewServer()
	resp, err := sv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: ""})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if resp.GetUrl() != "" {
		t.Errorf("url=%q (want empty for empty song id)", resp.GetUrl())
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────────────

// miguStub is a single httptest.Server that responds to the search
// endpoint and the cached-lyric / cached-trc endpoints. Pulled out so
// each test reads as "swap baseURL + run".
//
// CRITICAL: the canned search response anchors lyricUrl/trcUrl at the
// running stub server's URL (not at a hardcoded `upstream.example`).
// Otherwise cache.Store(lyricCache, stub.SearchResponse.LyricUrl)
// followed by cache.Load → http.Get(stub.LyricURL) would attempt a
// real network call against an unreachable host and silently return
// empty (the bug this stub design explicitly avoids).
//
// The override slot is built on *string + sync.RWMutex (NOT atomic.Value).
// atomic.Value panics on Store(nil) in Go 1.21+ — when resetPayload ran
// the first time, the test paniced with "store of nil value into
// Value". A *string pointer + explicit nil-check reads cleanly and is
// universally concurrent-safe without an atomic-package version pin.
type miguStub struct {
	*httptest.Server
	overrideMu     sync.RWMutex
	overridePtr    *string // nil = no override (use default anchored at s.Server.URL)
	listenMu       sync.RWMutex
	listenOverride *string // nil = default listen.do payload
	listenQueryMu  sync.RWMutex
	listenQueryDst *string // non-nil → capture /listen.do raw query here
}

// miguListenDefaultPayload is the canned /listen.do response the stub
// serves unless overridden. songListens[0].url is what GetAudioURL reads.
const miguListenDefaultPayload = `{"code":"000000","songListens":[{"resourceType":"2","formatType":"HQ","url":"https://listen.example/audio.mp3","fileType":"mp3"}]}`

func newMiguStub(t *testing.T) *miguStub {
	s := &miguStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search_all.do"):
			// Snapshot the override slot under RLock; release before IO
			// so a writer blocking on Lock() doesn't stall us mid-write.
			s.overrideMu.RLock()
			ovr := s.overridePtr
			s.overrideMu.RUnlock()
			payload := miguStubSearchPayloadAt(s.Server.URL)
			if ovr != nil {
				payload = *ovr
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, payload)
		case strings.HasSuffix(r.URL.Path, "/lyric"):
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "[00:00.00]hello world")
		case strings.HasSuffix(r.URL.Path, "/trc"):
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "[00:00.00]translation")
		case strings.HasSuffix(r.URL.Path, "/listen.do"):
			w.Header().Set("Content-Type", "application/json")
			s.listenQueryMu.RLock()
			dst := s.listenQueryDst
			s.listenQueryMu.RUnlock()
			if dst != nil {
				s.listenQueryMu.Lock()
				if s.listenQueryDst != nil {
					*s.listenQueryDst = r.URL.RawQuery
				}
				s.listenQueryMu.Unlock()
			}
			if s.listenOverride != nil {
				_, _ = io.WriteString(w, *s.listenOverride)
			} else {
				_, _ = io.WriteString(w, miguListenDefaultPayload)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	return s
}

// updatePayload replaces the next /search_all.do response with a
// custom JSON string. Use t.Cleanup(stub.resetPayload) to restore the
// default. Concurrent-safe (RWMutex under the hood).
func (s *miguStub) updatePayload(p string) {
	s.overrideMu.Lock()
	defer s.overrideMu.Unlock()
	s.overridePtr = &p
}

// captureListenQuery records the raw query string of the next /listen.do
// request into dst (used to pin request-shape contracts).
func (s *miguStub) captureListenQuery(dst *string) {
	s.listenQueryMu.Lock()
	defer s.listenQueryMu.Unlock()
	s.listenQueryDst = dst
}

// setListenPayload overrides the next /listen.do response (empty songListens
// scenarios). Use t.Cleanup(stub.resetListenPayload) to restore the default.
func (s *miguStub) setListenPayload(p string) {
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	s.listenOverride = &p
}

// resetListenPayload restores the default listen.do body.
func (s *miguStub) resetListenPayload() {
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	s.listenOverride = nil
}

// resetPayload clears the override so subsequent reads fall back to
// the default search payload anchored at the stub server URL.
func (s *miguStub) resetPayload() {
	s.overrideMu.Lock()
	defer s.overrideMu.Unlock()
	s.overridePtr = nil
}

// miguStubSearchPayloadAt returns a canned /search_all.do response
// anchored at `stubURL` so that the lyric / translation URLs point
// back at THIS stub server. Tests that mutate the payload start from
// this and apply edits (regex-replace or json-roundtrip).
func miguStubSearchPayloadAt(stubURL string) string {
	return fmt.Sprintf(`{
		"songResultData": {
			"result": [{
				"id": "1123458789",
				"contentId": "C1001",
				"copyrightId": "CP2002",
				"name": "test song",
				"lyricUrl": "%[1]s/lyric",
				"trcUrl": "%[1]s/trc",
				"singers": [{"name": "test artist"}],
				"albums": [{"name": "test album", "id": "A1"}],
				"imgItems": [{"img": "%[1]s/cover.jpg"}]
			}]
		}
	}`, stubURL)
}

// withBaseURL swaps the package-level baseURL for the duration of the
// test. Cleanup restores the production CN CDN URL. Tests must NOT use
// t.Parallel() while this swap is in effect — package-level var mutation.
func withBaseURL(t *testing.T, url string) {
	old := baseURL
	baseURL = url
	t.Cleanup(func() { baseURL = old })
}

// withListenURL swaps the package-level miguListenURL var for the duration
// of the test. Cleanup restores the production CN CDN URL. Tests must NOT
// use t.Parallel() while this swap is in effect — package-level var
// mutation.
func withListenURL(t *testing.T, url string) {
	old := miguListenURL
	miguListenURL = url
	t.Cleanup(func() { miguListenURL = old })
}
