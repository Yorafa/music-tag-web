package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/config"
)

// WebhookAuthInternalHeader is the HTTP header workers send to authenticate
// themselves when calling /api/webhooks/* endpoints. The value MUST equal
// cfg.WebhookInternalToken byte-for-byte.
const WebhookAuthInternalHeader = "X-Internal-Token"

// WebhookAuth gates /api/webhooks/* on a shared-secret header.
//
// Policy:
//
//   • cfg.WebhookInternalToken EMPTY + production (default): every webhook
//     request is rejected with 403. Operationally loud so a misconfigured
//     gateway cannot accept anonymous webhook traffic in production.
//
//   • cfg.WebhookInternalToken SET: subtle.ConstantTimeCompare against
//     the X-Internal-Token header. Length mismatch short-circuits to
//     false without raising a side-channel signal: subtle returns 0 when
//     the byte slices have different lengths (which is uniform across
//     attackers picking any "wrong" length).
//
//   • cfg.WebhookInternalToken EMPTY + ALLOW_INSECURE_DEFAULTS=1:
//     warn-and-skip (dev only). The dev-default switch is documented and
//     can never be true in production unless an operator explicitly opts
//     in via env.
func WebhookAuth(cfg *config.Config) gin.HandlerFunc {
	var token string
	if cfg != nil {
		token = cfg.WebhookInternalToken
	}
	return func(c *gin.Context) {
		if token == "" {
			if !config.IsInsecureDevDefaults() {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"detail": "webhook internal token not configured",
				})
				return
			}
			c.Next()
			return
		}
		presented := c.GetHeader(WebhookAuthInternalHeader)
		if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"detail": "webhook internal token mismatch",
			})
			return
		}
		c.Next()
	}
}
