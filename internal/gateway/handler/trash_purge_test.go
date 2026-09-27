package handler_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/gateway/handler"
)

type purgeReport struct {
	Results []struct {
		RelPath string `json:"rel_path"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
	} `json:"results"`
	Purged int `json:"purged"`
	Failed int `json:"failed"`
}

type purgeEnvelope struct {
	Result  bool        `json:"result"`
	Data    purgeReport `json:"data"`
	Message string      `json:"message"`
}

// A refusal is served with a nil `data`, which marshals to `[]` rather than to
// an object, so a plain decode of the envelope blows up exactly on the
// responses where the caller most wants to read the reason.
func (e *purgeEnvelope) UnmarshalJSON(b []byte) error {
	var wire struct {
		Result  bool            `json:"result"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	e.Result, e.Message = wire.Result, wire.Message
	_ = json.Unmarshal(wire.Data, &e.Data) // zero report when data is not an object
	return nil
}

func purgeRouter(t *testing.T) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.POST("/api/trash/purge/", handler.PurgeTrash)
	return r
}

// postPurge sends a purge with an explicit confirm flag, because the flag is
// the point of several tests here.
func postPurge(t *testing.T, batchID string, relPaths []string, confirm bool) purgeEnvelope {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"batch_id": batchID, "rel_paths": relPaths, "confirm": confirm,
	})
	req := httptest.NewRequest("POST", "/api/trash/purge/", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	purgeRouter(t).ServeHTTP(w, req)
	var env purgeEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("purge body is not JSON: %v (%q)", err, w.Body.String())
	}
	return env
}

// The whole reason this endpoint is shaped the way it is. A client that wires
// the wrong button, or a retry that re-sends a body it already sent, must not
// be able to destroy the only copy of a file.
func TestPurgeTrash_RefusesWithoutAnExplicitConfirm(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	seedTrash(t, "20260927-115027", map[string]string{"only-copy.ogg": "the only bytes"})

	env := postPurge(t, "20260927-115027", []string{"only-copy.ogg"}, false)
	if env.Result {
		t.Fatalf("purge succeeded without confirmation: %+v", env)
	}
	if env.Message == "" {
		t.Error("no message; the caller cannot tell it needs to confirm")
	}
	if _, err := os.Stat(filepath.Join(data, ".trash", "20260927-115027", "only-copy.ogg")); err != nil {
		t.Fatalf("the file was destroyed anyway: %v", err)
	}
}

// An empty rel_paths is the "purge this whole batch" form, and it is the only
// way a batch directory itself ever goes away — otherwise an emptied batch
// lingers in the listing forever with nothing in it.
func TestPurgeTrash_WholeBatchRemovesTheDirectoryToo(t *testing.T) {
	data := t.TempDir()
	t.Setenv("DATA_DIR", data)
	seedTrash(t, "20260927-115027", map[string]string{
		"a.ogg":   "aaaa",
		"b/c.ogg": "cccc",
	})

	env := postPurge(t, "20260927-115027", nil, true)
	if !env.Result || env.Data.Purged != 2 || env.Data.Failed != 0 {
		t.Fatalf("purge = %+v, want 2 purged", env.Data)
	}
	if _, err := os.Stat(filepath.Join(data, ".trash", "20260927-115027")); err == nil {
		t.Error("the batch directory is still there; an emptied batch would show in the listing forever")
	}
}

// A file purged on its own leaves the batch, which is what the per-file
// button does. The directory staying is correct: other batches are unaffected
// and the batch still has a truthful "what happened" identity.
func TestPurgeTrash_SingleFileLeavesTheBatch(t *testing.T) {
	data := t.TempDir()
	t.Setenv("DATA_DIR", data)
	seedTrash(t, "20260927-115027", map[string]string{"a.ogg": "aaaa", "b.ogg": "bbbb"})

	env := postPurge(t, "20260927-115027", []string{"a.ogg"}, true)
	if !env.Result || env.Data.Purged != 1 {
		t.Fatalf("purge = %+v, want 1 purged", env.Data)
	}
	dir := filepath.Join(data, ".trash", "20260927-115027")
	if _, err := os.Stat(filepath.Join(dir, "a.ogg")); err == nil {
		t.Error("a.ogg is still there")
	}
	if _, err := os.Stat(filepath.Join(dir, "b.ogg")); err != nil {
		t.Errorf("b.ogg was destroyed too: %v", err)
	}
}

// rel_path decides what gets destroyed, so it is attacker-controlled. A purge
// that can reach outside .trash is a remote delete for anyone who can POST.
func TestPurgeTrash_RefusesPathsThatEscapeTheBatch(t *testing.T) {
	parent := t.TempDir()
	data := filepath.Join(parent, "data")
	music := filepath.Join(parent, "music")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	seedTrash(t, "20260927-115027", map[string]string{"ok.ogg": "x"})

	canaries := map[string]string{
		"../../canary.txt":       filepath.Join(data, "canary.txt"),
		"../../../escaped.ogg":   filepath.Join(parent, "escaped.ogg"),
		"../../.trash/other.ogg": filepath.Join(data, ".trash", "other.ogg"),
	}
	for _, abs := range canaries {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	env := postPurge(t, "20260927-115027", []string{
		"../../canary.txt", "../../../escaped.ogg", "../../.trash/other.ogg",
	}, true)
	if env.Data.Purged != 0 {
		t.Errorf("purged %d traversal paths", env.Data.Purged)
	}
	for _, abs := range canaries {
		if _, err := os.Stat(abs); err != nil {
			t.Errorf("DESTROYED OUTSIDE THE TRASH: %s (%v)", abs, err)
		}
	}
}

// A library file reached by traversal is the sharpest version of that: the
// purge would remove a track the user still has, from a place the trash is
// not allowed to write to.
func TestPurgeTrash_CannotReachTheLibrary(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	seedTrash(t, "20260927-115027", map[string]string{"ok.ogg": "x"})
	track := filepath.Join(music, "a-real-track.ogg")
	if err := os.WriteFile(track, []byte("my music"), 0o644); err != nil {
		t.Fatal(err)
	}
	// data and music are siblings here, so "../../../<music base>" lands on
	// the library from inside the batch.
	rel := "../../../" + filepath.Base(music) + "/a-real-track.ogg"
	env := postPurge(t, "20260927-115027", []string{rel}, true)
	if env.Data.Purged != 0 {
		t.Errorf("purged %d files, want 0", env.Data.Purged)
	}
	if _, err := os.Stat(track); err != nil {
		t.Fatalf("A LIBRARY TRACK WAS DESTROYED: %v", err)
	}
}

// A crafted rel_path naming a directory must not get os.RemoveAll semantics
// from the per-file path — the whole-batch call is the one that is explicit
// about removing directories.
func TestPurgeTrash_PerFilePathRefusesADirectory(t *testing.T) {
	data := t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", t.TempDir())
	seedTrash(t, "20260927-115027", map[string]string{"nested/keep.ogg": "x"})

	env := postPurge(t, "20260927-115027", []string{"nested"}, true)
	if env.Data.Purged != 0 || env.Data.Failed != 1 {
		t.Fatalf("purge = %+v, want a refusal", env.Data)
	}
	if env.Data.Results[0].Status != "refused" {
		t.Errorf("status = %q, want %q", env.Data.Results[0].Status, "refused")
	}
	if env.Data.Results[0].Reason == "" {
		t.Error("no reason; the user is told nothing about what to do instead")
	}
	if _, err := os.Stat(filepath.Join(data, ".trash", "20260927-115027", "nested", "keep.ogg")); err != nil {
		t.Errorf("the file inside the named directory was destroyed: %v", err)
	}
}

func TestPurgeTrash_ReportsAMissingFileWithoutFailingTheBatch(t *testing.T) {
	data := t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", t.TempDir())
	seedTrash(t, "20260927-115027", map[string]string{"here.ogg": "x"})

	env := postPurge(t, "20260927-115027", []string{"here.ogg", "gone.ogg"}, true)
	if env.Data.Purged != 1 || env.Data.Failed != 1 {
		t.Fatalf("purge = %+v, want 1 purged and 1 missing", env.Data)
	}
	if env.Data.Results[1].Status != "missing" {
		t.Errorf("second row status = %q, want %q", env.Data.Results[1].Status, "missing")
	}
}

// The batch id names a directory and arrives from the client, same rule as
// restore. ".." is the one that matters: no slash, no glob character, so a
// glob-safety check waves it through and it resolves to DATA_DIR itself.
func TestPurgeTrash_RefusesABatchIdThatIsAPath(t *testing.T) {
	data := t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(data, "canary.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"..", ".", "../../etc", "a/b", ".hidden", "with space", ""} {
		// No rel_paths, so each of these takes the RemoveAll path — the one
		// that would actually be destructive.
		env := postPurge(t, id, nil, true)
		if env.Result {
			t.Errorf("batch_id %q was accepted and purged: %+v", id, env)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "canary.txt")); err != nil {
		t.Fatalf("a canary in DATA_DIR was destroyed: %v", err)
	}
}
