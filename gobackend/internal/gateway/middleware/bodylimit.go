package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// DefaultBodyLimitBytes is the default request-body cap applied by
// BodyLimit. 8 MiB comfortably fits the largest JSON payload the gateway
// accepts (batch id3 updates cover hundreds of files per request) while
// refusing pathological uploads that would otherwise OOM the gateway.
const DefaultBodyLimitBytes int64 = 8 << 20 // 8 MiB

// BodyLimit caps any request body to `n` bytes using http.MaxBytesReader.
// When the body exceeds the cap, the wrapped Read returns an error which
// Gin returns as HTTP 413 Request Entity Too Large.
//
// Wiring:
//
//	api := r.Group("/api")
//	api.Use(BodyLimit(0)) // 0 → DefaultBodyLimitBytes
//
// We intentionally do NOT read up-front; MaxBytesReader is a streaming
// limit so memory usage stays proportional to actual consumption rather
// than request size.
func BodyLimit(n int64) gin.HandlerFunc {
	if n <= 0 {
		n = DefaultBodyLimitBytes
	}
	return func(c *gin.Context) {
		if c.Request != nil && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		}
		c.Next()
	}
}
