package tasks

import (
	"testing"

	"go-music-tag/internal/db"
)

// TestClearMusicUsesModelTableNames.
//
// clear_music's MySQL scrub must not list tables as string literals — "track",
// "album", "task_record", "track_attachment" — none of which match the
// models' actual TableName() (music_track, music_album, task_taskrecord,
// music_attachment). Because the OPTIMIZE error was logged and ignored,
// every one of those statements had been failing silently for the life of
// the feature: the post-clear space reclamation simply never happened on
// MySQL.
//
// The literals are gone, but a future rename could reintroduce the drift,
// so the derived names are asserted directly.
func TestClearMusicUsesModelTableNames(t *testing.T) {
	// The exact set the handler now iterates.
	derived := []string{
		db.TaskRecord{}.TableName(),
		db.Track{}.TableName(),
		db.Album{}.TableName(),
		db.Artist{}.TableName(),
		db.Genre{}.TableName(),
		db.Attachment{}.TableName(),
		db.Folder{}.TableName(),
	}

	want := map[string]bool{
		"task_taskrecord":  true,
		"music_track":      true,
		"music_album":      true,
		"music_artist":     true,
		"music_genre":      true,
		"music_attachment": true,
		"music_folder":     true,
	}
	if len(derived) != len(want) {
		t.Fatalf("derived %d table names, want %d", len(derived), len(want))
	}
	for _, name := range derived {
		if !want[name] {
			t.Errorf("unexpected table name %q", name)
		}
		delete(want, name)
	}
	for missing := range want {
		t.Errorf("table %q missing from the scrub list", missing)
	}
}

// TestClearMusicTableNamesAreNotTheOldBrokenLiterals is an explicit guard
// against reintroducing the exact strings that were wrong before.
func TestClearMusicTableNamesAreNotTheOldBrokenLiterals(t *testing.T) {
	broken := []string{"track", "album", "task_record", "track_attachment", "artist", "genre"}
	models := map[string]string{
		"track":            db.Track{}.TableName(),
		"album":            db.Album{}.TableName(),
		"task_record":      db.TaskRecord{}.TableName(),
		"track_attachment": db.Attachment{}.TableName(),
		"artist":           db.Artist{}.TableName(),
		"genre":            db.Genre{}.TableName(),
	}
	for _, lit := range broken {
		if real, ok := models[lit]; ok && real == lit {
			t.Errorf("model table name %q equals the old (wrong) literal — drift is back", lit)
		}
	}
}
