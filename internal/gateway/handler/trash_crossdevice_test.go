package handler

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/trash"
)

// RestoreTrash and DeleteFiles move files between MUSIC_DIR and
// DATA_DIR/.trash. In the default compose layout those are separate mounts, so
// a rename between them fails with EXDEV and has to fall back to copy+remove.
//
// This test exists because that fallback was missing on the restore side and
// nothing noticed: one temp dir is one device, os.Rename succeeds, and the
// suite was green while every restore in a real deployment returned
// "invalid cross-device link".
// withCrossDeviceRename makes every rename look like a cross-device one and
// reports how many times the move actually attempted one.
//
// The count is the point. Asserting only "the file came back" cannot tell the
// fallback apart from a plain rename that happened to work, and a plain rename
// DOES work on a single temp dir — so a handler that skipped MoveAside and
// called os.Rename itself passed every test here while failing on every file
// in the real two-mount deployment.
func withCrossDeviceRename(t *testing.T) *int {
	t.Helper()
	attempts := 0
	prev := trash.Rename
	trash.Rename = func(src, dest string) error {
		attempts++
		return &os.LinkError{Op: "rename", Old: src, New: dest, Err: syscall.EXDEV}
	}
	t.Cleanup(func() { trash.Rename = prev })
	return &attempts
}

func exdevRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/trash/restore/", RestoreTrash)
	return r
}

func postRestoreInPackage(t *testing.T, r *gin.Engine, batchID string, relPaths []string) map[string]interface{} {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"batch_id": batchID, "rel_paths": relPaths})
	req := httptest.NewRequest("POST", "/api/trash/restore/", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env struct {
		Data struct {
			Results []struct {
				RelPath string `json:"rel_path"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
			} `json:"results"`
			Restored int `json:"restored"`
			Failed   int `json:"failed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("restore body is not JSON: %v (%q)", err, w.Body.String())
	}
	return map[string]interface{}{
		"restored": env.Data.Restored,
		"failed":   env.Data.Failed,
		"status":   env.Data.Results[0].Status,
		"reason":   env.Data.Results[0].Reason,
	}
}

// The headline case: MUSIC_DIR and DATA_DIR on different devices. The file
// must come back with its content intact and be gone from the trash.
func TestRestoreTrash_SurvivesSeparateMountsForDataAndMusic(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	trashFile := filepath.Join(data, ".trash", "20260927-115027", "artist", "song.ogg")
	if err := os.MkdirAll(filepath.Dir(trashFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trashFile, []byte("the actual audio bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	renames := withCrossDeviceRename(t)

	got := postRestoreInPackage(t, exdevRouter(), "20260927-115027", []string{"artist/song.ogg"})
	if got["restored"] != 1 || got["failed"] != 0 {
		t.Fatalf("restore across mounts = %+v, want 1 restored; reason %q", got, got["reason"])
	}
	if *renames == 0 {
		t.Error("the move never attempted a rename: RestoreTrash is not going through trash.MoveAside, " +
			"so it will return invalid cross-device link on every file in a real deployment")
	}
	back, err := os.ReadFile(filepath.Join(music, "artist", "song.ogg"))
	if err != nil {
		t.Fatalf("file did not come back: %v", err)
	}
	if string(back) != "the actual audio bytes" {
		t.Errorf("content = %q, want the original bytes", back)
	}
	if _, err := os.Stat(trashFile); err == nil {
		t.Error("the copy is still in the trash after a successful restore")
	}
}

// A fallback that copied but did not remove would silently duplicate every
// restored track, and the library browser reads the disk — so the user would
// see the file appear in the trash's own listing forever.
func TestRestoreTrash_CrossDeviceFallbackDoesNotLeaveTheOriginalBehind(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	trashFile := filepath.Join(data, ".trash", "20260927-115027", "song.ogg")
	if err := os.MkdirAll(filepath.Dir(trashFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trashFile, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = withCrossDeviceRename(t)

	postRestoreInPackage(t, exdevRouter(), "20260927-115027", []string{"song.ogg"})

	if _, err := os.Stat(trashFile); err == nil {
		t.Error("the original is still in the trash: a restore would duplicate the file on every retry")
	}
	if _, err := os.Stat(filepath.Join(music, "song.ogg")); err != nil {
		t.Errorf("nothing landed in the library: %v", err)
	}
}

// The destination is still checked before any moving happens, on the
// cross-device path too — EXDEV must not become a way around "do not
// overwrite".
func TestRestoreTrash_CrossDeviceFallbackStillRefusesToOverwrite(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	trashFile := filepath.Join(data, ".trash", "20260927-115027", "song.ogg")
	if err := os.MkdirAll(filepath.Dir(trashFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trashFile, []byte("old copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(music, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(music, "song.ogg"), []byte("re-downloaded"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = withCrossDeviceRename(t)

	got := postRestoreInPackage(t, exdevRouter(), "20260927-115027", []string{"song.ogg"})
	if got["restored"] != 0 || got["status"] != "exists" {
		t.Fatalf("restore = %+v, want it refused with %q", got, "exists")
	}
	current, _ := os.ReadFile(filepath.Join(music, "song.ogg"))
	if string(current) != "re-downloaded" {
		t.Errorf("the existing file was replaced with %q", current)
	}
}

// Guards the seam itself: with the real rename the same request succeeds via
// the rename fast path, so a passing test above is about the fallback and not
// about a request that is broken either way.
func TestRestoreTrash_RenameFastPathStillWorksWithoutEXDEV(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	trashFile := filepath.Join(data, ".trash", "20260927-115027", "song.ogg")
	if err := os.MkdirAll(filepath.Dir(trashFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trashFile, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := postRestoreInPackage(t, exdevRouter(), "20260927-115027", []string{"song.ogg"})
	if got["restored"] != 1 {
		t.Fatalf("restore on one device = %+v, want 1 restored; reason %q", got, got["reason"])
	}
	if _, err := os.Stat(trashFile); err == nil {
		t.Error("rename fast path left the file in the trash")
	}
}

// A move that genuinely cannot happen must be reported, not swallowed: the
// panel shows one reason per row, and a silent failure looks exactly like a
// restore that did nothing.
//
// Note what is NOT asserted here: that a non-EXDEV rename error is reported
// as itself. trash.MoveAside falls back to copy+remove on ANY rename error, so
// such an error is retried as a copy and succeeds if the filesystem allows it.
// That is the shared helper's existing contract, and changing it is a
// separate decision from making restore reachable at all.
func TestRestoreTrash_ReportsWhyAMoveCouldNotHappen(t *testing.T) {
	data, music := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	t.Setenv("MUSIC_DIR", music)
	trashFile := filepath.Join(data, ".trash", "20260927-115027", "album", "song.ogg")
	if err := os.MkdirAll(filepath.Dir(trashFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trashFile, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A regular file where a directory has to be: the destination cannot be
	// created, so neither the rename nor the copy can land.
	if err := os.MkdirAll(music, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(music, "album"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = withCrossDeviceRename(t)

	got := postRestoreInPackage(t, exdevRouter(), "20260927-115027", []string{"album/song.ogg"})
	if got["restored"] != 0 || got["failed"] != 1 || got["status"] != "failed" {
		t.Fatalf("restore = %+v, want a reported failure", got)
	}
	reason, _ := got["reason"].(string)
	if reason == "" {
		t.Error("no reason given; the user is left with a silent failure")
	}
	// A failed restore must not eat the only copy.
	if _, err := os.Stat(trashFile); err != nil {
		t.Errorf("the file was removed from the trash despite the failure: %v", err)
	}
}
