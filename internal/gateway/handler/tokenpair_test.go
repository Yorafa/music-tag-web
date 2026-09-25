package handler

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "token-split-test-secret-not-a-placeholder"

// parseClaims is a tiny helper so the specs can read what a token carries
// without going through an HTTP round-trip.
func parseClaims(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	parsed, err := jwt.Parse(token, func(tok *jwt.Token) (interface{}, error) {
		return []byte(testSecret), nil
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("claims type = %T", parsed.Claims)
	}
	return claims
}

// TestAccessAndRefreshTokensDiffer pins REVIEW.md P2-8. Both tokens used
// to come from the same generateJWT(secret, username) call with the same
// 7-day TTL and no type claim, so they were byte-identical in every field
// and the refresh endpoint provided no additional security boundary.
func TestAccessAndRefreshTokensDiffer(t *testing.T) {
	access, err := generateAccessToken(testSecret, "admin")
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := generateRefreshToken(testSecret, "admin")
	if err != nil {
		t.Fatal(err)
	}

	if access == refresh {
		t.Fatal("access and refresh tokens are byte-identical — they are the same token")
	}

	ac := parseClaims(t, access)
	rc := parseClaims(t, refresh)

	if got := ac[claimTokenType]; got != tokenTypeAccess {
		t.Errorf("access typ = %v, want %q", got, tokenTypeAccess)
	}
	if got := rc[claimTokenType]; got != tokenTypeRefresh {
		t.Errorf("refresh typ = %v, want %q", got, tokenTypeRefresh)
	}
	if ac["sub"] != rc["sub"] {
		t.Errorf("subjects differ: %v vs %v", ac["sub"], rc["sub"])
	}
}

// TestAccessTokenIsShortLived pins the lifetime split. A week-long access
// token is what made the earlier equivalence damaging: the token the
// browser attaches to every request stayed valid for a week.
func TestAccessTokenIsShortLived(t *testing.T) {
	access, err := generateAccessToken(testSecret, "admin")
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := generateRefreshToken(testSecret, "admin")
	if err != nil {
		t.Fatal(err)
	}

	ac := parseClaims(t, access)
	rc := parseClaims(t, refresh)

	ai, ok1 := ac["iat"].(float64)
	ri, ok2 := rc["iat"].(float64)
	if !ok1 || !ok2 {
		t.Fatalf("iat not numeric: access=%T refresh=%T", ac["iat"], rc["iat"])
	}
	aexp, ok1 := ac["exp"].(float64)
	rexp, ok2 := rc["exp"].(float64)
	if !ok1 || !ok2 {
		t.Fatalf("exp not numeric: access=%T refresh=%T", ac["exp"], rc["exp"])
	}

	accessTTL := time.Duration(aexp-ai) * time.Second
	refreshTTL := time.Duration(rexp-ri) * time.Second

	if accessTTL != accessTokenTTL {
		t.Errorf("access TTL = %v, want %v", accessTTL, accessTokenTTL)
	}
	if refreshTTL != refreshTokenTTL {
		t.Errorf("refresh TTL = %v, want %v", refreshTTL, refreshTokenTTL)
	}
	if accessTTL >= refreshTTL {
		t.Errorf("access TTL %v is not shorter than refresh TTL %v", accessTTL, refreshTTL)
	}
	// Belt and braces: the access token must actually have expired long
	// before the refresh one does.
	if accessTTL > 24*time.Hour {
		t.Errorf("access TTL %v is too long for a browser-attached bearer token", accessTTL)
	}
}

// TestRefreshTokenIsLongerLived is the mirror image, stated explicitly so a
// future "simplification" that equalises the TTLs fails here.
func TestRefreshTokenIsLongerLived(t *testing.T) {
	refresh, err := generateRefreshToken(testSecret, "admin")
	if err != nil {
		t.Fatal(err)
	}
	claims := parseClaims(t, refresh)
	iat := claims["iat"].(float64)
	exp := claims["exp"].(float64)
	if got := time.Duration(exp-iat) * time.Second; got < 24*time.Hour {
		t.Errorf("refresh TTL = %v; a refresh token shorter than a day is not usable in practice", got)
	}
}

// TestTokensAreSignedWithTheSameSubjectAndSecret confirms the split did
// not accidentally change who the tokens authenticate as.
func TestTokensAreSignedWithTheSameSubjectAndSecret(t *testing.T) {
	for _, gen := range []struct {
		name string
		fn   func(string, string) (string, error)
	}{
		{"access", generateAccessToken},
		{"refresh", generateRefreshToken},
	} {
		tok, err := gen.fn(testSecret, "alice")
		if err != nil {
			t.Fatalf("%s: %v", gen.name, err)
		}
		claims := parseClaims(t, tok)
		if claims["sub"] != "alice" {
			t.Errorf("%s sub = %v, want alice", gen.name, claims["sub"])
		}
	}
}

// TestAWrongSecretIsStillRejected guards against a mistake that would make
// the type-claim split meaningless (e.g. signing with a constant).
func TestAWrongSecretIsStillRejected(t *testing.T) {
	tok, err := generateAccessToken(testSecret, "admin")
	if err != nil {
		t.Fatal(err)
	}
	other, err := generateAccessToken("a-completely-different-secret", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if tok == other {
		t.Fatal("tokens signed with different secrets are identical")
	}
	if _, err := jwt.Parse(tok, func(*jwt.Token) (interface{}, error) {
		return []byte("a-completely-different-secret"), nil
	}); err == nil {
		t.Fatal("token verified under the wrong secret")
	}
}
