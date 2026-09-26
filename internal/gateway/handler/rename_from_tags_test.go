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

	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
)

// These tests drive the real handlers over a real temp library with real
// audio files. The behaviours worth protecting here are all about
// destroying or misplacing files, and a mock filesystem would prove
// nothing about either.

// newRenameFixture builds a library and returns its root.
//
// It writes tags through tag.Write rather than applyFileUpdate because
// applyFileUpdate runs a duplicate check, and the collision tests need
// two files carrying IDENTICAL tags — which is exactly what it refuses.
// The duplicate check is right for the API and wrong for a fixture.
func newRenameFixture(t *testing.T, files map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	// The handlers resolve paths through utils.MusicRoot(), which reads
	// MUSIC_DIR. Point it at the fixture for the duration of the test.
	t.Setenv("MUSIC_DIR", root)
	for rel, tags := range files {
		p := testaudio.SeedMP3(t, root, rel)
		upd := &tag.TagUpdate{}
		for k, v := range tags {
			val := v
			switch strings.ToLower(k) {
			case "title":
				upd.Title = &val
			case "album":
				upd.Album = &val
			case "albumartist":
				upd.AlbumArtist = &val
			case "genre":
				upd.Genre = &val
			case "year":
				upd.Year = &val
			case "tracknumber":
				upd.TrackNumber = &val
			case "discnumber":
				upd.DiscNumber = &val
			case "artist":
				upd.Artist = []string{val}
			default:
				t.Fatalf("fixture: unhandled tag %q", k)
			}
		}
		if err := tag.Write(p, upd); err != nil {
			t.Fatalf("seed tags for %s: %v", rel, err)
		}
	}
	return root
}

// renameJSON posts to one of the rename endpoints and returns the parsed
// envelope plus the raw body, so a test can assert on either the
// transport or the payload.
func renameJSON(t *testing.T, path string, body any) (map[string]any, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	c.Request.Header.Set("Content-Type", "application/json")
	if strings.Contains(path, "preview_rename") {
		PreviewRenameFromTags(c)
	} else {
		ApplyRenameFromTags(c)
	}
	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v (body %s)", path, err, w.Body.String())
	}
	return env, w.Code
}

// planRows pulls the row list out of a success envelope.
func planRows(t *testing.T, env map[string]any) []map[string]any {
	t.Helper()
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data in envelope: %v", env)
	}
	raw, _ := data["rows"].([]any)
	rows := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, _ := r.(map[string]any)
		rows = append(rows, m)
	}
	return rows
}

func listNames(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(p, ".mp3") {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPreviewRename_WritesNothing is the load-bearing guarantee: the
// preview is a dry run. If it renamed files, the whole feature would be
// unusable — you would be applying a 500-file rename to read the plan.
func TestPreviewRename_WritesNothing(t *testing.T) {
	root := newRenameFixture(t, map[string]map[string]string{
		"17/a.mp3": {"Title": "Sunny Day", "Artist": "Someone"},
	})
	before := listNames(t, root)

	env, _ := renameJSON(t, "/preview_rename_from_tags/", map[string]any{
		"paths":    []string{"17/a.mp3"},
		"template": "${artist} - ${title}",
	})
	rows := planRows(t, env)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0]["new_name"] != "Someone - Sunny Day.mp3" {
		t.Errorf("new_name = %v", rows[0]["new_name"])
	}
	if rows[0]["status"] != RenameOK {
		t.Errorf("status = %v", rows[0]["status"])
	}
	if data, _ := env["data"].(map[string]any); data["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", data["dry_run"])
	}
	if got := listNames(t, root); len(got) != len(before) || got[0] != before[0] {
		t.Errorf("preview touched the filesystem: %v -> %v", before, got)
	}
}

func TestApplyRename_RenamesAndKeepsExtension(t *testing.T) {
	root := newRenameFixture(t, map[string]map[string]string{
		"17/a.mp3": {"Title": "Sunny Day", "Artist": "Someone", "Genre": "Pop", "Year": "1994"},
	})
	env, _ := renameJSON(t, "/apply_rename_from_tags/", map[string]any{
		"paths":    []string{"17/a.mp3"},
		"template": "${artist} - ${year} - ${title}",
	})
	rows := planRows(t, env)
	if rows[0]["status"] != RenameOK {
		t.Fatalf("status = %v (%v)", rows[0]["status"], rows[0]["detail"])
	}
	got := listNames(t, root)
	// ${year} must be a real year, not the literal text "${year}".
	if len(got) != 1 || got[0] != filepath.Join("17", "Someone - 1994 - Sunny Day.mp3") {
		t.Errorf("files = %v", got)
	}
}

func TestApplyRename_UnknownFieldIsBlockedNotWritten(t *testing.T) {
	root := newRenameFixture(t, map[string]map[string]string{
		"17/a.mp3": {"Title": "T", "Artist": "A"},
	})
	env, _ := renameJSON(t, "/apply_rename_from_tags/", map[string]any{
		"paths":    []string{"17/a.mp3"},
		"template": "${artist} - ${album_artist} - ${title}",
	})
	rows := planRows(t, env)
	if rows[0]["status"] != RenameBlocked {
		t.Fatalf("status = %v, want blocked", rows[0]["status"])
	}
	if !strings.Contains(rows[0]["detail"].(string), "album_artist") {
		t.Errorf("detail should name the offending field: %v", rows[0]["detail"])
	}
	// The regression this whole handler exists for: a template naming a
	// field the server does not have must NOT produce a file with the
	// placeholder text in its name.
	if names := listNames(t, root); len(names) != 1 || names[0] != filepath.Join("17", "a.mp3") {
		t.Errorf("blocked row still renamed: %v", names)
	}
}

func TestApplyRename_MissingFieldRendersEmptyAndIsReported(t *testing.T) {
	newRenameFixture(t, map[string]map[string]string{
		"17/a.mp3": {"Title": "T", "Artist": "A"}, // no genre
	})
	env, _ := renameJSON(t, "/apply_rename_from_tags/", map[string]any{
		"paths":    []string{"17/a.mp3"},
		"template": "${artist} - ${genre} - ${title}",
	})
	rows := planRows(t, env)
	if rows[0]["status"] != RenameOK {
		t.Fatalf("status = %v", rows[0]["status"])
	}
	// Deliberate: the operator chose to see the gap rather than have it
	// silently collapsed, so the empty field leaves its separators.
	if rows[0]["new_name"] != "A -  - T.mp3" {
		t.Errorf("new_name = %v, want the gap preserved", rows[0]["new_name"])
	}
	miss, _ := rows[0]["missing"].([]any)
	if len(miss) != 1 || miss[0] != "genre" {
		t.Errorf("missing = %v, want [genre]", rows[0]["missing"])
	}
}

func TestApplyRename_NoChangeWhenTemplateReproducesTheName(t *testing.T) {
	newRenameFixture(t, map[string]map[string]string{
		"17/A - T.mp3": {"Title": "T", "Artist": "A"},
	})
	env, _ := renameJSON(t, "/apply_rename_from_tags/", map[string]any{
		"paths":    []string{"17/A - T.mp3"},
		"template": "${artist} - ${title}",
	})
	rows := planRows(t, env)
	if rows[0]["status"] != RenameNoChange {
		t.Errorf("status = %v, want no_change", rows[0]["status"])
	}
	if rows[0]["new_name"] != "A - T.mp3" {
		t.Errorf("new_name should be the current name: %v", rows[0]["new_name"])
	}
}

// TestApplyRename_BatchCollision is the failure that loses data.
// os.Rename replaces, so two rows rendering to the same target would
// leave one file and no error. The first row must win.
func TestApplyRename_BatchCollision(t *testing.T) {
	root := newRenameFixture(t, map[string]map[string]string{
		"17/one.mp3": {"Title": "Same", "Artist": "A"},
		"17/two.mp3": {"Title": "Same", "Artist": "A"},
	})
	env, _ := renameJSON(t, "/apply_rename_from_tags/", map[string]any{
		"paths":    []string{"17/one.mp3", "17/two.mp3"},
		"template": "${artist} - ${title}",
	})
	rows := planRows(t, env)
	if rows[0]["status"] != RenameOK {
		t.Fatalf("first row status = %v", rows[0]["status"])
	}
	if rows[1]["status"] != RenameTaken {
		t.Errorf("second row status = %v, want taken", rows[1]["status"])
	}
	// Both files must still exist — one renamed, one untouched.
	if names := listNames(t, root); len(names) != 2 {
		t.Errorf("a file was lost: %v", names)
	}
}

func TestApplyRename_SameNameInDifferentFoldersIsNotACollision(t *testing.T) {
	// The bug this guards: keying the collision map on the bare name
	// instead of the full target path blocks every track in a
	// multi-album library.
	root := newRenameFixture(t, map[string]map[string]string{
		"17/a.mp3":        {"Title": "Same", "Artist": "A"},
		"18 (Live)/a.mp3": {"Title": "Same", "Artist": "A"},
	})
	env, _ := renameJSON(t, "/apply_rename_from_tags/", map[string]any{
		"paths":    []string{"17/a.mp3", "18 (Live)/a.mp3"},
		"template": "${artist} - ${title}",
	})
	for _, r := range planRows(t, env) {
		if r["status"] != RenameOK {
			t.Errorf("row %v status = %v (%v), want ok", r["old_name"], r["status"], r["detail"])
		}
	}
	if names := listNames(t, root); len(names) != 2 {
		t.Errorf("files = %v", names)
	}
}

func TestApplyRename_ExistingTargetIsTaken(t *testing.T) {
	root := newRenameFixture(t, map[string]map[string]string{
		"17/a.mp3":     {"Title": "T", "Artist": "A"},
		"17/A - T.mp3": {"Title": "other", "Artist": "other"},
	})
	env, _ := renameJSON(t, "/apply_rename_from_tags/", map[string]any{
		"paths":    []string{"17/a.mp3"},
		"template": "${artist} - ${title}",
	})
	if rows := planRows(t, env); rows[0]["status"] != RenameTaken {
		t.Errorf("status = %v, want taken", rows[0]["status"])
	}
	if len(listNames(t, root)) != 2 {
		t.Error("a file was overwritten")
	}
}

func TestPreviewRename_EscapingPathIsRefused(t *testing.T) {
	newRenameFixture(t, map[string]map[string]string{"17/a.mp3": {"Title": "T"}})
	env, _ := renameJSON(t, "/preview_rename_from_tags/", map[string]any{
		"paths":    []string{"../../etc/passwd"},
		"template": "${title}",
	})
	rows := planRows(t, env)
	if rows[0]["status"] != RenameBlocked {
		t.Errorf("status = %v, want blocked", rows[0]["status"])
	}
}

func TestPreviewRename_RejectsUselessTemplates(t *testing.T) {
	newRenameFixture(t, map[string]map[string]string{"17/a.mp3": {"Title": "T"}})
	for _, tc := range []struct{ name, tmpl string }{
		{"empty", ""},
		{"no placeholders", "whatever"},
	} {
		env, _ := renameJSON(t, "/preview_rename_from_tags/", map[string]any{
			"paths": []string{"17/a.mp3"}, "template": tc.tmpl,
		})
		if env["result"] != false {
			t.Errorf("%s template was accepted: %v", tc.name, env)
		}
	}
}

func TestApplyRename_MovesSidecars(t *testing.T) {
	root := newRenameFixture(t, map[string]map[string]string{
		"17/a.mp3": {"Title": "T", "Artist": "A"},
	})
	// A lyric file named after the audio, which is what the library
	// lists; renaming the audio without it would orphan the lyrics.
	lyr := filepath.Join(root, "17", "a.lrc")
	if err := os.WriteFile(lyr, []byte("la la"), 0o644); err != nil {
		t.Fatal(err)
	}
	renameJSON(t, "/apply_rename_from_tags/", map[string]any{
		"paths": []string{"17/a.mp3"}, "template": "${artist} - ${title}",
	})
	if _, err := os.Stat(filepath.Join(root, "17", "A - T.lrc")); err != nil {
		t.Errorf("sidecar did not follow the rename: %v", err)
	}
	if _, err := os.Stat(lyr); !os.IsNotExist(err) {
		t.Errorf("old sidecar still present: %v", err)
	}
}
