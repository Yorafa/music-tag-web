package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"go-music-tag/internal/config"
)

const jwtTestSecret = "middleware-jwt-auth-test-secret-not-a-placeholder"

// newJWTRouter mounts a single GET /guarded route behind JWTAuth so the specs
// can assert exactly what the gate on every protected route lets through.
func newJWTRouter(t *testing.T, cfg *config.Config) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/guarded", JWTAuth(cfg), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// mintToken signs an HS256 token with the given typ claim ("" omits the claim,
// standing in for a legacy token minted before the access/refresh split).
func mintToken(t *testing.T, secret, typ string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub": "admin",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	if typ != "" {
		claims[claimTokenType] = typ
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func doGuarded(t *testing.T, r *gin.Engine, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/guarded", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestJWTAuth_RejectsRefreshToken pins REVIEW.md P2-8. The refresh token is
// only meant to be redeemed at /api/token/refresh/; if the route guard accepts
// it as a bearer credential the 2h/7d access/refresh split buys nothing — a
// leaked 7-day refresh token would grant a week of full API access.
func TestJWTAuth_RejectsRefreshToken(t *testing.T) {
	cfg := &config.Config{JWTSecret: jwtTestSecret}
	r := newJWTRouter(t, cfg)
	w := doGuarded(t, r, "Bearer "+mintToken(t, jwtTestSecret, tokenTypeRefresh))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a refresh token; body=%s", w.Code, w.Body.String())
	}
}

// TestJWTAuth_AcceptsAccessToken is the other half of the split: the token the
// browser attaches to every request must be honoured.
func TestJWTAuth_AcceptsAccessToken(t *testing.T) {
	cfg := &config.Config{JWTSecret: jwtTestSecret}
	r := newJWTRouter(t, cfg)
	w := doGuarded(t, r, "Bearer "+mintToken(t, jwtTestSecret, "access"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an access token; body=%s", w.Code, w.Body.String())
	}
}

// TestJWTAuth_AcceptsLegacyTokenWithoutType documents the migration behaviour:
// tokens minted before the split carry no typ claim and must still pass, so an
// upgrade costs at most one re-login rather than logging everyone out.
func TestJWTAuth_AcceptsLegacyTokenWithoutType(t *testing.T) {
	cfg := &config.Config{JWTSecret: jwtTestSecret}
	r := newJWTRouter(t, cfg)
	w := doGuarded(t, r, "JWT "+mintToken(t, jwtTestSecret, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a legacy (no-typ) token; body=%s", w.Code, w.Body.String())
	}
}

// TestJWTAuth_RejectsWrongSecret guards the signature check the typ gate sits
// behind — a token signed with another key must never reach the typ logic.
func TestJWTAuth_RejectsWrongSecret(t *testing.T) {
	cfg := &config.Config{JWTSecret: jwtTestSecret}
	r := newJWTRouter(t, cfg)
	w := doGuarded(t, r, "Bearer "+mintToken(t, "a-completely-different-secret", "access"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a token signed with the wrong secret", w.Code)
	}
}

// TestJWTAuth_RejectsMissingHeader keeps the unauthenticated path fail-closed.
func TestJWTAuth_RejectsMissingHeader(t *testing.T) {
	cfg := &config.Config{JWTSecret: jwtTestSecret}
	r := newJWTRouter(t, cfg)
	w := doGuarded(t, r, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when no Authorization header is present", w.Code)
	}
}
