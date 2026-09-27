package handler_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/db"
	"go-music-tag/internal/gateway/handler"
)

func trashRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	audit.SetDB(gdb)
	t.Cleanup(func() { audit.SetDB(nil) })
	r := gin.New()
	r.GET("/api/trash/", handler.ListTrash)
	r.POST("/api/trash/restore/", handler.RestoreTrash)
	return r
}

type trashListEnvelope struct {
	Result bool `json:"result"`
	Data   struct {
		Batches []struct {
			ID        string `json:"id"`
			TotalSize int64  `json:"total_size"`
			Files     []struct {
				RelPath     string `json:"rel_path"`
				Size        int64  `json:"size"`
				IsAudio     bool   `json:"is_audio"`
				ContentType string `json:"content_type"`
			} `json:"files"`
		} `json:"batches"`
		TotalFiles int   `json:"total_files"`
		TotalSize  int64 `json:"total_size"`
		Truncated  bool  `json:"truncated"`
	} `json:"data"`
}

// postRestoreRaw returns the raw response body. Failure envelopes carry
// `"data": []` rather than an object, so a typed parse of a refusal is not
// possible — and asserting on the refusal is exactly what some tests need.
func postRestoreRaw(t *testing.T, r *gin.Engine, batchID string, relPaths []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"batch_id": batchID, "rel_paths": relPaths,
	})
	req := httptest.NewRequest("POST", "/api/trash/restore/", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

type restoreEnvelope struct {
	Result bool `json:"result"`
	Data   struct {
		Results []struct {
			RelPath string `json:"rel_path"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
		} `json:"results"`
		Restored int `json:"restored"`
		Failed   int `json:"failed"`
	} `json:"data"`
	Message string `json:"message"`
}

func getTrash(t *testing.T, r *gin.Engine) trashListEnvelope {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/trash/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env trashListEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("list body is not JSON: %v (%q)", err, w.Body.String())
	}
	return env
}

func postRestore(t *testing.T, r *gin.Engine, batchID string, relPaths []string) restoreEnvelope {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"batch_id": batchID, "rel_paths": relPaths,
	})
	req := httptest.NewRequest("POST", "/api/trash/restore/", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env restoreEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("restore body is not JSON: %v (%q)", err, w.Body.String())
	}
	return env
}

// seedTrash plants one batch the way DeleteFiles would.
func seedTrash(t *testing.T, batchID string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("DATA_DIR"), ".trash", batchID)
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A batch whose files have all been restored or purged leaves its directory
// behind, and that directory is not a record of anything. Listing it put a
// row in the dialog with no file in it — a history entry the user can read
// as "there is something to recover here" and act on.
func TestListTrash_OmitsBatchesThatNoLongerHoldFiles(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	seedTrash(t, "20260927-115027", map[string]string{"a.ogg": "a"})
	// Emptied by restoring everything it held: the directory survives, its
	// contents do not.
	seedTrash(t, "20260927-120000", map[string]string{})

	env := getTrash(t, trashRouter(t))
	if !env.Result {
		t.Fatalf("result = false: %+v", env)
	}
	if len(env.Data.Batches) != 1 {
		t.Fatalf("batches = %d, want only the one with a file in it: %+v",
			len(env.Data.Batches), env.Data.Batches)
	}
	if env.Data.Batches[0].ID != "20260927-115027" {
		t.Errorf("listed %q, want the batch that still has a file", env.Data.Batches[0].ID)
	}
	if env.Data.TotalFiles != 1 {
		t.Errorf("total_files = %d, want 1", env.Data.TotalFiles)
	}
}

// A fresh install has no trash at all. That is an empty trash, not a failure —
// the dialog opens on first run and must not look broken.
func TestListTrash_EmptyIsNotAnError(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	env := getTrash(t, trashRouter(t))
	if !env.Result {
		t.Error("result = false for a library that has never deleted anything")
	}
	if len(env.Data.Batches) != 0 || env.Data.TotalFiles != 0 {
		t.Errorf("expected an empty listing, got %+v", env.Data)
	}
}

func TestListTrash_ShowsWhatWasDeletedNewestFirst(t *testing.T) {
	data := t.TempDir()
	t.Setenv("DATA_DIR", data)
	seedTrash(t, "20260101-100000", map[string]string{"artist/a.ogg": "aaaa"})
	seedTrash(t, "20260927-115027", map[string]string{
		"artist/b.ogg":     "bbbbbb",
		"artist/cover.jpg": "jj",
	})

	env := getTrash(t, trashRouter(t))
	if !env.Result {
		t.Fatalf("result = false: %+v", env)
	}
	if len(env.Data.Batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(env.Data.Batches))
	}
	if env.Data.Batches[0].ID != "20260927-115027" {
		t.Errorf("first batch = %s, want the newest one", env.Data.Batches[0].ID)
	}
	if env.Data.TotalFiles != 3 {
		t.Errorf("total_files = %d, want 3", env.Data.TotalFiles)
	}
	if env.Data.TotalSize != int64(len("bbbbbb")+len("jj")+len("aaaa")) {
		t.Errorf("total_size = %d, wrong", env.Data.TotalSize)
	}
	// The UI needs to tell a track from a picture to render an icon.
	for _, f := range env.Data.Batches[0].Files {
		if f.RelPath == "artist/b.ogg" && !f.IsAudio {
			t.Error("b.ogg is not flagged as audio")
		}
		if f.RelPath == "artist/cover.jpg" && f.IsAudio {
			t.Error("cover.jpg is flagged as audio")
		}
	}
	// A batch reports its own size, not just the grand total: the panel shows
	// a size per batch, and a field that is always 0 is worse than no field.
	if got, want := env.Data.Batches[0].TotalSize, int64(len("bbbbbb")+len("jj")); got != want {
		t.Errorf("newest batch total_size = %d, want %d", got, want)
	}
	if got, want := env.Data.Batches[1].TotalSize, int64(len("aaaa")); got != want {
		t.Errorf("older batch total_size = %d, want %d", got, want)
	}
	// content_type has to be truthful per file. It was "application/octet-stream"
	// for every track, which tells a caller nothing the is_audio flag did not.
	for _, f := range env.Data.Batches[0].Files {
		switch f.RelPath {
		case "artist/b.ogg":
			if f.ContentType != "audio/ogg" {
				t.Errorf("b.ogg content_type = %q, want audio/ogg", f.ContentType)
			}
		case "artist/cover.jpg":
			if f.ContentType != "image/jpg" {
				t.Errorf("cover.jpg content_type = %q, want image/jpg", f.ContentType)
			}
		}
	}
}

func TestRestoreTrash_PutsTheFileBackWhereItCameFrom(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	seedTrash(t, "20260927-115027", map[string]string{"artist/album/song.ogg": "audio"})

	env := postRestore(t, trashRouter(t), "20260927-115027", []string{"artist/album/song.ogg"})
	if !env.Result || env.Data.Restored != 1 || env.Data.Failed != 0 {
		t.Fatalf("restore = %+v, want 1 restored", env.Data)
	}
	got, err := os.ReadFile(filepath.Join(music, "artist", "album", "song.ogg"))
	if err != nil {
		t.Fatalf("restored file is not where it came from: %v", err)
	}
	if string(got) != "audio" {
		t.Errorf("content = %q, want %q", got, "audio")
	}
	if _, err := os.Stat(filepath.Join(data, ".trash", "20260927-115027", "artist", "album", "song.ogg")); err == nil {
		t.Error("the file is still in the trash after being restored")
	}
}

// The whole point of restoring is getting a file back. Silently replacing a
// track the user re-downloaded since, with a stale copy of the same name, is a
// worse outcome than an error — so the destination is checked, not overwritten.
func TestRestoreTrash_RefusesToOverwriteAnExistingFile(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	seedTrash(t, "20260927-115027", map[string]string{"song.ogg": "old copy"})
	if err := os.WriteFile(filepath.Join(music, "song.ogg"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := postRestore(t, trashRouter(t), "20260927-115027", []string{"song.ogg"})
	if env.Data.Restored != 0 || env.Data.Failed != 1 {
		t.Fatalf("restore = %+v, want it refused", env.Data)
	}
	if env.Data.Results[0].Status != "exists" {
		t.Errorf("status = %q, want %q", env.Data.Results[0].Status, "exists")
	}
	if env.Data.Results[0].Reason == "" {
		t.Error("no reason given; the user cannot tell what to do about it")
	}
	got, _ := os.ReadFile(filepath.Join(music, "song.ogg"))
	if string(got) != "current" {
		t.Errorf("the existing file was overwritten with %q", got)
	}
}

// rel_path decides where a file is written, so it is attacker-controlled.
//
// The depth matters: the batch lives at <DATA_DIR>/.trash/<id>/, so a path
// with ONE ".." only reaches .trash/ and a bare filepath.Join would still
// resolve to a nonexistent file there — the test would pass with no guard at
// all. These are the ones that actually land somewhere real.
func TestRestoreTrash_RefusesPathsThatEscapeTheBatch(t *testing.T) {
	parent := t.TempDir()
	data := filepath.Join(parent, "data")
	music := filepath.Join(parent, "music")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	seedTrash(t, "20260927-115027", map[string]string{"ok.ogg": "x"})

	// Reachable with a plain filepath.Join from the batch directory.
	canaries := map[string]string{
		"../../canary.txt":       filepath.Join(data, "canary.txt"),
		"../../../escaped.ogg":   filepath.Join(parent, "escaped.ogg"),
		"../../.trash/other.ogg": filepath.Join(data, ".trash", "other.ogg"),
	}
	for rel, abs := range canaries {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = rel
	}

	env := postRestore(t, trashRouter(t), "20260927-115027", []string{
		"../../canary.txt", "../../../escaped.ogg", "../../.trash/other.ogg",
	})
	if env.Data.Restored != 0 {
		t.Errorf("restored %d traversal paths", env.Data.Restored)
	}
	for _, abs := range canaries {
		if _, err := os.Stat(filepath.Join(music, filepath.Base(abs))); err == nil {
			t.Errorf("ESCAPED: %s was written into the library", filepath.Base(abs))
		}
		if _, err := os.Stat(abs); err != nil {
			t.Errorf("the canary at %s was moved: %v", abs, err)
		}
	}
}

// The batch id names a directory under .trash and arrives from the client.
func TestRestoreTrash_RefusesABatchIdThatIsAPath(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)

	// ".." is the one that matters: it holds no slash and no glob character,
	// so a glob-safety check waves it through and the batch silently resolves
	// to DATA_DIR itself.
	for _, id := range []string{"..", ".", "../../etc", "a/b", ".hidden", "*", "with space", ""} {
		raw := postRestoreRaw(t, trashRouter(t), id, []string{"song.ogg"})
		if !strings.Contains(raw, `"result":false`) {
			t.Errorf("batch_id %q was accepted: %s", id, raw)
		}
	}
}

func TestRestoreTrash_ReportsAMissingFileWithoutFailingTheBatch(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	seedTrash(t, "20260927-115027", map[string]string{"here.ogg": "x"})

	env := postRestore(t, trashRouter(t), "20260927-115027", []string{"here.ogg", "gone.ogg"})
	if env.Data.Restored != 1 || env.Data.Failed != 1 {
		t.Fatalf("restore = %+v, want 1 restored and 1 missing", env.Data)
	}
	if env.Data.Results[1].Status != "missing" {
		t.Errorf("second row status = %q, want %q", env.Data.Results[1].Status, "missing")
	}
}
