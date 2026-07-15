package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/config"
)

func setupRouter(cfg *config.Config) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(cfg))
	r.GET("/any", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func TestCORS_AllowsWhitelistedOriginWithCredentials(t *testing.T) {
	cfg := &config.Config{
		CORSAllowedOrigins: []string{"https://music.example.com"},
	}
	r := setupRouter(cfg)
	req := httptest.NewRequest(http.MethodGet, "/any", nil)
	req.Header.Set("Origin", "https://music.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://music.example.com" {
		t.Errorf("Allow-Origin = %q, want https://music.example.com", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true", got)
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
}

func TestCORS_RejectsUnknownOrigin(t *testing.T) {
	cfg := &config.Config{
		CORSAllowedOrigins: []string{"https://music.example.com"},
	}
	r := setupRouter(cfg)
	req := httptest.NewRequest(http.MethodGet, "/any", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin should be empty for unknown origin; got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials should be empty for unknown origin; got %q", got)
	}
}

func TestCORS_EmptyWhitelistBlocksAllOrigins(t *testing.T) {
	cfg := &config.Config{CORSAllowedOrigins: nil}
	r := setupRouter(cfg)
	req := httptest.NewRequest(http.MethodGet, "/any", nil)
	req.Header.Set("Origin", "https://anywhere.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("empty whitelist should produce empty Allow-Origin; got %q", got)
	}
}

func TestCORS_PreflightWhitelistedReturns204(t *testing.T) {
	cfg := &config.Config{
		CORSAllowedOrigins: []string{"https://music.example.com"},
	}
	r := setupRouter(cfg)
	req := httptest.NewRequest(http.MethodOptions, "/any", nil)
	req.Header.Set("Origin", "https://music.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://music.example.com" {
		t.Errorf("preflight Allow-Origin = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("preflight Cache-Control = %q, want no-store", got)
	}
}

func TestCORS_PreflightUnknownOrigin204NoCache(t *testing.T) {
	// Even on preflight to a non-whitelisted origin, no-store must still
	// apply so intermediaries don't cache rejected preflights and replay
	// them later after the operator rotates the whitelist.
	cfg := &config.Config{
		CORSAllowedOrigins: []string{"https://music.example.com"},
	}
	r := setupRouter(cfg)
	req := httptest.NewRequest(http.MethodOptions, "/any", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (preflight always answered)", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("rejected preflight Cache-Control = %q, want no-store", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("rejected preflight should not echo Origin; got %q", got)
	}
}

func TestCORS_NilCfgPreflightStill204NoCache(t *testing.T) {
	r := setupRouter(nil)
	req := httptest.NewRequest(http.MethodOptions, "/any", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("nil-cfg preflight should still 204; got %d", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("nil-cfg preflight Cache-Control = %q, want no-store", got)
	}
}

func TestCORS_NoOriginHeaderOmitsHeaders(t *testing.T) {
	// Same-origin request (no Origin) — middleware should not force any CORS
	// header. This preserves behaviour when curl / server-to-server calls
	// bypass CORS entirely.
	cfg := &config.Config{
		CORSAllowedOrigins: []string{"https://music.example.com"},
	}
	r := setupRouter(cfg)
	req := httptest.NewRequest(http.MethodGet, "/any", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("no Origin header should not produce Allow-Origin; got %q", got)
	}
}
