package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/testaudio"
	"go-music-tag/internal/utils"
)

// TestBatchUpdateID3_DoesNotReportNonAudioAsUpdated is the false-success fix.
//
// applyFileUpdate returned (zero, nil) for a path that is not a library audio
// file, and every caller reads a nil error as "the write happened":
//
//	res, err := applyFileUpdate(...)
//	if err != nil { ...; continue }
//	report.addDone(...)
//
// So a select_data row naming a cover.jpg, a .lrc, or anything without a
// library audio extension was reported as {status: "updated"} while not a
// single byte was written. Nothing in the response let a caller tell the
// difference, so the UI's "成功 N 首" counted writes that never happened.
//
// The trigger is easy to hit by accident: BatchUpdateID3 resolves each row's
// target as SafeJoin(baseDir, row.name), so a row whose `name` is a title
// rather than an existing filename addresses a path that does not exist —
// which is also not an audio file, and was likewise reported as updated.
func TestBatchUpdateID3_DoesNotReportNonAudioAsUpdated(t *testing.T) {
	t.Setenv("MUSIC_DIR", t.TempDir())
	dir := filepath.Join(mustMusicRoot(t), "Album")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	realAudio := testaudio.SeedMP3(t, dir, "real.ogg")
	// A real non-audio file, so this row is refused because of its TYPE.
	// Without it the row would be refused for being missing, which is a
	// different reason with a different remedy, and the assertion about
	// telling the two apart would pass vacuously.
	cover := filepath.Join(dir, "cover.jpg")
	if err := os.WriteFile(cover, []byte("\xff\xd8\xff\xe0 not really a jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, _ := setupAuditRouter(t)
	data := postBatch(t, r, map[string]interface{}{
		"file_full_path": "Album",
		"music_info":     map[string]interface{}{"title": "T", "artist": "A"},
		"select_data": []map[string]interface{}{
			// A sidecar, not an audio file.
			{"name": "cover.jpg"},
			// A name that does not exist and carries no audio extension.
			{"name": "Some Title"},
			// A real audio file, as the control.
			{"name": "real.ogg"},
		},
	})

	done := entries(t, data, "done")
	skipped := entries(t, data, "skipped")

	if len(done) != 1 || done[0]["file_full_path"] != "Album/real.ogg" {
		t.Fatalf("done = %+v, want exactly the one real audio file", done)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %+v, want the two non-audio rows", skipped)
	}

	gotSkipped := map[string]string{}
	gotReason := map[string]string{}
	for _, s := range skipped {
		status, _ := s["status"].(string)
		reason, _ := s["reason"].(string)
		p, _ := s["file_full_path"].(string)
		gotSkipped[p] = status
		gotReason[p] = reason
		if reason == "" {
			t.Errorf("skip for %s carries no reason; the client cannot explain it", p)
		}
		// A not-audio skip must not claim to be a duplicate: that is a
		// different verdict with a different remedy, and the frontend
		// surfaces the reason verbatim.
		if status == "duplicate" {
			t.Errorf("skip for %s is reported as a duplicate, but nothing was compared", p)
		}
	}
	for _, want := range []string{"Album/cover.jpg", "Album/Some Title"} {
		if _, ok := gotSkipped[want]; !ok {
			t.Errorf("no skip entry for %s; got %+v", want, gotSkipped)
		}
	}
	// The two refusals have different causes and different fixes, and the
	// frontend shows this string verbatim, so the wording has to tell them
	// apart. A single "not an audio file" for both leaves the user staring
	// at a cover.jpg being told it does not exist, or at a missing file
	// being told to check its extension.
	if r := gotReason["Album/cover.jpg"]; !strings.Contains(r, "不是音频文件") {
		t.Errorf("cover.jpg reason = %q, want it to say the file is not audio (it exists)", r)
	}
	if r := gotReason["Album/Some Title"]; !strings.Contains(r, "找不到文件") {
		t.Errorf("Some Title reason = %q, want it to say the file was not found (it is not audio either, but that is not the problem)", r)
	}

	// The row naming something that does not exist must not have conjured it
	// into being.
	if _, err := os.Stat(filepath.Join(dir, "Some Title")); err == nil {
		t.Error(`"Some Title" was created; a refused row must not create anything`)
	}
	if _, err := os.Stat(realAudio); err != nil {
		t.Errorf("the real audio file went missing: %v", err)
	}
	// The sidecar must survive untouched too, byte for byte: a refusal to
	// write tags is not a licence to delete or rewrite.
	got, err := os.ReadFile(cover)
	if err != nil {
		t.Fatalf("the sidecar was removed: %v", err)
	}
	if string(got) != "\xff\xd8\xff\xe0 not really a jpeg" {
		t.Errorf("the sidecar was rewritten: %q", got)
	}
}

// TestUpdateID3_DoesNotReportNonAudioAsUpdated covers the single-file
// endpoint, which shares applyFileUpdate but has its own error handling and
// its own audit row. A fix applied to only one of the two call sites would
// leave the other reporting phantom successes.
func TestUpdateID3_DoesNotReportNonAudioAsUpdated(t *testing.T) {
	t.Setenv("MUSIC_DIR", t.TempDir())
	dir := filepath.Join(mustMusicRoot(t), "Album")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, dir, "real.ogg")

	gin.SetMode(gin.TestMode)
	rr := gin.New()
	rr.POST("/update_id3/", UpdateID3)
	data := postJSON(t, rr, "/update_id3/", map[string]interface{}{
		"music_id3_info": []map[string]interface{}{
			{"file_full_path": "Album/cover.jpg", "title": "T"},
			{"file_full_path": "Album/real.ogg", "title": "T"},
		},
	})

	if done := entries(t, data, "done"); len(done) != 1 {
		t.Errorf("done = %+v, want only the real audio file", done)
	}
	skipped := entries(t, data, "skipped")
	if len(skipped) != 1 {
		t.Fatalf("skipped = %+v, want the non-audio row", skipped)
	}
	if p, _ := skipped[0]["file_full_path"].(string); p != "Album/cover.jpg" {
		t.Errorf("skip path = %q, want Album/cover.jpg", p)
	}
	if r, _ := skipped[0]["reason"].(string); r == "" {
		t.Error("skip carries no reason")
	}
}

func mustMusicRoot(t *testing.T) string {
	t.Helper()
	r := utils.MusicRoot()
	if r == "" {
		t.Fatal("MUSIC_DIR resolved to empty")
	}
	return r
}

// postJSON drives one handler and decodes the success envelope, so a spec can
// assert on the report buckets rather than the request it sent.
func postJSON(t *testing.T, r *gin.Engine, path string, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var env struct {
		Result bool                   `json:"result"`
		Data   map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if !env.Result {
		t.Fatalf("request failed: %s", w.Body.String())
	}
	return env.Data
}
