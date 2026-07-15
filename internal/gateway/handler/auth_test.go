package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// b64URL is a tiny stdlib helper — base64-url-with-no-padding, the
// encoding JWT tokens use.
func b64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func newAuthRouter(t *testing.T) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/token/", Login)
	r.POST("/api/token/refresh/", RefreshToken)
	r.POST("/api/token/verify/", VerifyToken)
	return r
}

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

// ─── M1: JWT alg gate ──────────────────────────────────────────────────────

// craftAlgNoneToken hand-rolls a {"alg":"none"} JWT with no signature.
// The jwt/v5 parser used to accept this and surface the unsigned claims;
// our jwtKeyFunc now rejects it via the SigningMethodHMAC type assertion.
func craftAlgNoneToken(subject string) string {
	header := b64URL([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := b64URL([]byte(fmt.Sprintf(
		`{"sub":%q,"iat":%d,"exp":%d}`,
		subject, time.Now().Unix(), time.Now().Add(time.Hour).Unix(),
	)))
	return header + "." + payload + "." // empty signature segment
}

// craftHS256Token signs an HS256 JWT using the given key (used by both
// happy-path tests AND alg-mismatch tests so we can isolate the alg gate
// from key validity).
func craftHS256Token(secret []byte, method jwt.SigningMethod, subject string) string {
	t := jwt.NewWithClaims(method, jwt.MapClaims{
		"sub": subject,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := t.SignedString(secret)
	if err != nil {
		panic(err)
	}
	return signed
}

func TestRefreshToken_RejectsAlgNone(t *testing.T) {
	withEnv(t, map[string]string{
		"JWT_SECRET":              "unit-test-secret-do-not-use",
		"ALLOW_INSECURE_DEFAULTS": "",
	})
	r := newAuthRouter(t)
	body, _ := json.Marshal(map[string]string{"refresh": craftAlgNoneToken("attacker")})
	req := httptest.NewRequest(http.MethodPost, "/api/token/refresh/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("alg=none token accepted by RefreshToken; status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVerifyToken_RejectsAlgNone(t *testing.T) {
	withEnv(t, map[string]string{
		"JWT_SECRET":              "unit-test-secret-do-not-use",
		"ALLOW_INSECURE_DEFAULTS": "",
	})
	r := newAuthRouter(t)
	body, _ := json.Marshal(map[string]string{"token": craftAlgNoneToken("attacker")})
	req := httptest.NewRequest(http.MethodPost, "/api/token/verify/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("alg=none accepted by VerifyToken; status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRefreshToken_AcceptsValidHS256(t *testing.T) {
	withEnv(t, map[string]string{
		"JWT_SECRET":              "unit-test-secret-do-not-use",
		"ALLOW_INSECURE_DEFAULTS": "",
	})
	r := newAuthRouter(t)
	tok := craftHS256Token([]byte("unit-test-secret-do-not-use"), jwt.SigningMethodHS256, "alice")
	body, _ := json.Marshal(map[string]string{"refresh": tok})
	req := httptest.NewRequest(http.MethodPost, "/api/token/refresh/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("valid HS256 rejected; status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestRefreshToken_RejectsForeignAlg covers the alg-gate rejecting
// *any* non-HMAC signing method, not just the historical "none" attack.
func TestRefreshToken_RejectsForeignAlg(t *testing.T) {
	withEnv(t, map[string]string{
		"JWT_SECRET":              "unit-test-secret-do-not-use",
		"ALLOW_INSECURE_DEFAULTS": "",
	})

	// Construct a header that claims RS256 but the body is just hash of
	// empty (we never verify the second part — the gate fails on header
	// alone).
	header := b64URL([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := b64URL([]byte(fmt.Sprintf(
		`{"sub":"x","iat":%d,"exp":%d}`,
		time.Now().Unix(), time.Now().Add(time.Hour).Unix(),
	)))
	body := header + "." + payload + "." + "fake-signature"

	r := newAuthRouter(t)
	jb, _ := json.Marshal(map[string]string{"refresh": body})
	req := httptest.NewRequest(http.MethodPost, "/api/token/refresh/", bytes.NewReader(jb))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("RS256 signed token accepted by HMAC keyfunc; status=%d body=%s",
			w.Code, w.Body.String())
	}
}

// TestRefreshToken_RejectsHS256WithWrongSecret is the negative case: even
// when the alg is correct, a wrong-key token must be refused — proving
// we're not just rubber-stamping HMAC tokens.
func TestRefreshToken_RejectsHS256WithWrongSecret(t *testing.T) {
	withEnv(t, map[string]string{
		"JWT_SECRET":              "real-secret",
		"ALLOW_INSECURE_DEFAULTS": "",
	})
	tok := craftHS256Token([]byte("different-secret"), jwt.SigningMethodHS256, "alice")
	r := newAuthRouter(t)
	body, _ := json.Marshal(map[string]string{"refresh": tok})
	req := httptest.NewRequest(http.MethodPost, "/api/token/refresh/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-key HS256 refresh accepted; status=%d body=%s", w.Code, w.Body.String())
	}
}

// ─── M2: bcrypt + constant-time compare ────────────────────────────────────

func TestLogin_AcceptsBcryptCredential(t *testing.T) {
	h, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}
	withEnv(t, map[string]string{
		"JWT_SECRET":              "unit-test-secret-do-not-use",
		"ALLOW_INSECURE_DEFAULTS": "",
		"ADMIN_USERS":             "alice:" + string(h),
	})

	r := newAuthRouter(t)
	body, _ := json.Marshal(map[string]string{"username": "alice", "password": "correct-password"})
	req := httptest.NewRequest(http.MethodPost, "/api/token/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bcrypt valid login rejected; status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"access"`) {
		t.Errorf("response should include access token; got %s", w.Body.String())
	}
}

func TestLogin_RejectsWrongBcryptCredential(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	withEnv(t, map[string]string{
		"JWT_SECRET":              "x",
		"ALLOW_INSECURE_DEFAULTS": "",
		"ADMIN_USERS":             "alice:" + string(h),
	})

	r := newAuthRouter(t)
	body, _ := json.Marshal(map[string]string{"username": "alice", "password": "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/token/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-bcrypt login accepted; status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLogin_AcceptsPlainCredential(t *testing.T) {
	withEnv(t, map[string]string{
		"JWT_SECRET":              "x",
		"ALLOW_INSECURE_DEFAULTS": "",
		"ADMIN_USERS":             "bob:plain-dev-pwd",
	})

	r := newAuthRouter(t)
	body, _ := json.Marshal(map[string]string{"username": "bob", "password": "plain-dev-pwd"})
	req := httptest.NewRequest(http.MethodPost, "/api/token/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("plain valid login rejected; status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLogin_ConstantTimeRejectsWrongPlain(t *testing.T) {
	withEnv(t, map[string]string{
		"JWT_SECRET":              "x",
		"ALLOW_INSECURE_DEFAULTS": "",
		"ADMIN_USERS":             "bob:plain-dev-pwd",
	})

	r := newAuthRouter(t)
	body, _ := json.Marshal(map[string]string{"username": "bob", "password": "wrong-pwd"})
	req := httptest.NewRequest(http.MethodPost, "/api/token/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-plain login accepted; status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestLogin_LoadUsersBcryptPrefixDetection ensures the $2 prefix switch
// means ADMIN_USERS=alice:$2-anything (no real hash) is treated as bcrypt.
// The bcrypt library will reject the bogus input so Login must 401.
func TestLogin_LoadUsersBcryptPrefixDetection(t *testing.T) {
	withEnv(t, map[string]string{
		"JWT_SECRET":              "x",
		"ALLOW_INSECURE_DEFAULTS": "",
		"ADMIN_USERS":             "alice:$2notarealhash",
	})
	r := newAuthRouter(t)
	body, _ := json.Marshal(map[string]string{"username": "alice", "password": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/token/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("malformed-bcrypt login should reject; status=%d body=%s", w.Code, w.Body.String())
	}
}

// ─── Sanity check: loadUsers parsing edge cases ─────────────────────────────

func TestLoadUsers_ParsesMultipleCommaEntries(t *testing.T) {
	t.Setenv("ADMIN_USERS", "alice:plain,bob:$2a$10$abc, empty: ,, carol:plain")
	t.Setenv("ALLOW_INSECURE_DEFAULTS", "")
	users := loadUsers()
	if _, ok := users["alice"]; !ok || users["alice"].isBcrypt {
		t.Errorf("alice parsed wrong: %+v", users["alice"])
	}
	if _, ok := users["bob"]; !ok || !users["bob"].isBcrypt {
		t.Errorf("bob parsed wrong: %+v", users["bob"])
	}
	if _, ok := users["carol"]; !ok {
		t.Errorf("carol missing; empty segments should be skipped, not crash")
	}
	if len(users) != 3 {
		t.Errorf("expected 3 valid entries, got %d: %+v", len(users), users)
	}
}

// TestLoadUsers_EmptyInDev seeds the dev-default admin/admin path so a
// future regression of the IsInsecureDevDefaults branch would crash.
func TestLoadUsers_EmptyInDev(t *testing.T) {
	t.Setenv("ADMIN_USERS", "")
	t.Setenv("ALLOW_INSECURE_DEFAULTS", "1")
	users := loadUsers()
	u, ok := users["admin"]
	if !ok {
		t.Fatalf("dev default admin entry missing")
	}
	if u.isBcrypt || u.hash != "admin" {
		t.Errorf("dev default wrong: %+v", u)
	}
}

// TestLoadUsers_EmptyInProd is fail-closed: empty map.
func TestLoadUsers_EmptyInProd(t *testing.T) {
	t.Setenv("ADMIN_USERS", "")
	t.Setenv("ALLOW_INSECURE_DEFAULTS", "")
	if users := loadUsers(); len(users) != 0 {
		t.Errorf("empty-prod should be fail-closed; got %+v", users)
	}
}

// ─── end of test file ──────────────────────────────────────────────────────
