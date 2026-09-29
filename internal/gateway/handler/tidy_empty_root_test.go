package handler_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"

	"go-music-tag/internal/gateway/handler"
	"go-music-tag/internal/taskclient"
)

// tidyWithRedis points the task queue at an in-process Redis so the
// handler gets as far as the enqueue, which is the only way to tell "the
// request was refused" from "the request was accepted".
func tidyWithRedis(t *testing.T) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	t.Setenv("REDIS_ADDR", mr.Addr())
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "0")
	taskclient.Reset()
	t.Cleanup(taskclient.Reset)
}

func tidyPost(t *testing.T, body string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/tidy_folder/", handler.TidyFolder)
	req := httptest.NewRequest("POST", "/api/tidy_folder/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

// The dialog sends no root_path at all by default, so the binding layer
// has to accept its absence. This used to be binding:"required", which
// rejected the common case before the rule that interprets it ever ran.
func TestTidyFolder_AcceptsAMissingRootPath(t *testing.T) {
	tidyWithRedis(t)
	t.Setenv("MUSIC_DIR", t.TempDir())

	// No root_path key at all, and an empty one — both must be the
	// library root rather than a 400 about a required field.
	for _, body := range []string{
		`{"music_paths":["a/x.mp3"],"segments":["${artist}"]}`,
		`{"music_paths":["a/x.mp3"],"root_path":"","segments":["${artist}"]}`,
	} {
		if got := tidyPost(t, body); !strings.Contains(got, `"result":true`) {
			t.Errorf("body %s was refused: %s", body, got)
		}
	}
}

// The strictness that is NOT going away: a root the user actually typed
// is still checked against the library, and the refusal happens here
// rather than in the worker — by then the UI has already toasted success.
func TestTidyFolder_StillRefusesARootOutsideTheLibrary(t *testing.T) {
	tidyWithRedis(t)
	t.Setenv("MUSIC_DIR", t.TempDir())

	got := tidyPost(t, `{"music_paths":["a/x.mp3"],"root_path":"/etc","segments":["${artist}"]}`)
	if !strings.Contains(got, `"result":false`) {
		t.Errorf("a root outside the library was accepted: %s", got)
	}
	if !strings.Contains(got, "不在曲库内") {
		t.Errorf("the refusal does not say why: %s", got)
	}
}
