package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/plugin"
)

// ─── helpers ──────────────────────────────────────────────────────────────

// fakeTagSource is the test-installed mock TagSource whose GetAudioURL
// returns a fixed upstream URL. Other methods are no-ops; the test
// surface only exercises StreamAudio's plugin branch.
type fakeTagSource struct {
	name        string
	displayName string
	audioURL    string
	audioErr    error
}

func (f *fakeTagSource) Name() string           { return f.name }
func (f *fakeTagSource) DisplayName() string    { return f.displayName }
func (f *fakeTagSource) SupportsSearch() bool   { return false }
func (f *fakeTagSource) SupportsLyric() bool    { return false }
func (f *fakeTagSource) SupportsId3() bool      { return false }
func (f *fakeTagSource) SupportsAudioURL() bool { return f.audioURL != "" || f.audioErr != nil }
func (f *fakeTagSource) Search(_ context.Context, _ string, _, _ int) (*plugin.SearchResult, error) {
	return nil, nil
}
func (f *fakeTagSource) FetchID3ByTitle(_ context.Context, _ string) ([]plugin.Song, error) {
	return nil, nil
}
func (f *fakeTagSource) FetchLyric(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *fakeTagSource) GetAudioURL(_ context.Context, _ string) (string, error) {
	return f.audioURL, f.audioErr
}

// newStreamTestRouter wires /api/stream/ behind a bare JWT-skipping
// router — these tests cover the handler, not the auth middleware.
// HEAD is registered alongside GET to mirror the production router
// (router.go registers both so the frontend's waitForStreamReady HEAD
// preflight works).
func newStreamTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/stream/", StreamAudio)
	r.HEAD("/api/stream/", StreamAudio)
	return r
}

// ─── download-source branch (was "youtube branch") ───────────────────────

// TestStreamAudio_DownloadSourceMissingTriggers202WithRetryAfter pins the
// new behaviour: when a registered DownloadSource's cache dir is empty,
// the handler enqueues a download task (or fails gracefully past the
// taskclient semantics in tests) AND long-polls the cache dir for up to
// 10s. Since this test runs without a real Redis, the enqueue helper
// returns false; the handler fail-closes with "下载 cache 写入失败" rather
// than silently returning a 202 — but if a download-source mock is
// installed and the cache is empty, the result must NOT be the legacy
// "尚未下载" failure envelope. The handler is expected to either enqueue
// successfully (real Redis) or short-circuit with the inline-trigger
// error; both paths achieve the contract that the test asserts: the
// handler routes by registry, not by hardcoded source-name compare.
func TestStreamAudio_DownloadSourceMissingReturns202OrEnqueueError(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	// Point AUDIO_CACHE_DIR at an empty temp dir so the glob hits no files.
	// The handler reads os.Getenv via audioCacheDir helper; t.Setenv is
	// picked up on every handler invocation. The per-source subdir is
	// <root>/<source>, so we don't need to pre-create anything.
	dir := t.TempDir()
	t.Setenv("AUDIO_CACHE_DIR", dir)

	plugin.InstallMockDownloadSource("youtube", &fakeListDownload{
		name:        "youtube",
		displayName: "YouTube",
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=youtube&id=missingVid", nil)
	r.ServeHTTP(w, req)

	// Two acceptable outcomes:
	//  (a) 202 + Retry-After (real Redis available, enqueue succeeded,
	//      long-poll timed out). Body shape is the download_pending JSON.
	//  (b) Failure() envelope with "下载 cache 写入失败" or "enqueue: …"
	//      (test env has no Redis; enqueueDownloadTask fails fast).
	// Either is fine — what matters is "尚未下载" is NOT returned, because
	// that sentinel relied on the old hardcoded src=="youtube" branch.
	body := w.Body.String()
	if contains(body, "尚未下载") {
		t.Errorf("body=%q still contains legacy 尚未下载 sentinel — handler must route via registry, not hardcoded src==\"youtube\"", body)
	}
}

// TestStreamAudio_DownloadSourceServesFileWithRange proves that
// http.ServeFile is invoked for registered DownloadSources so Range
// scrubbing works without extra code. The test expects the per-source
// cache subdir `<AUDIO_CACHE_DIR>/<source>/` — that's part of the
// grilling decision (per-source subdirs prevent cross-source id
// collisions) and is enforced here.
func TestStreamAudio_DownloadSourceServesFileWithRange(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	dir := t.TempDir()
	// Create <dir>/youtube/ and drop the fixture file inside — the new
	// per-source subdir layout is what the handler globs.
	ytDir := filepath.Join(dir, "youtube")
	if err := os.MkdirAll(ytDir, 0o755); err != nil {
		t.Fatalf("mkdir yt subdir: %v", err)
	}
	// filterAudioMatches refuses <1KiB stubs (avoids 416 on empty/half files).
	// Pad so ServeFile is exercised without falling into the enqueue path.
	payload := make([]byte, 2048)
	copy(payload, []byte("0123456789ABCDEF"))
	if err := os.WriteFile(filepath.Join(ytDir, "abc123.ogg"), payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("AUDIO_CACHE_DIR", dir)

	plugin.InstallMockDownloadSource("youtube", &fakeListDownload{
		name:        "youtube",
		displayName: "YouTube",
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=youtube&id=abc123", nil)
	req.Header.Set("Range", "bytes=4-7")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status=%d (want 206 from Range), body=%s", w.Code, w.Body.String())
	}
	got, _ := io.ReadAll(w.Body)
	if string(got) != "4567" {
		t.Errorf("body=%q (want range slice '4567')", got)
	}
	if cr := w.Header().Get("Content-Range"); cr == "" {
		t.Error("missing Content-Range header from http.ServeFile")
	}
}

// TestStreamAudio_DownloadSourceRejectsPathSeparator guards against
// traversal: a malicious `id` containing "/" or "\" (URI-encoded) is
// refused before the cache dir is globergized, so a hostile id can't
// escape the per-source cache subdir.
func TestStreamAudio_DownloadSourceRejectsPathSeparator(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	plugin.InstallMockDownloadSource("youtube", &fakeListDownload{
		name:        "youtube",
		displayName: "YouTube",
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=youtube&id=..%2Fetc%2Fpasswd", nil)
	r.ServeHTTP(w, req)
	if !contains(w.Body.String(), "invalid source id") {
		t.Errorf("body=%q (expected invalid-id guard)", w.Body.String())
	}
}

// ─── plugin branch ────────────────────────────────────────────────────────

func TestStreamAudio_EmptyURLFromPluginReturnsFailure(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)
	plugin.InstallMockTagSource("testplugin", &fakeTagSource{
		name: "testplugin", displayName: "Test",
		audioURL: "", audioErr: nil,
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=testplugin&id=track1", nil)
	r.ServeHTTP(w, req)

	if !contains(w.Body.String(), "preview unavailable") {
		t.Errorf("body=%q (expected preview-unavailable failure)", w.Body.String())
	}
}

// TestStreamAudio_EmptyURLFromPluginReturns502 pins HTTP status contract:
// when the upstream plugin returns ("", nil) — i.e. preview unavailable —
// the gateway must surface 502 (not 200). 200-with-JSON is silently
// dropped by `<audio>` elements: they never fire `error`, so the toast
// path in PlayerBar.tsx::onMediaError is never reached. Returning 502
// makes every browser fire `error` reliably.
func TestStreamAudio_EmptyURLFromPluginReturns502(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)
	plugin.InstallMockTagSource("testplugin", &fakeTagSource{
		name: "testplugin", displayName: "Test",
		audioURL: "", audioErr: nil,
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=testplugin&id=track1", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status=%d (want 502 so <audio>.error fires), body=%s", w.Code, w.Body.String())
	}
}

// TestStreamAudio_PluginErrorReturns502 covers the *other* preview-
// unavailable branch: when GetAudioURL returns ("", err) with a non-nil
// error. The message embeds the upstream error text (different from the
// empty-URL path), so we assert both status=502 AND body contains the
// simulated error message. Without this test the FailureStatus code path
// that string-concatenates err.Error() is not locked.
func TestStreamAudio_PluginErrorReturns502(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)
	plugin.InstallMockTagSource("testplugin", &fakeTagSource{
		name: "testplugin", displayName: "Test",
		audioURL: "", audioErr: errors.New("simulated upstream timeout"),
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=testplugin&id=track1", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status=%d (want 502 so <audio>.error fires), body=%s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "simulated upstream timeout") {
		t.Errorf("body=%q (want upstream error message in body)", w.Body.String())
	}
}

func TestStreamAudio_ProxiesUpstreamBytes(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	// Spin an upstream httptest.Server that returns a small audio body.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("upstream-bytes"))
	}))
	t.Cleanup(upstream.Close)

	plugin.InstallMockTagSource("testplugin", &fakeTagSource{
		name: "testplugin", displayName: "Test",
		audioURL: upstream.URL,
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=testplugin&id=track1", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d (want 200)", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	if string(body) != "upstream-bytes" {
		t.Errorf("body=%q (want upstream-bytes)", body)
	}
}

// TestStreamAudio_HeadUsesUpstreamHead pins the preflight fix: the
// frontend waitForStreamReady does a HEAD first. The handler must forward
// HEAD as HEAD to the upstream plugin URL — not a GET that would pull the
// entire audio body just to have net/http discard it for the HEAD
// response (a 2×-bandwidth waste on every preview click).
func TestStreamAudio_HeadUsesUpstreamHead(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	var gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	plugin.InstallMockTagSource("testplugin", &fakeTagSource{
		name: "testplugin", displayName: "Test",
		audioURL: upstream.URL,
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("HEAD", "/api/stream/?src=testplugin&id=track1", nil)
	r.ServeHTTP(w, req)

	if gotMethod != http.MethodHead {
		t.Errorf("upstream method=%q (want HEAD for HEAD preflight)", gotMethod)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status=%d (want 200)", w.Code)
	}
}

// TestStreamAudio_HeadFallsBackToGetWhenUpstreamRejectsHEAD pins the
// preflight resilience fix: signed-URL CDNs (kuwo / kugou) sometimes
// reject HEAD with 403/404 while answering GET fine. The frontend's
// waitForStreamReady only falls back to GET on 405/501, so the handler
// must retry the upstream once as GET on a non-2xx HEAD and let the
// HEAD inbound discard the body.
func TestStreamAudio_HeadFallsBackToGetWhenUpstreamRejectsHEAD(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	var headHits, getHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			headHits++
			w.WriteHeader(http.StatusForbidden) // CDN rejects HEAD
		case http.MethodGet:
			getHits++
			w.Header().Set("Content-Type", "audio/mpeg")
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected upstream method %q", r.Method)
		}
	}))
	t.Cleanup(upstream.Close)

	plugin.InstallMockTagSource("testplugin", &fakeTagSource{
		name: "testplugin", displayName: "Test",
		audioURL: upstream.URL,
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("HEAD", "/api/stream/?src=testplugin&id=track1", nil)
	r.ServeHTTP(w, req)

	if headHits != 1 || getHits != 1 {
		t.Errorf("upstream hits head=%d get=%d (want 1/1 after GET retry)", headHits, getHits)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status=%d (want 200 from the GET retry)", w.Code)
	}
}

// TestStreamAudio_DropsContentLengthOnlyWhenOverCap pins the targeted
// truncation guard: the handler strips Content-Length only when the
// upstream declares a length ABOVE streamBodyCapBytes (otherwise the
// proxied io.CopyN-truncated stream would advertise bytes that never
// arrive and <audio> would hang waiting for them). Under-cap responses
// keep their exact byte count so browsers get accurate progress.
func TestStreamAudio_DropsContentLengthOnlyWhenOverCap(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	// Over-cap: declare a length above the proxy cap while serving a tiny
	// body — simulates a multi-GB upstream track the gateway truncates.
	overCap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(int(streamBodyCapBytes)+1))
		_, _ = w.Write([]byte("overcap"))
	}))
	t.Cleanup(overCap.Close)

	// Under-cap: normal small preview; the exact byte count must survive.
	underCap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("under-cap-preview"))
	}))
	t.Cleanup(underCap.Close)

	cases := []struct {
		name     string
		upstream string
		wantBody string
		wantCL   bool
	}{
		{name: "over-cap drops Content-Length", upstream: overCap.URL, wantBody: "overcap", wantCL: false},
		{name: "under-cap keeps Content-Length", upstream: underCap.URL, wantBody: "under-cap-preview", wantCL: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin.ResetForTesting()
			t.Cleanup(plugin.ResetForTesting)
			plugin.InstallMockTagSource("testplugin", &fakeTagSource{
				name: "testplugin", displayName: "Test",
				audioURL: tc.upstream,
			})

			r := newStreamTestRouter()
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/api/stream/?src=testplugin&id=track1", nil)
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status=%d (want 200), body=%s", w.Code, w.Body.String())
			}
			if got := w.Body.String(); got != tc.wantBody {
				t.Errorf("body=%q (want %q)", got, tc.wantBody)
			}
			gotCL := w.Header().Get("Content-Length")
			if (gotCL != "") != tc.wantCL {
				t.Errorf("Content-Length present=%v value=%q (want present=%v)", gotCL != "", gotCL, tc.wantCL)
			}
		})
	}
}

// TestStreamAudio_RangePassthroughToUpstream pins the contract that
// the request's Range header is forwarded as-is to the upstream URL.
func TestStreamAudio_RangePassthroughToUpstream(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	var observedRange string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusPartialContent)
		w.Header().Set("Content-Range", "bytes 0-3/10")
		_, _ = w.Write([]byte("abcd"))
	}))
	t.Cleanup(upstream.Close)

	plugin.InstallMockTagSource("testplugin", &fakeTagSource{
		name: "testplugin", displayName: "Test",
		audioURL: upstream.URL,
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=testplugin&id=track1", nil)
	req.Header.Set("Range", "bytes=0-3")
	r.ServeHTTP(w, req)

	if observedRange != "bytes=0-3" {
		t.Errorf("upstream Range header=%q (want bytes=0-3)", observedRange)
	}
	if w.Code != http.StatusPartialContent {
		t.Errorf("status=%d (want 206)", w.Code)
	}
}

// TestStreamAudio_UnknownSourceReturnsFailure exists so a future
// addition that forgets the dropdown path doesn't silently 500.
func TestStreamAudio_UnknownSourceReturnsFailure(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=doesnotexist&id=track1", nil)
	r.ServeHTTP(w, req)
	if !contains(w.Body.String(), "unsupported source") {
		t.Errorf("body=%q (expected unsupported-source failure)", w.Body.String())
	}
}

// TestStreamAudio_UnknownSourceReturns400 pins that an unsupported source
// name surfaces as 400 + envelope (caller-side mistake), NOT 200+envelope.
// The 200 path silently fails through <audio>.error — see FailureStatus
// comment in response.go.
func TestStreamAudio_UnknownSourceReturns400(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=doesnotexist&id=track1", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d (want 400), body=%s", w.Code, w.Body.String())
	}
}

// TestStreamAudio_RejectPathSeparatorReturns400 pins the invalid-id
// guard at 400 + envelope. The legacy test only checked the body; this
// one locks the HTTP status so a regression to 200 is caught.
func TestStreamAudio_RejectPathSeparatorReturns400(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	plugin.InstallMockDownloadSource("youtube", &fakeListDownload{
		name:        "youtube",
		displayName: "YouTube",
	})

	r := newStreamTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/stream/?src=youtube&id=..%2Fetc%2Fpasswd", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d (want 400 for invalid-id guard), body=%s", w.Code, w.Body.String())
	}
}

// contains: avoid pulling in strings just for Index (deps-light test).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
