package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/plugin"
)

// downloadTestRouter wires POST /api/download/ behind the bare handler
// (no JWT middleware — these tests cover the handler logic only).
func downloadTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/download/", Download)
	return r
}

// TestDownload_CacheHitSkipsErrorMarker pins the short-circuit fix: the
// glob `<cache>/<source>/<id>.*` also matches worker-written `.error`
// failure markers. The handler must NOT treat a stale `<id>.error` as a
// cache hit — otherwise the error text gets copied into MUSIC_DIR as if
// it were the downloaded audio. A sibling `.ogg` (the real cache hit)
// must win.
func TestDownload_CacheHitSkipsErrorMarker(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)
	plugin.InstallMockDownloadSource("youtube", &fakeListDownload{
		name:        "youtube",
		displayName: "YouTube",
	})

	root := t.TempDir()
	ytDir := filepath.Join(root, "youtube")
	if err := os.MkdirAll(ytDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A stale failure marker (the regression vector). Also seed a genuine
	// audio cache file so the test can tell which one wins.
	marker := "yt-dlp: Sign in to confirm you're not a bot"
	if err := os.WriteFile(filepath.Join(ytDir, "abc123.error"), []byte(marker), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	audio := make([]byte, 4096)
	for i := range audio {
		audio[i] = byte('A' + i%26)
	}
	if err := os.WriteFile(filepath.Join(ytDir, "abc123.ogg"), audio, 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	t.Setenv("AUDIO_CACHE_DIR", root)
	musicRoot := t.TempDir()
	t.Setenv("MUSIC_DIR", musicRoot)

	body := `{"source":"youtube","video_id":"abc123","download_path":"Artist - Title.ogg"}`
	r := downloadTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/download/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var envelope struct {
		Result  bool   `json:"result"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, w.Body.String())
	}
	if !envelope.Result {
		t.Fatalf("envelope.result=false body=%s", w.Body.String())
	}

	// The library copy must be the audio bytes, NOT the .error marker.
	dest := filepath.Join(musicRoot, "Artist - Title.ogg")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read library dest: %v", err)
	}
	if string(got) != string(audio) {
		t.Errorf("library file contains the error marker or wrong bytes:\n got  %q\n want audio payload (len %d)", truncateForTest(got), len(audio))
	}
}

// TestDownload_CacheHitOnlyErrorMarkerNotShortCircuited pins the other
// half: when the ONLY `<id>.*` match is a `.error` marker, the handler
// must NOT report "已经在缓存中" / copy it to the library. It falls
// through to enqueue (which, without Redis in this test, fails with the
// enqueue error envelope) — but never a fake cache-hit success.
func TestDownload_CacheHitOnlyErrorMarkerNotShortCircuited(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)
	plugin.InstallMockDownloadSource("youtube", &fakeListDownload{
		name:        "youtube",
		displayName: "YouTube",
	})

	root := t.TempDir()
	ytDir := filepath.Join(root, "youtube")
	if err := os.MkdirAll(ytDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ytDir, "abc123.error"),
		[]byte("yt-dlp: only images are available"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	t.Setenv("AUDIO_CACHE_DIR", root)
	musicRoot := t.TempDir()
	t.Setenv("MUSIC_DIR", musicRoot)

	body := `{"source":"youtube","video_id":"abc123","download_path":"Artist - Title.ogg"}`
	r := downloadTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/download/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	bodyStr := w.Body.String()
	if strings.Contains(bodyStr, "已经在缓存中") {
		t.Errorf("body claims cache hit when only a .error marker exists: %s", bodyStr)
	}
	// Nothing should have been copied into the library.
	entries, err := os.ReadDir(musicRoot)
	if err != nil {
		t.Fatalf("read music root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("library should be empty; found %d entries", len(entries))
	}
}

// TestDownload_RejectsGlobMetacharID pins the unsafeCacheID guard: a
// video_id containing glob metacharacters must be refused before any
// filepath.Glob against the cache dir (which would otherwise match every
// cached file and let a hostile id copy arbitrary cache entries).
func TestDownload_RejectsGlobMetacharID(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)
	plugin.InstallMockDownloadSource("youtube", &fakeListDownload{
		name:        "youtube",
		displayName: "YouTube",
	})

	r := downloadTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/download/",
		strings.NewReader(`{"source":"youtube","video_id":"*","download_path":"x.ogg"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "invalid video_id") {
		t.Errorf("body=%q (expected invalid video_id)", w.Body.String())
	}
}

func truncateForTest(b []byte) string {
	if len(b) > 80 {
		return string(b[:80]) + "..."
	}
	return string(b)
}
