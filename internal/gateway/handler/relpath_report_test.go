package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestBatchUpdateID3_ReportsRelativePaths is REVIEW.md P3-9 driven through
// the real handler.
//
// UpdateID3 echoed the relative path the client had sent; BatchUpdateID3
// echoed the absolute leaf it built internally from SafeJoin. Same
// `done[].file_full_path` field, two shapes, depending on which endpoint
// answered — so a caller that fed one answer into the other got a path
// rejected for being absolute. Nothing consumed the field, which is why it
// went unnoticed.
//
// The assertion is deliberately "would this value survive being sent back
// to update_id3/", because that is the property the inconsistency broke.
func TestBatchUpdateID3_ReportsRelativePaths(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "01 - Song.mp3"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/batch_update_id3/", BatchUpdateID3)

	body, _ := json.Marshal(map[string]interface{}{
		"file_full_path": "Artist/Album",
		"music_info":     map[string]interface{}{"filename": "01 - Song"},
		"select_data":    []map[string]interface{}{{"name": "01 - Song.mp3"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/batch_update_id3/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Failure() answers 200 with result:false, so the envelope is what
	// carries the outcome, not the status code.
	var env struct {
		Result bool `json:"result"`
		Data   struct {
			Done []struct {
				FileFullPath string `json:"file_full_path"`
				Status       string `json:"status"`
			} `json:"done"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if !env.Result {
		t.Fatalf("batch update failed: %s", w.Body.String())
	}
	if len(env.Data.Done) != 1 {
		t.Fatalf("done = %+v, want exactly one entry", env.Data.Done)
	}
	got := env.Data.Done[0].FileFullPath
	want := "Artist/Album/01 - Song.mp3"
	if got != want {
		t.Errorf("done[0].file_full_path = %q, want %q (relative to MUSIC_DIR, as update_id3/ echoes it)", got, want)
	}
	// The round-trip that used to break: hand the answer back to the
	// single-file endpoint, which rejects absolute paths.
	if _, err := roundTrippable(want); err != nil {
		t.Errorf("done[0].file_full_path cannot be sent back to update_id3/: %v", err)
	}
}

// roundTrippable mirrors what UpdateID3 does with the field: SafeJoin
// against MUSIC_DIR, which refuses anything absolute or escaping.
func roundTrippable(rel string) (string, error) {
	music := os.Getenv("MUSIC_DIR")
	// Same check, inlined rather than calling utils.SafeJoin, so the test
	// fails if the endpoint's contract changes rather than if a helper does.
	if filepath.IsAbs(rel) {
		return "", errAbsolutePath
	}
	joined := filepath.Join(music, rel)
	if joined != music && !bytes.HasPrefix([]byte(joined), []byte(music+string(filepath.Separator))) {
		return "", errEscapesRoot
	}
	return joined, nil
}

var (
	errAbsolutePath = &pathError{"path is absolute"}
	errEscapesRoot  = &pathError{"path escapes MUSIC_DIR"}
)

type pathError struct{ msg string }

func (e *pathError) Error() string { return e.msg }
