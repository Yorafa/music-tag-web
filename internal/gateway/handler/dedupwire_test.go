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

	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
)

// dupPost drives BatchUpdateID3 and returns the decoded `data` payload.
func dupPost(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/batch_update_id3/", BatchUpdateID3)

	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/batch_update_id3/", bytes.NewReader(raw))
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

func entries(t *testing.T, data map[string]interface{}, key string) []map[string]interface{} {
	t.Helper()
	raw, ok := data[key].([]interface{})
	if !ok {
		t.Fatalf("data[%q] missing or not an array: %#v", key, data[key])
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for _, v := range raw {
		m, _ := v.(map[string]interface{})
		out = append(out, m)
	}
	return out
}

// TestBatchUpdateID3_BlocksContentIdentical is the feature finally running.
//
// dedup.Checker was injected through a setter nothing called, so the whole
// four-stage funnel had never executed against a real library. It runs now,
// and with the default-on rule two byte-identical files must not both be
// written: the second is reported as a duplicate and the client can say so.
func TestBatchUpdateID3_BlocksContentIdentical(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, leaf, "one.mp3")
	// Same bytes under a different name: not a filename clash, a content
	// clash, which is the only kind allowed to refuse a write.
	b, err := os.ReadFile(filepath.Join(leaf, "one.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "copy.mp3"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	data := dupPost(t, map[string]interface{}{
		"file_full_path": "Artist/Album",
		"music_info":     map[string]interface{}{"title": "T"},
		"select_data": []map[string]interface{}{
			{"name": "one.mp3"},
			{"name": "copy.mp3"},
		},
	})

	if got := len(entries(t, data, "skipped")); got == 0 {
		t.Errorf("expected a duplicate to be skipped, got skipped=%#v done=%#v",
			data["skipped"], data["done"])
	}
	for _, sk := range entries(t, data, "skipped") {
		if sk["verdict"] != "duplicate" {
			t.Errorf("skip verdict = %v, want duplicate", sk["verdict"])
		}
	}
}

// TestBatchUpdateID3_WarnsButWritesOnNameClash is the other half: weak
// evidence must not cost the user their save. Two different recordings that
// happen to share a name get their tags written, plus a warning naming the
// other file.
func TestBatchUpdateID3_WarnsButWritesOnNameClash(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	first := testaudio.SeedMP3(t, leaf, "track.mp3")
	other := filepath.Join(music, "Artist", "Other", "track.mp3")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	// Same name, different content.
	raw[len(raw)-1] ^= 0xFF
	if err := os.WriteFile(other, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	data := dupPost(t, map[string]interface{}{
		"file_full_path": "Artist/Album",
		"music_info":     map[string]interface{}{"title": "Real Title"},
		"select_data":    []map[string]interface{}{{"name": "track.mp3"}},
	})

	if got := len(entries(t, data, "done")); got != 1 {
		t.Errorf("done = %d, want 1 — a name clash must not refuse the write (%#v)",
			got, data["skipped"])
	}
	warns := entries(t, data, "duplicate_warnings")
	if len(warns) != 1 {
		t.Fatalf("duplicate_warnings = %#v, want exactly 1", data["duplicate_warnings"])
	}
	if warns[0]["match_field"] != "filename" {
		t.Errorf("match_field = %v, want filename", warns[0]["match_field"])
	}
	if warns[0]["duplicate_path"] == "" {
		t.Error("warning should name the clashing file so the user can act on it")
	}
	// And the write really landed, not just a report entry.
	info, err := tag.Read(first)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Real Title" {
		t.Errorf("title on disk = %q, want %q", info.Title, "Real Title")
	}
}

// TestUpdateID3_RespectsOptOut is the escape hatch. check_duplicate:false has
// to win even for byte-identical audio, or a user who knows what they are
// doing has no way through.
func TestUpdateID3_RespectsOptOut(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, leaf, "one.mp3")
	b, err := os.ReadFile(filepath.Join(leaf, "one.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "copy.mp3"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	data := dupPost(t, map[string]interface{}{
		"file_full_path": "Artist/Album",
		"music_info":     map[string]interface{}{"title": "T", "check_duplicate": false},
		"select_data": []map[string]interface{}{
			{"name": "one.mp3"},
			{"name": "copy.mp3"},
		},
	})

	if got := len(entries(t, data, "done")); got != 2 {
		t.Errorf("done = %d, want 2 — check_duplicate:false must write through (%#v)",
			got, data["skipped"])
	}
}
