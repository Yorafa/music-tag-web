package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// newBodyRouter wires a router that reads the whole body and echoes the
// number of bytes consumed. Used by BodyLimit tests to confirm the
// middleware caps how many bytes the handler can pull through `c.Request.Body`.
func newBodyRouter(limit int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/echo", BodyLimit(limit), func(c *gin.Context) {
		buf := bytes.Buffer{}
		n, err := buf.ReadFrom(c.Request.Body)
		if err != nil {
			c.JSON(400, gin.H{"err": err.Error()})
			return
		}
		c.JSON(200, gin.H{"bytes": strconv.FormatInt(n, 10)})
	})
	return r
}

func TestBodyLimit_AllowsSmallPayload(t *testing.T) {
	r := newBodyRouter(DefaultBodyLimitBytes)
	body := strings.Repeat("a", 1024) // 1 KiB
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader([]byte(body)))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestBodyLimit_RejectsOversizedPayload(t *testing.T) {
	// Use a smaller cap so the test stays cheap.
	const cap = int64(2 << 20) // 2 MiB
	r := newBodyRouter(cap)
	body := strings.Repeat("a", int(cap)+1024) // 1 KiB past the cap
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader([]byte(body)))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Gin returns 400 from the handler (c.Request.Body Read errors bubble
	// up). We don't care between 400 and 413 specifically — the
	// invariant is "NOT 200".
	if w.Code == 200 {
		t.Fatalf("oversized body got 200; body=%s", w.Body.String())
	}
	if w.Code < 400 {
		t.Fatalf("status = %d; expected 4xx for oversized body", w.Code)
	}
}

func TestBodyLimit_DefaultCapUsedWhenZero(t *testing.T) {
	if DefaultBodyLimitBytes != 8<<20 {
		t.Fatalf("DefaultBodyLimitBytes = %d, want %d (8 MiB)", DefaultBodyLimitBytes, 8<<20)
	}
	r := newBodyRouter(0) // 0 → default cap
	body := strings.Repeat("a", 1024)
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("default-cap small payload status = %d", w.Code)
	}
}
