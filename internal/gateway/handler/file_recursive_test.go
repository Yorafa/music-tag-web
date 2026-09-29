package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/testaudio"
)

// These tests pin the two properties that make the recursive endpoint a
// drop-in for the client-side walk it replaces:
//
//  1. One request returns the whole subtree, at ANY depth. A tree shaped
//     歌手/专辑/碟片/ is the case that made the old client-side walk issue
//     thousands of requests; if this handler needed a round trip per level
//     the whole premise is gone, so the depth case is asserted directly.
//  2. Containment. SafeJoin is the only thing standing between a crafted
//     `paths` entry and the rest of the filesystem, so traversal is tested
//     the same way SECURITY.md documents it.

func newRecursiveRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/file_list_recursive/", FileListRecursive)
	r.POST("/api/music_id3/", MusicID3)
	r.GET("/api/album_cover/", AlbumCover)
	return r
}

// buildTree creates a directory tree under a temp MUSIC_DIR and points
// MUSIC_DIR at it.
func buildTree(t *testing.T, files map[string]bool) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MUSIC_DIR", root)
	for rel, isDir := range files {
		full := filepath.Join(root, rel)
		if isDir {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func postRecursive(t *testing.T, r *gin.Engine, body string) RecursiveFileListResponse {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/file_list_recursive/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var env struct {
		Result bool                      `json:"result"`
		Data   RecursiveFileListResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}
	if !env.Result {
		t.Fatalf("result=false: %s", w.Body.String())
	}
	return env.Data
}

func pathsOf(items []RecursiveFileItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Path)
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestFileListRecursive_WalksAnyDepthInOneCall is the regression this whole
// endpoint exists for. 4 levels deep, and ONE request must return all of it.
func TestFileListRecursive_WalksAnyDepthInOneCall(t *testing.T) {
	buildTree(t, map[string]bool{
		"歌手A/专辑1/碟片1/01.flac":      false,
		"歌手A/专辑1/碟片1/02.flac":      false,
		"歌手A/专辑1/碟片2/03.flac":      false,
		"歌手A/专辑2/04.flac":          false,
		"歌手B/专辑3/深层/更深/最深/05.flac": false,
		"歌手A/专辑1/cover.jpg":        false,
		"歌手A/专辑1/notes.txt":        false,
	})

	got := postRecursive(t, newRecursiveRouter(t), `{"paths":[""]}`)

	// Exactly the five audio files; the .jpg and .txt are not library files.
	if len(got.Files) != 5 {
		t.Fatalf("got %d files, want 5: %v", len(got.Files), pathsOf(got.Files))
	}
	list := pathsOf(got.Files)
	for _, want := range []string{
		"歌手A/专辑1/碟片1/01.flac",
		"歌手A/专辑1/碟片1/02.flac",
		"歌手A/专辑1/碟片2/03.flac",
		"歌手A/专辑2/04.flac",
		"歌手B/专辑3/深层/更深/最深/05.flac",
	} {
		if !has(list, want) {
			t.Errorf("missing %q; got %v", want, list)
		}
	}
	for _, bad := range list {
		if strings.HasSuffix(bad, ".jpg") || strings.HasSuffix(bad, ".txt") {
			t.Errorf("non-audio leaked into results: %q", bad)
		}
	}
	if got.Truncated {
		t.Error("Truncated = true on a 5-file tree")
	}
}

// TestFileListRecursive_AcceptsUppercaseExt pins parity with FileList,
// which lowercases before judging the extension.
func TestFileListRecursive_AcceptsUppercaseExt(t *testing.T) {
	buildTree(t, map[string]bool{"A/Track.FLAC": false})
	got := postRecursive(t, newRecursiveRouter(t), `{"paths":[""]}`)
	if len(got.Files) != 1 {
		t.Fatalf("uppercase ext dropped: %v", pathsOf(got.Files))
	}
}

// TestFileListRecursive_MultiplePathsTagSource checks the per-source
// attribution the worklist dedupes on.
func TestFileListRecursive_MultiplePathsTagSource(t *testing.T) {
	buildTree(t, map[string]bool{
		"A/1.flac": false,
		"B/1.flac": false,
		"C/1.flac": false,
	})
	got := postRecursive(t, newRecursiveRouter(t), `{"paths":["A","B"]}`)
	if len(got.Files) != 2 {
		t.Fatalf("got %d, want 2: %v", len(got.Files), pathsOf(got.Files))
	}
	for _, f := range got.Files {
		if f.Source != "A" && f.Source != "B" {
			t.Errorf("file %q has source %q, want A or B", f.Path, f.Source)
		}
		if !strings.HasPrefix(f.Path, f.Source+"/") {
			t.Errorf("file %q does not sit under its source %q", f.Path, f.Source)
		}
	}
}

// TestFileListRecursive_EmptyPathsMeansRoot documents the sentinel.
func TestFileListRecursive_EmptyPathsMeansRoot(t *testing.T) {
	buildTree(t, map[string]bool{"A/1.flac": false})
	got := postRecursive(t, newRecursiveRouter(t), `{"paths":[]}`)
	if len(got.Files) != 1 {
		t.Fatalf("empty paths did not mean root: %v", pathsOf(got.Files))
	}
}

// TestFileListRecursive_AcceptsBareFile keeps parity with the picker, which
// can hand this endpoint a single ticked file.
func TestFileListRecursive_AcceptsBareFile(t *testing.T) {
	buildTree(t, map[string]bool{"A/1.flac": false, "A/2.mp3": false})
	got := postRecursive(t, newRecursiveRouter(t), `{"paths":["A/1.flac"]}`)
	if len(got.Files) != 1 || got.Files[0].Path != "A/1.flac" {
		t.Fatalf("bare file not returned: %v", pathsOf(got.Files))
	}
	if got.Files[0].Name != "1.flac" {
		t.Errorf("Name = %q, want 1.flac", got.Files[0].Name)
	}
}

// TestFileListRecursive_LimitTruncatesAndSays so — a capped response must
// never read as a complete one.
func TestFileListRecursive_LimitTruncatesAndSays(t *testing.T) {
	buildTree(t, map[string]bool{
		"A/1.flac": false, "A/2.flac": false, "A/3.flac": false,
		"A/4.flac": false, "A/5.flac": false,
	})
	got := postRecursive(t, newRecursiveRouter(t), `{"paths":[""],"limit":2}`)
	if len(got.Files) != 2 {
		t.Fatalf("limit ignored: got %d files", len(got.Files))
	}
	if !got.Truncated {
		t.Error("Truncated = false despite hitting the limit — a partial list would read as complete")
	}
}

// TestFileListRecursive_MissingPathSkippedNotFatal: one deleted directory
// must not cost the user the rest of their batch.
func TestFileListRecursive_MissingPathSkippedNotFatal(t *testing.T) {
	buildTree(t, map[string]bool{"Good/1.flac": false})
	got := postRecursive(t, newRecursiveRouter(t), `{"paths":["Gone","Good"]}`)
	if len(got.Files) != 1 || got.Files[0].Path != "Good/1.flac" {
		t.Fatalf("missing path broke the batch: %v", pathsOf(got.Files))
	}
}

// TestFileListRecursive_RejectsTraversal is the security pin.
func TestFileListRecursive_RejectsTraversal(t *testing.T) {
	root := buildTree(t, map[string]bool{"inside/1.flac": false})
	// A file the traversal must never reach, outside MUSIC_DIR.
	outside := filepath.Join(filepath.Dir(root), "secret.flac")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	for _, evil := range []string{"../secret.flac", "/etc/passwd", "inside/../../secret.flac"} {
		got := postRecursive(t, newRecursiveRouter(t), `{"paths":["`+evil+`"]}`)
		if len(got.Files) != 0 {
			t.Errorf("traversal %q returned %v", evil, pathsOf(got.Files))
		}
		for _, f := range got.Files {
			if filepath.IsAbs(f.Path) {
				t.Errorf("traversal %q leaked an absolute path %q", evil, f.Path)
			}
		}
	}
}

// TestMusicID3_IncludeArtworkFalse pins the payload trim against a REAL
// audio file, so the assertion is about the response shape and not a mock's
// idea of it. Uses the repo's own testaudio seeder rather than hand-built
// bytes: internal/tag.ensureAudioFile rejects anything that is not a
// decodable stream, so a stub would only test the rejection path.
func TestMusicID3_IncludeArtworkFalse(t *testing.T) {
	dir := t.TempDir()
	testaudio.SeedMP3(t, dir, "song.mp3")
	t.Setenv("MUSIC_DIR", dir)

	r := newRecursiveRouter(t)
	post := func(body string) map[string]interface{} {
		req := httptest.NewRequest("POST", "/api/music_id3/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var env struct {
			Data map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		return env.Data
	}

	// Absent → artwork key still present, so a client that never sends the
	// field sees exactly the response it always did.
	if _, ok := post(`{"file_path":"","file_name":"song.mp3"}`)["artwork"]; !ok {
		t.Error("omitting include_artwork dropped the artwork key — existing clients would see a changed shape")
	}

	without := post(`{"file_path":"","file_name":"song.mp3","include_artwork":false}`)
	if _, ok := without["artwork"]; ok {
		t.Error("include_artwork=false still returned artwork")
	}
	// The point is a SMALLER response, not a different one: the text tags
	// the table renders must survive.
	for _, k := range []string{"filename"} {
		if _, ok := without[k]; !ok {
			t.Errorf("include_artwork=false also dropped %q", k)
		}
	}
}

// TestAlbumCover_NoCoverIs404: a cover-less file is a normal state, and 404
// is what lets the client fall back to its gradient without a toast.
func TestAlbumCover_NoCoverIs404(t *testing.T) {
	dir := t.TempDir()
	testaudio.SeedMP3(t, dir, "song.mp3")
	t.Setenv("MUSIC_DIR", dir)

	req := httptest.NewRequest("GET", "/api/album_cover/?file_path=&file_name=song.mp3", nil)
	w := httptest.NewRecorder()
	newRecursiveRouter(t).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("cover-less file: status %d, want 404 (body %s)", w.Code, w.Body.String())
	}
}

// TestAlbumCover_RejectsNonAudio keeps the binary endpoint from becoming a
// general file-read primitive. 400 (not 404): the caller asked for something
// that is not a track, which is its bug, not a missing resource.
func TestAlbumCover_RejectsNonAudio(t *testing.T) {
	buildTree(t, map[string]bool{"A/secret.txt": false})
	req := httptest.NewRequest("GET", "/api/album_cover/?file_path=A&file_name=secret.txt", nil)
	w := httptest.NewRecorder()
	newRecursiveRouter(t).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("served a cover for a .txt: status %d, want 400", w.Code)
	}
}

// TestAlbumCover_RejectsTraversal: an <img> src is built from row data, so
// containment has to hold here too — not just on the JSON endpoints.
func TestAlbumCover_RejectsTraversal(t *testing.T) {
	root := buildTree(t, map[string]bool{"inside/1.flac": false})
	outside := filepath.Join(filepath.Dir(root), "secret.flac")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	req := httptest.NewRequest("GET", "/api/album_cover/?file_path=..&file_name=secret.flac", nil)
	w := httptest.NewRecorder()
	newRecursiveRouter(t).ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Error("traversal served bytes from outside MUSIC_DIR")
	}
}

func TestAlbumCover_RequiresFileName(t *testing.T) {
	buildTree(t, map[string]bool{"A/1.flac": false})
	req := httptest.NewRequest("GET", "/api/album_cover/?file_path=A", nil)
	w := httptest.NewRecorder()
	newRecursiveRouter(t).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing file_name: status %d, want 400", w.Code)
	}
}

// TestDecodeDataURI covers the parsing the cover endpoint depends on.
func TestDecodeDataURI(t *testing.T) {
	// "data:image/png;base64," + base64("hi")
	mime, raw, err := decodeDataURI("data:image/png;base64,aGk=")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q", mime)
	}
	if string(raw) != "hi" {
		t.Errorf("raw = %q, want %q", raw, "hi")
	}
	for _, bad := range []string{"", "nope", "data:image/png,hi", "data:;base64"} {
		if _, _, err := decodeDataURI(bad); err == nil {
			t.Errorf("decodeDataURI(%q) accepted a malformed URI", bad)
		}
	}
}
