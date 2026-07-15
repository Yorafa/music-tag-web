package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/config"
)

// newWebhookRouter mounts a single POST /hook route guarded by WebhookAuth.
func newWebhookRouter(t *testing.T, cfg *config.Config) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/hook", WebhookAuth(cfg), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})
	return r
}

// withEnv isolates env mutations per test (other tests in the suite may
// run in parallel with t.Setenv — Go 1.17+).
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	orig := map[string]string{}
	for k, v := range kv {
		orig[k] = os.Getenv(k)
		t.Setenv(k, v)
	}
	t.Cleanup(func() {
		for k, v := range orig {
			os.Setenv(k, v)
		}
	})
}

func TestWebhookAuth_Production_RejectsUnconfigured(t *testing.T) {
	withEnv(t, map[string]string{"ALLOW_INSECURE_DEFAULTS": ""})
	cfg := &config.Config{WebhookInternalToken: ""}
	r := newWebhookRouter(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/hook", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (fail-closed in production)", w.Code)
	}
}

func TestWebhookAuth_Production_AcceptsMatchingToken(t *testing.T) {
	withEnv(t, map[string]string{"ALLOW_INSECURE_DEFAULTS": ""})
	cfg := &config.Config{WebhookInternalToken: "correct-horse-battery-staple"}
	r := newWebhookRouter(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/hook", nil)
	req.Header.Set(WebhookAuthInternalHeader, "correct-horse-battery-staple")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestWebhookAuth_Production_RejectsMismatchToken(t *testing.T) {
	withEnv(t, map[string]string{"ALLOW_INSECURE_DEFAULTS": ""})
	cfg := &config.Config{WebhookInternalToken: "tok"}
	r := newWebhookRouter(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/hook", nil)
	req.Header.Set(WebhookAuthInternalHeader, "wrong-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestWebhookAuth_Production_RejectsMissingHeaderWithTokenSet(t *testing.T) {
	withEnv(t, map[string]string{"ALLOW_INSECURE_DEFAULTS": ""})
	cfg := &config.Config{WebhookInternalToken: "tok"}
	r := newWebhookRouter(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/hook", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when header missing", w.Code)
	}
}

func TestWebhookAuth_DevFallback_AllowsUnconfiguredWhenInsecure(t *testing.T) {
	withEnv(t, map[string]string{"ALLOW_INSECURE_DEFAULTS": "1"})
	cfg := &config.Config{WebhookInternalToken: ""}
	r := newWebhookRouter(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/hook", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (dev fallback permitted)", w.Code)
	}
}

func TestWebhookAuth_LengthMismatchShortCircuits(t *testing.T) {
	withEnv(t, map[string]string{"ALLOW_INSECURE_DEFAULTS": ""})
	cfg := &config.Config{WebhookInternalToken: "abcdef"}
	r := newWebhookRouter(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/hook", nil)
	req.Header.Set(WebhookAuthInternalHeader, "x") // shorter than config
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (different-length should still reject)", w.Code)
	}
}
