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
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/db"
	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
)

// setupAuditRouter wires BatchUpdateID3 against an in-memory audit DB, so a
// spec can assert on the row the handler wrote rather than on the request
// it was handed.
func setupAuditRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	audit.SetDB(gdb)
	t.Cleanup(func() { audit.SetDB(nil) })

	r.POST("/batch_update_id3/", BatchUpdateID3)
	return r, gdb
}

func postBatch(t *testing.T, r *gin.Engine, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/batch_update_id3/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var env struct {
		Result  bool                   `json:"result"`
		Data    map[string]interface{} `json:"data"`
		Message string                 `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if !env.Result {
		t.Fatalf("batch update failed: %s", w.Body.String())
	}
	return env.Data
}

// TestBatchUpdateID3_PerEntryMusicInfo pins the per-row override that makes
// this endpoint usable for auto-scrape.
//
// The original contract is one shared music_info applied to every selected
// file, which is right for "set these four fields on these forty files" and
// catastrophic for scraping: every track has its own title and artist, so a
// shared map writes track A's tags onto track B. A row carrying its own
// music_info must win, and only for that row.
//
// The shared music_info is sent as an EMPTY map, which is what the scrape
// path does: the field is `binding:"required"`, so it has to be present,
// but once every row overrides it there is nothing left to put in it. That
// an empty map survives validation is the load-bearing assumption here —
// if it did not, every scrape would fail at ShouldBindJSON.
func TestBatchUpdateID3_PerEntryMusicInfo(t *testing.T) {
	for _, shared := range []struct {
		name string
		info map[string]interface{}
	}{
		// The empty placeholder the scrape path actually sends.
		{"empty placeholder", map[string]interface{}{}},
		// A leftover from a caller that still fills it, to prove the
		// placeholder really is inert rather than accidentally harmless.
		{"populated placeholder", map[string]interface{}{"genre": "placeholder-must-not-be-used"}},
	} {
		t.Run(shared.name, func(t *testing.T) {
			music := t.TempDir()
			t.Setenv("MUSIC_DIR", music)

			leaf := filepath.Join(music, "Artist", "Album")
			if err := os.MkdirAll(leaf, 0o755); err != nil {
				t.Fatal(err)
			}
			testaudio.SeedMP3(t, leaf, "01 - First.mp3")
			testaudio.SeedMP3(t, leaf, "02 - Second.mp3")

			r, _ := setupAuditRouter(t)

			postBatch(t, r, map[string]interface{}{
				"file_full_path": "Artist/Album",
				"music_info":     shared.info,
				"select_data": []map[string]interface{}{
					{
						"name": "01 - First.mp3",
						"music_info": map[string]interface{}{
							"title":  "First Song",
							"artist": "Artist One",
							"genre":  "Rock",
						},
					},
					{
						"name": "02 - Second.mp3",
						"music_info": map[string]interface{}{
							"title":  "Second Song",
							"artist": "Artist Two",
							"genre":  "Jazz",
						},
					},
				},
			})

			for _, tc := range []struct{ file, title, artist, genre string }{
				{"01 - First.mp3", "First Song", "Artist One", "Rock"},
				{"02 - Second.mp3", "Second Song", "Artist Two", "Jazz"},
			} {
				info, err := tag.Read(filepath.Join(leaf, tc.file))
				if err != nil {
					t.Fatalf("%s: read back: %v", tc.file, err)
				}
				if info.Title != tc.title {
					t.Errorf("%s: title = %q, want %q", tc.file, info.Title, tc.title)
				}
				if info.Artist != tc.artist {
					t.Errorf("%s: artist = %q, want %q", tc.file, info.Artist, tc.artist)
				}
				if info.Genre != tc.genre {
					t.Errorf("%s: genre = %q, want %q", tc.file, info.Genre, tc.genre)
				}
			}
		})
	}
}

// TestBatchUpdateID3_AuditAction pins which audit.Action a batch records.
//
// Auto-scrape rides this endpoint but is a different event from a uniform
// hand-made edit. Before this, a 50-track scrape produced either 50
// anonymous update_id3 rows or one row indistinguishable from a manual
// batch — 操作审计 could not tell you a scrape had run.
func TestBatchUpdateID3_AuditAction(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, leaf, "01 - Song.mp3")

	for _, tc := range []struct {
		name   string
		action string
		want   string
	}{
		{"absent", "", audit.ActionBatchUpdateID3},
		{"explicit batch", audit.ActionBatchUpdateID3, audit.ActionBatchUpdateID3},
		{"auto scrape", audit.ActionAutoScrape, audit.ActionAutoScrape},
		// A stray value must not be able to invent an action the audit
		// UI has no config for, which would render as a blank row.
		{"unknown", "rm -rf", audit.ActionBatchUpdateID3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, gdb := setupAuditRouter(t)
			if _, err := audit.Clear(t.Context(), 0); err != nil {
				t.Fatalf("clear audit: %v", err)
			}
			_ = gdb

			body := map[string]interface{}{
				"file_full_path": "Artist/Album",
				"music_info":     map[string]interface{}{"title": "Song"},
				"select_data":    []map[string]interface{}{{"name": "01 - Song.mp3"}},
			}
			if tc.action != "" {
				body["action"] = tc.action
			}
			postBatch(t, r, body)

			rows, total, err := audit.Query(t.Context(), audit.QueryOptions{Page: 1, PageSize: 10})
			if err != nil {
				t.Fatalf("query audit: %v", err)
			}
			if total != 1 {
				t.Fatalf("audit rows = %d, want 1", total)
			}
			if rows[0].Action != tc.want {
				t.Errorf("action = %q, want %q", rows[0].Action, tc.want)
			}
		})
	}
}

// TestBatchUpdateID3_PerEntryAuditDetails guards the detail payload: once
// rows carry their own tags, the shared music_info is a placeholder, so
// recording it would describe a write that never happened.
func TestBatchUpdateID3_PerEntryAuditDetails(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, leaf, "01 - Song.mp3")

	r, _ := setupAuditRouter(t)
	postBatch(t, r, map[string]interface{}{
		"file_full_path": "Artist/Album",
		"music_info":     map[string]interface{}{"genre": "placeholder"},
		"action":         audit.ActionAutoScrape,
		"select_data": []map[string]interface{}{
			{
				"name":       "01 - Song.mp3",
				"music_info": map[string]interface{}{"title": "Song", "genre": "Rock"},
			},
		},
	})

	rows, _, err := audit.Query(t.Context(), audit.QueryOptions{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	// Details is stored as JSON text, not a decoded map.
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(rows[0].Details), &details); err != nil {
		t.Fatalf("decode details %q: %v", rows[0].Details, err)
	}
	if perEntry, _ := details["per_entry_music_info"].(bool); !perEntry {
		t.Errorf("per_entry_music_info = %v, want true", details["per_entry_music_info"])
	}
	if _, ok := details["music_info"]; ok {
		t.Errorf("music_info recorded alongside per-entry rows: %v", details["music_info"])
	}
}
