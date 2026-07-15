package handler

import (
	"crypto/subtle"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"go-music-tag/internal/config"
)

// bcryptHashPrefix is the magic prefix that flags the credential we read
// from ADMIN_USERS as a bcrypt hash rather than a plain string. Bcrypt
// versions 2a / 2b / 2y all share the leading "$2".
const bcryptHashPrefix = "$2"

// storedCred is one row of ADMIN_USERS after parsing.
//
// `isBcrypt=true` means `hash` is a bcrypt digest and Login must run
// bcrypt.CompareHashAndPassword. `isBcrypt=false` means a plain-text
// dev-friendly password that we compare constant-time. Production
// deployments are expected to ship only bcrypt rows; the plain path is
// preserved so existing dev rigs (ADMIN_USERS=admin:admin) keep working
// without change.
type storedCred struct {
	hash     string
	isBcrypt bool
}

// loadUsers reads credentials from env. Format:
//
//	ADMIN_USERS=alice:$2a$10$...,bob:$2b$10$...           (production)
//	ADMIN_USERS=admin:admin                                (dev)
//
// SECURITY (P1.5 issue F):
//
//   - The admin/admin dev default only applies when ALLOW_INSECURE_DEFAULTS=1.
//     Without that opt-in, an unset ADMIN_USERS produces an empty map so
//     every Login fails-closed.
//
//   - Bcrypt rows are recognised by a leading "$2" prefix. Anything else
//     is treated as plain text — operators must take care to use bcrypt
//     in production (e.g. `htpasswd -bnBC 10 "" "secret" | tr -d ':\n'`
//     and slice off the username prefix).
//
//   - config.Load() is expected to log.Fatalf at startup when JWT_SECRET
//     is the placeholder value; we re-check here so the request refuses
//     explicitly rather than minting tokens that trust an
//     attacker-controlled secret.
func loadUsers() map[string]storedCred {
	raw := os.Getenv("ADMIN_USERS")
	if raw == "" {
		if config.IsInsecureDevDefaults() {
			log.Printf("[auth] WARNING: ADMIN_USERS unset; using dev admin/admin (only because ALLOW_INSECURE_DEFAULTS=1)")
			return map[string]storedCred{"admin": {hash: "admin", isBcrypt: false}}
		}
		return map[string]storedCred{}
	}
	users := map[string]storedCred{}
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		user := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if user == "" || val == "" {
			continue
		}
		users[user] = storedCred{
			hash:     val,
			isBcrypt: strings.HasPrefix(val, bcryptHashPrefix),
		}
	}
	return users
}

// verifyCred does the actual password check in constant time where
// possible:
//
//   - bcrypt hash: bcrypt.CompareHashAndPassword is itself constant-time
//     across the presented password (the comparison runs over a derived
//     key, byte-wise XOR).
//
//   - plain text: subtle.ConstantTimeCompare returns 0 when lengths
//     differ. This is uniform across attackers picking any "wrong"
//     length, so no timing leak on the mismatch path. Length equal but
//     value different still takes constant time on shared length bytes.
func verifyCred(stored storedCred, presented string) bool {
	if stored.isBcrypt {
		return bcrypt.CompareHashAndPassword([]byte(stored.hash), []byte(presented)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(stored.hash), []byte(presented)) == 1
}

// jwtKeyFunc is shared by RefreshToken + VerifyToken. It enforces:
//
//   • Only HMAC-family methods are accepted (rejects "none" and any
//     asymmetric alg trivially). P1.5 issue F (M1).
//   • Returns the configured secret.
//
// Errors are intentionally non-specific to avoid leaking which check failed.
func jwtKeyFunc(secret string) jwt.Keyfunc {
	return func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	}
}

// Login handles POST /api/token/ — returns JWT access + refresh tokens.
// Matches Python simplejwt format: {"access": "<token>", "refresh": "<token>"}
func Login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"detail": "Invalid input"})
		return
	}

	if config.JWTSecretIsDefault() {
		log.Printf("[auth] REFUSED /api/token/: JWT_SECRET is the placeholder value")
		c.JSON(401, gin.H{"detail": "用户名或密码错误"})
		return
	}

	users := loadUsers()
	cred, ok := users[req.Username]
	if !ok || !verifyCred(cred, req.Password) {
		c.JSON(401, gin.H{"detail": "用户名或密码错误"})
		return
	}

	cfg := config.Load()

	accessToken, _ := generateJWT(cfg.JWTSecret, req.Username)
	refreshToken, _ := generateJWT(cfg.JWTSecret, req.Username)

	c.JSON(200, gin.H{
		"access":  accessToken,
		"refresh": refreshToken,
	})
}

// RefreshToken handles POST /api/token/refresh/
func RefreshToken(c *gin.Context) {
	var req struct {
		Refresh string `json:"refresh"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"detail": "Invalid input"})
		return
	}
	cfg := config.Load()
	if config.JWTSecretIsDefault() {
		c.JSON(401, gin.H{"detail": "Invalid refresh token"})
		return
	}
	token, err := jwt.Parse(req.Refresh, jwtKeyFunc(cfg.JWTSecret))
	if err != nil || !token.Valid {
		c.JSON(401, gin.H{"detail": "Invalid refresh token"})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.JSON(401, gin.H{"detail": "Invalid token claims"})
		return
	}
	username, _ := claims["sub"].(string)
	access, _ := generateJWT(cfg.JWTSecret, username)
	c.JSON(200, gin.H{"access": access})
}

// VerifyToken handles POST /api/token/verify/
func VerifyToken(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"detail": "Invalid input"})
		return
	}
	cfg := config.Load()
	if config.JWTSecretIsDefault() {
		c.JSON(401, gin.H{"detail": "Invalid token"})
		return
	}
	token, err := jwt.Parse(req.Token, jwtKeyFunc(cfg.JWTSecret))
	if err != nil || !token.Valid {
		c.JSON(401, gin.H{"detail": "Invalid token"})
		return
	}
	c.JSON(200, gin.H{})
}

func generateJWT(secret, username string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": username,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(7 * 24 * time.Hour).Unix(), // 7 days, matching Python
	})
	return token.SignedString([]byte(secret))
}
