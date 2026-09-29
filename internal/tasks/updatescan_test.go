package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newScanTestDB builds an in-memory sqlite with just the Folder model.
func newScanTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "scan.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

// TestUpdateScan_RecordsAudioFiles.
//
// updateScan must not `os.ReadDir(dir)` and skip on error *before*
// checking whether dir was a file. ReadDir on a file always returns
// ENOTDIR, so the `if !isDir` branch was unreachable: an incremental scan
// of a subtree of plain files recorded nothing at all.
//
// The fix type-checks with os.Stat first and only then ReadDirs, so this
// asserts the file rows actually land in the DB.
func TestUpdateScan_RecordsAudioFiles(t *testing.T) {
	music := t.TempDir()

	// A flat subtree: no subdirectories at all, which is precisely the
	// shape the old code could not record.
	sub := filepath.Join(music, "album")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.mp3", "b.FLAC", "c.flac", "cover.jpg", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	d := newScanTestDB(t)
	h := &UpdateScanHandler{DB: d, MusicRoot: music}

	// Scan the subtree directly, as a targeted incremental scan would.
	err := h.updateScan(context.Background(), [][2]string{{"", sub}})
	if err != nil {
		t.Fatalf("updateScan: %v", err)
	}

	var rows []db.Folder
	if err := d.Find(&rows).Error; err != nil {
		t.Fatalf("query: %v", err)
	}

	byPath := map[string]db.Folder{}
	for _, r := range rows {
		byPath[r.Path] = r
	}

	// The audio files must be present, with the right file_type. Note
	// "b.FLAC" exercises the case-folding: a lowercase-only comparison
	// would drop it.
	for _, want := range []struct{ name, fileType string }{
		{"a.mp3", "music"},
		{"b.FLAC", "music"},
		{"c.flac", "music"},
		{"cover.jpg", "image"},
	} {
		p := filepath.Join(sub, want.name)
		r, ok := byPath[p]
		if !ok {
			t.Errorf("no row recorded for %s (P2-4 regression)", p)
			continue
		}
		if r.FileType != want.fileType {
			t.Errorf("%s file_type=%q, want %q", p, r.FileType, want.fileType)
		}
		if r.UID == "" {
			t.Errorf("%s has an empty UID", p)
		}
	}

	// Non-media must not be recorded.
	if _, ok := byPath[filepath.Join(sub, "notes.txt")]; ok {
		t.Error("notes.txt was recorded; only audio + cover belong here")
	}

	if len(rows) < 4 {
		t.Errorf("recorded %d rows, want at least 4 (the media files)", len(rows))
	}
}

// TestUpdateScan_IsIdempotent pins that re-scanning the same subtree
// updates rather than duplicating — the incremental counterpart to P2-5.
func TestUpdateScan_IsIdempotent(t *testing.T) {
	music := t.TempDir()
	if err := os.WriteFile(filepath.Join(music, "song.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := newScanTestDB(t)
	h := &UpdateScanHandler{DB: d, MusicRoot: music}

	for i := 0; i < 3; i++ {
		if err := h.updateScan(context.Background(), [][2]string{{"", music}}); err != nil {
			t.Fatalf("scan %d: %v", i, err)
		}
	}

	var count int64
	d.Model(&db.Folder{}).Where("path = ?", filepath.Join(music, "song.mp3")).Count(&count)
	if count != 1 {
		t.Errorf("song.mp3 has %d rows after 3 scans, want 1", count)
	}
}
