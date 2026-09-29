package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// DefaultBodyLimitBytes is the default request-body cap applied by
// BodyLimit. 28 MiB reconciles with the 20 MiB cover cap
// (maxUploadImageBytes / fetchRemoteBytes): a cover rides in the
// update_id3 JSON body as base64, which inflates the raw bytes by ~33%, so
// a 20 MiB cover is ~27 MiB on the wire. 28 MiB leaves headroom for that
// plus the surrounding tag JSON, so any cover that upload_image accepts can
// actually be saved — the 8 MiB cap used to reset that save mid-upload,
// surfacing as a bare axios "Network Error". It still refuses the
// pathological payloads that would otherwise OOM the gateway.
const DefaultBodyLimitBytes int64 = 28 << 20 // 28 MiB

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
