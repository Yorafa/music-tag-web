package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"go-music-tag/internal/config"
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

		c.Next()
	}
}
