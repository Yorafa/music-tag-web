package handler_test

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/gateway/handler"
	"go-music-tag/internal/tasks"
)

func tidyRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/tidy_folder/", handler.TidyFolder)
	return r
}

func postTidy(t *testing.T, r *gin.Engine, root string) string {
	t.Helper()
	body := `{"music_paths":["17/song.ogg"],"root_path":` +
		mustJSON(t, root) + `,"first_dir":"artist"}`
	req := httptest.NewRequest("POST", "/api/tidy_folder/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

func mustJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type tidyEnvelope struct {
	Result  bool   `json:"result"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// requireRefused asserts a Failure envelope. handler.Failure answers HTTP 200
// with code "400" in the body (response.go) — this codebase's long-standing
// convention — so the status line says nothing and the envelope is the answer.
func requireRefused(t *testing.T, body string) tidyEnvelope {
	t.Helper()
	var env tidyEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("body is not an envelope: %v (%q)", err, body)
	}
	if env.Result || env.Code != "400" {
		t.Errorf("envelope = %+v, want result=false code=400", env)
	}
	return env
}

// The bug this pins: a root outside the library used to be enqueued anyway.
// The gateway reported success, the UI toasted "已提交目录整理异步任务", and
// the worker then refused the batch — so a typo read as a success that moved
// nothing. The rejection has to happen HERE, synchronously, before the enqueue.
func TestTidyFolder_RefusesARootOutsideTheLibraryBeforeEnqueueing(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	r := tidyRouter()

	body := postTidy(t, r, "/etc")
	env := requireRefused(t, body)
	if !strings.Contains(env.Message, music) {
		t.Errorf("message %q does not name the real library root %q — the user cannot correct the field without it", env.Message, music)
	}
}

// The message has to quote what the user typed, not just complain: "/etc" and
// "/media/etc" are different mistakes and the fix for each is different.
func TestTidyFolder_RefusalQuotesTheSubmittedRoot(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	r := tidyRouter()

	body := postTidy(t, r, "/etc")
	env := requireRefused(t, body)
	if !strings.Contains(env.Message, "/etc") {
		t.Errorf("message %q does not quote the rejected root", env.Message)
	}
}

// A gateway that waved through a root the worker refuses would reintroduce the
// silent failure from the other direction. The two now call the SAME function
// (tasks.TidyRoot), so this table is the rule itself rather than a comparison
// of two copies that could drift.
func TestTidyRoot_WhichRootsAreLegal(t *testing.T) {
	music := t.TempDir()

	cases := []struct {
		name   string
		root   string
		accept bool
	}{
		{"the library root itself", music, true},
		{"the root with a trailing slash", music + "/", true},
		{"a subdirectory of the library", filepath.Join(music, "reorganised"), true},
		{"an absolute path outside", "/etc", false},
		// The trap this whole change exists for: the rest of the UI speaks
		// "/music", the container speaks "/app/media", and only the latter
		// is a real path on the machine doing the move.
		{"the UI's own display path, which is not a container path", "/music", false},
		// Widening the per-file paths to accept the relative form must NOT
		// have widened the root: a stray word would otherwise build a new
		// nested tree instead of reporting the typo.
		{"a relative root", "reorganised", false},
		{"a traversal out of the library", filepath.Join(music, "..", "..", "etc"), false},
		{"a sibling with a shared prefix", music + "-backup", false},
	}
	for _, tc := range cases {
		_, err := tasks.TidyRoot(music, tc.root)
		if got := err == nil; got != tc.accept {
			t.Errorf("%s (root=%q): accepted=%v, want %v (err=%v)", tc.name, tc.root, got, tc.accept, err)
		}
	}
}

// TidyRoot is exported now that the gateway calls it too, so its precondition
// is part of the contract rather than an internal detail: with no library root
// there is no way to know what is in bounds, and this handler MOVES files. It
// has to refuse, not admit.
func TestTidyRoot_RefusesWhenNoLibraryRootIsConfigured(t *testing.T) {
	for _, root := range []string{"/app/media", "", "/etc"} {
		abs, err := tasks.TidyRoot("", root)
		if err == nil {
			t.Errorf("root=%q: accepted with no music root (resolved to %q) — that admits anything", root, abs)
		}
	}
}

// Without MUSIC_DIR there is no way to know what is in bounds, and the
// mutation-capable handlers move files. Refuse everything rather than admit
// everything — same stance the worker takes.
func TestTidyFolder_RefusesWhenMusicDirIsUnset(t *testing.T) {
	t.Setenv("MUSIC_DIR", "")
	r := tidyRouter()

	body := postTidy(t, r, "/app/media")
	env := requireRefused(t, body)
	if !strings.Contains(env.Message, "MUSIC_DIR") {
		t.Errorf("message %q does not mention the missing configuration", env.Message)
	}
}
