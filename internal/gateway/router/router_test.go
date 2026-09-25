package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/config"
)

// TestSetup_MediaStaticServesRange pins the contract that browser-side
// id3Reader + <audio> depend on: GET /media/<relpath> streams bytes from
// MUSIC_DIR with native Range support, without JWT. Regression of this
// route is exactly the "added music never shows tags" bug — hydrateTags
// Range-fetches /media/... and silently skips empty/failed parses.
func TestSetup_MediaStaticServesRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	musicDir := t.TempDir()
	// Nested path mirrors real library layout: <album>/<track>.ogg
	rel := filepath.Join("17", "track.ogg")
	abs := filepath.Join(musicDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("OggS-fake-audio-bytes-0123456789abcdef")
	if err := os.WriteFile(abs, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	staticDir := t.TempDir()
	// Minimal SPA so /admin does not 500 on missing index.
	dist := filepath.Join(staticDir, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>spa</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STATIC_DIR", staticDir)

	cfg := &config.Config{
		MusicDir:           musicDir,
		JWTSecret:          "test-secret",
		CORSAllowedOrigins: nil,
	}
	r := gin.New()
	Setup(r, cfg, nil)

	// Full-file GET
	req := httptest.NewRequest(http.MethodGet, "/media/"+filepath.ToSlash(rel), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /media: status=%d body=%q", w.Code, w.Body.String())
	}
	body, _ := io.ReadAll(w.Body)
	if string(body) != string(payload) {
		t.Fatalf("GET /media body mismatch: got %q want %q", body, payload)
	}

	// Range GET — id3Reader uses bytes=0-N for front-chunk tag parse.
	req = httptest.NewRequest(http.MethodGet, "/media/"+filepath.ToSlash(rel), nil)
	req.Header.Set("Range", "bytes=0-7")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent {
		t.Fatalf("Range /media: status=%d body=%q", w.Code, w.Body.String())
	}
	partial, _ := io.ReadAll(w.Body)
	if string(partial) != string(payload[:8]) {
		t.Fatalf("Range body=%q want %q", partial, payload[:8])
	}

	// Missing file → 404 (not SPA HTML)
	req = httptest.NewRequest(http.MethodGet, "/media/does-not-exist.mp3", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing media status=%d", w.Code)
	}
}

// TestSetup_SPAAndStaticAssets pins the README entry surface:
// /admin serves the Vite index, /static/dist/* serves hashed assets.
func TestSetup_SPAAndStaticAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	musicDir := t.TempDir()
	staticDir := t.TempDir()
	dist := filepath.Join(staticDir, "dist")
	assets := filepath.Join(dist, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	index := "<!doctype html><html><body>spa-ok</body></html>"
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	assetBody := "console.log('asset')"
	if err := os.WriteFile(filepath.Join(assets, "app.js"), []byte(assetBody), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STATIC_DIR", staticDir)

	cfg := &config.Config{MusicDir: musicDir, JWTSecret: "test-secret"}
	r := gin.New()
	Setup(r, cfg, nil)

	// /admin/anything is served via NoRoute (cannot register /admin/*path
	// because /admin/login/ already occupies that tree segment).
	for _, path := range []string{"/", "/admin", "/admin/anything"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "spa-ok") {
			t.Fatalf("%s body missing spa marker: %q", path, w.Body.String())
		}
	}

	// Healthcheck must stay a bare 200, not the SPA document.
	req := httptest.NewRequest(http.MethodGet, "/admin/login/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/admin/login/ status=%d", w.Code)
	}
	if strings.Contains(w.Body.String(), "spa-ok") {
		t.Fatalf("/admin/login/ must not serve SPA HTML")
	}

	req = httptest.NewRequest(http.MethodGet, "/static/dist/assets/app.js", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/static asset status=%d", w.Code)
	}
	if w.Body.String() != assetBody {
		t.Fatalf("asset body=%q", w.Body.String())
	}

	// Unknown /api path stays JSON 404 (not SPA).
	req = httptest.NewRequest(http.MethodGet, "/api/does-not-exist/", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("/api miss status=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("/api miss body=%q", w.Body.String())
	}
}
