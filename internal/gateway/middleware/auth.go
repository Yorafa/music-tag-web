package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"go-music-tag/internal/config"
)

// JWT claim discriminator, mirrored from handler/auth.go where tokens are
// minted. Kept as local constants rather than imported so middleware does not
// depend on the handler package (the dependency runs the other way). The
// string values are part of the on-the-wire token contract and must stay in
// sync with handler.claimTokenType / tokenTypeRefresh.
const (
	claimTokenType   = "typ"
	tokenTypeRefresh = "refresh"
)

// JWTAuth checks JWT tokens in the Authorization header.
// Compatible with the Python simplejwt format:
//
//	Authorization: JWT <token>
//	Authorization: Bearer <token>
//	Authorization: jwt <token>
func JWTAuth(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			// Also check Cookie AUTHORIZATION (Python backend compat)
			cookie, _ := c.Cookie("AUTHORIZATION")
			if cookie != "" {
				header = "JWT " + cookie
			}
		}

		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"detail": "身份认证信息未提供",
			})
			return
		}

		// Parse "JWT <token>" or "Bearer <token>"
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "Invalid token format"})
			return
		}
		tokenStr := parts[1]

		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			// Accept HS256 used by simplejwt
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(cfg.JWTSecret), nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "Invalid token"})
			return
		}

		// A refresh token must not authenticate an API request. Login mints a
		// short-lived access token AND a 7-day refresh token (handler.Login);
		// the refresh token is only meant to be redeemed at
		// POST /api/token/refresh/ for a new access token. If this gate accepted
		// it as a bearer credential, the 2h/7d split would buy nothing — a leaked
		// refresh token would grant a week of full API access, which is exactly
		// what the split exists to prevent. handler.VerifyToken already refuses
		// refresh tokens for the same reason; this is the check on the path that
		// actually guards every protected route.
		//
		// Semantics match VerifyToken: reject only an explicit "refresh" typ.
		// Access tokens (typ="access") and legacy tokens minted before the split
		// (no typ) pass — the latter means at most one re-login after upgrade,
		// which is the documented migration behaviour.
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			if typ, _ := claims[claimTokenType].(string); typ == tokenTypeRefresh {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "Refresh token cannot be used as an access token"})
				return
			}
		}

		c.Next()
	}
}
