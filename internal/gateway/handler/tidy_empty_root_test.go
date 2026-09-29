package handler_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/gateway/handler"
)

// The dialog sends no root_path at all by default, so the binding layer
// has to accept its absence. This used to be binding:"required", which
// rejected the common case before the rule that interprets it ever ran.
func TestTidyFolder_AcceptsAMissingRootPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("MUSIC_DIR", t.TempDir())

	r := gin.New()
	r.POST("/api/tidy_folder/preview/", handler.PreviewTidyFolder)

	// No root_path key at all, and an empty one — both must be the
	// library root rather than a 400 about a required field.
	for _, body := range []string{
		`{"music_paths":["a/x.mp3"],"segments":["${artist}"]}`,
		`{"music_paths":["a/x.mp3"],"root_path":"","segments":["${artist}"]}`,
	} {
		req := httptest.NewRequest("POST", "/api/tidy_folder/preview/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if !strings.Contains(w.Body.String(), `"result":true`) {
			t.Errorf("body %s was refused: %s", body, w.Body.String())
		}
	}
}

// The strictness that is NOT going away: a root the user actually typed
// is still checked against the library.
func TestTidyFolder_StillRefusesARootOutsideTheLibrary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("MUSIC_DIR", t.TempDir())

	r := gin.New()
	r.POST("/api/tidy_folder/preview/", handler.PreviewTidyFolder)

	req := httptest.NewRequest("POST", "/api/tidy_folder/preview/",
		strings.NewReader(`{"music_paths":["a/x.mp3"],"root_path":"/etc","segments":["${artist}"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `"result":false`) {
		t.Errorf("a root outside the library was accepted: %s", w.Body.String())
	}
}
