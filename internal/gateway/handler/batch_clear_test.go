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

	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
)

// TestBatchUpdateID3_ClearIsNullNotEmptyString pins the wire contract that
// lets a human clear a tag across a selection.
//
// Before this, an empty string meant "the caller had nothing to say" for
// every field except lyrics, so the only way to write a batch was to write
// values: there was no way to say "these forty files should not have a
// genre". A UI that unchecks a field and empties the box would have looked
// like it worked and silently done nothing, which is worse than not offering
// the control at all.
//
// The three states are told apart by the JSON key, never by the value:
//
//	absent            → leave the tag alone
//	null              → clear the tag
//	""                → leave the tag alone (unchanged legacy meaning)
//	"value"           → write it
//
// "" deliberately keeps its old meaning. The single-track form submits every
// field including untouched ones, and its store copy can be incomplete, so
// reading "" as "delete" would wipe tags the user never looked at.
func TestBatchUpdateID3_ClearIsNullNotEmptyString(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	song := testaudio.SeedMP3(t, leaf, "01 - Song.mp3")

	r, _ := setupAuditRouter(t)

	// Seed the fields this spec then manipulates. check_duplicate lives
	// INSIDE music_info: the request struct has no top-level field for it,
	// so a sibling would be dropped by JSON binding.
	postBatch(t, r, map[string]interface{}{
		"file_full_path": "Artist/Album",
		"music_info": map[string]interface{}{
			"title":           "Song Title",
			"artist":          "Artist Name",
			"album":           "Album Name",
			"genre":           "Progressive Rock",
			"year":            "1994",
			"comment":         "keep me",
			"check_duplicate": false,
		},
		"select_data": []map[string]interface{}{
			{"name": "01 - Song.mp3"},
		},
	})

	before, err := tag.Read(song)
	if err != nil {
		t.Fatalf("read after seed: %v", err)
	}
	if before.Genre == "" || before.Year == 0 {
		t.Fatalf("seed did not stick: genre=%q year=%d", before.Genre, before.Year)
	}

	// genre and year as JSON null = clear. title / artist / album /
	// comment are not mentioned at all, so they must survive untouched.
	postBatch(t, r, map[string]interface{}{
		"file_full_path": "Artist/Album",
		"music_info": map[string]interface{}{
			"genre":           nil,
			"year":            nil,
			"check_duplicate": false,
		},
		"select_data": []map[string]interface{}{
			{"name": "01 - Song.mp3"},
		},
	})

	after, err := tag.Read(song)
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if after.Genre != "" {
		t.Errorf("Genre = %q after clearing it with null; the clear was ignored", after.Genre)
	}
	if after.Year != 0 {
		t.Errorf("Year = %d after clearing it with null; the clear was ignored", after.Year)
	}
	// The whole point of "absent means leave alone": a clear request must
	// not double as a wipe-everything-else request.
	for _, keep := range []struct {
		field string
		got   string
		want  string
	}{
		{"Title", after.Title, "Song Title"},
		{"Artist", after.Artist, "Artist Name"},
		{"Album", after.Album, "Album Name"},
		{"Comment", after.Comment, "keep me"},
	} {
		if keep.got != keep.want {
			t.Errorf("%s = %q, want %q: a field the request never mentioned must survive", keep.field, keep.got, keep.want)
		}
	}
}

// TestBatchUpdateID3_EmptyStringStillMeansLeaveAlone is the other half of the
// contract above, and the reason clearing is null rather than "": the
// single-track form spreads its whole form into music_info, so every request
// carries a dozen keys the user never touched. If "" meant "delete", saving
// one track would delete every tag that track happened not to have.
func TestBatchUpdateID3_EmptyStringStillMeansLeaveAlone(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	song := testaudio.SeedMP3(t, leaf, "01 - Song.mp3")

	r, _ := setupAuditRouter(t)

	postBatch(t, r, map[string]interface{}{
		"file_full_path": "Artist/Album",
		"music_info": map[string]interface{}{
			"title":           "Song Title",
			"genre":           "Progressive Rock",
			"check_duplicate": false,
		},
		"select_data": []map[string]interface{}{
			{"name": "01 - Song.mp3"},
		},
	})

	// Shaped like TrackInspector's payload: every field present, the ones
	// the user did not touch left as "".
	postBatch(t, r, map[string]interface{}{
		"file_full_path": "Artist/Album",
		"music_info": map[string]interface{}{
			"title":           "Song Title",
			"genre":           "",
			"album":           "",
			"year":            "",
			"check_duplicate": false,
		},
		"select_data": []map[string]interface{}{
			{"name": "01 - Song.mp3"},
		},
	})

	after, err := tag.Read(song)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if after.Genre != "Progressive Rock" {
		t.Errorf("Genre = %q, want it untouched: an empty string is not a clear request", after.Genre)
	}
}

// TestTagIntent_TellsTheThreeStatesApart covers the resolver directly, so the
// contract is pinned independently of whether any endpoint happens to reach
// it. "act" is the third return value: false means the caller had nothing to
// say about this field and must not touch it.
func TestTagIntent_TellsTheThreeStatesApart(t *testing.T) {
	nullInAJSONMap := map[string]interface{}{"genre": nil}

	cases := []struct {
		name      string
		key       string
		info      map[string]interface{}
		wantAct   bool
		wantClear bool
		wantValue string
	}{
		{"absent", "genre", map[string]interface{}{"title": "x"}, false, false, ""},
		{"null", "genre", nullInAJSONMap, true, true, ""},
		{"empty string", "genre", map[string]interface{}{"genre": ""}, false, false, ""},
		{"value", "genre", map[string]interface{}{"genre": "Rock"}, true, false, "Rock"},
		// JSON has no integer type, so a year arrives as a float64. The
		// resolver has to stringify it or every numeric field from the
		// browser would read as absent.
		{"number", "year", map[string]interface{}{"year": float64(1994)}, true, false, "1994"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, clear, act := tagIntent(tc.info, tc.key)
			if act != tc.wantAct || clear != tc.wantClear || v != tc.wantValue {
				t.Errorf("tagIntent = (%q, %v, %v), want (%q, %v, %v)",
					v, clear, act, tc.wantValue, tc.wantClear, tc.wantAct)
			}
		})
	}
}

// TestBatchUpdateID3_EmptyBasePathWithNestedNames pins the payload shape the
// batch-edit dialog actually sends, which no other spec here used.
//
// Every other test in this package sends `file_full_path: "<dir>"` with
// leaf names. The dialog cannot: its selection routinely spans directories
// ("every track I just dragged in"), and one request is the whole point —
// splitting by parent directory would mean one audit row per album and a
// partial failure the user has to reconcile.
//
// So it sends an empty base and full relative paths, and the handler has to
// resolve a name with slashes in it. It does: SafeJoin joins then
// re-checks containment after Clean, so `Artist/Album/01.mp3` resolves
// normally and `../escape.mp3` is still refused. Both halves are asserted
// here, because the second is the reason the first is allowed to work.
func TestBatchUpdateID3_EmptyBasePathWithNestedNames(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	for _, dir := range []string{"Artist/Album", "Other/Album"} {
		if err := os.MkdirAll(filepath.Join(music, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	nested := testaudio.SeedMP3(t, filepath.Join(music, "Artist", "Album"), "01 - One.mp3")
	elsewhere := testaudio.SeedMP3(t, filepath.Join(music, "Other", "Album"), "02 - Two.mp3")

	r, _ := setupAuditRouter(t)

	postBatch(t, r, map[string]interface{}{
		"file_full_path": "",
		"music_info": map[string]interface{}{
			"album":           "Shared Album",
			"check_duplicate": false,
		},
		"select_data": []map[string]interface{}{
			{"name": "Artist/Album/01 - One.mp3"},
			{"name": "Other/Album/02 - Two.mp3"},
		},
	})

	for _, path := range []string{nested, elsewhere} {
		info, err := tag.Read(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if info.Album != "Shared Album" {
			t.Errorf("%s: Album = %q, want %q — a nested name did not resolve against an empty base path",
				filepath.Base(path), info.Album, "Shared Album")
		}
	}

	// The escape attempt, which must be refused rather than resolved.
	// Dedup is off so nothing else can be the reason it fails.
	body := map[string]interface{}{
		"file_full_path": "",
		"music_info":     map[string]interface{}{"album": "Escaped", "check_duplicate": false},
		"select_data":    []map[string]interface{}{{"name": "../../etc/passwd"}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/batch_update_id3/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Decoded loosely because the error envelope carries `data` as an
	// array, which the success-shaped helper above cannot unmarshal.
	var env struct {
		Result  bool   `json:"result"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if env.Result {
		t.Fatalf("a name escaping MUSIC_DIR was accepted: %s", w.Body.String())
	}
	// Not just "it failed". Allowing an empty base path is only defensible
	// while containment still holds, and a refusal for any other reason
	// (no such file, not an audio file) would mean this test is passing for
	// the wrong reason while the escape goes through unnoticed.
	if !strings.Contains(env.Message, "路径不安全") {
		t.Errorf("escape refused, but not by the containment check: message = %q", env.Message)
	}
}
