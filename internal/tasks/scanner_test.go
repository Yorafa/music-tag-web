package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/db"
)

// TestFullScanSkipsSymlinks covers P1.5 issue F — H5: fullScan must NOT
// index paths whose final segment is a symlink, even when the segment has
// an audio extension. The earlier draft used a directory-style symlink
// (linkout/), which the existing IsDir() check would already filter;
// that didn't pin the regression. The actual H5 exploit chain is:
//
//   1. attacker places `<music>/evil.mp3` as a regular-named SYMLINK
//      to an arbitrary target (e.g. /etc/passwd).
//   2. fullScan iterates `os.ReadDir(music)`; e.IsDir() returns false
//      for the symlink (Lstat → ModeSymlink, not ModeDir), so the
//      `else` branch checks ext="mp3" → audioExt match → row recorded.
//   3. db.Track/Path now points INSIDE music, so SafeAbs(MediaRoot, ...)
//      at Stream time passes. http.ServeFile then follows the symlink
//      and leaks the target file content.
//
// The post-H5 fix is to bail at scanner time before any record is
// written. We assert here that the symlink-named `.mp3` does NOT
// produce a row, while a real `.mp3` in the same dir DOES.
func TestFullScanSkipsSymlinks(t *testing.T) {
	tmp := t.TempDir()
	music := filepath.Join(tmp, "music")
	outside := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(music, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	// Sentinel inside outside — must never appear in db.Folder.
	secretAbs := filepath.Join(outside, "secret.mp3")
	if err := os.WriteFile(secretAbs, []byte("off-tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlink whose NAME has an audio extension (the actual exploit
	// shape). Without the H5 fix, fullScan would record a row whose
	// Path = `music/track.mp3`, then http.ServeFile would dereference
	// the symlink at Stream time and leak secret.mp3 content.
	trackLink := filepath.Join(music, "track.mp3")
	if err := os.Symlink(secretAbs, trackLink); err != nil {
		t.Skipf("symlink not supported in this env: %v", err)
	}
	// Real audio file in music — must be indexed exactly once.
	real := filepath.Join(music, "real.mp3")
	if err := os.WriteFile(real, []byte("on-tree"), 0o644); err != nil {
		t.Fatal(err)
	}

	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gormDB.AutoMigrate(
		&db.Folder{}, &db.Task{}, &db.TaskRecord{}, &db.Track{}, &db.Album{},
		&db.Artist{}, &db.Genre{}, &db.Attachment{}, &db.User{},
		&db.UserProfile{}, &db.Playlist{}, &db.TrackFavorite{}, &db.PlaylistTrack{},
	); err != nil {
		t.Fatal(err)
	}

	h := &FullScanHandler{DB: gormDB, MusicRoot: music}
	if err := h.fullScan(context.Background(), nil); err != nil {
		t.Fatalf("fullScan: %v", err)
	}

	// Outside-prefix rows MUST be zero regardless of fix (scanner never
	// stores outside-path in the `Path` column without the actual
	// exploit chain running at Stream time).
	var outsideRows int64
	gormDB.Model(&db.Folder{}).Where("path LIKE ?", outside+"%").Count(&outsideRows)
	if outsideRows != 0 {
		t.Errorf("scanner left rows referencing outside dir: %d", outsideRows)
	}

	// Real file MUST be indexed.
	var realRows int64
	gormDB.Model(&db.Folder{}).Where("path = ?", real).Count(&realRows)
	if realRows != 1 {
		t.Errorf("real.mp3 should be indexed once, got %d rows", realRows)
	}

	// Critical regression check: the symlink-named track.mp3 MUST NOT
	// be indexed. Pre-fix scanners record it (IsDir() false + mp3 ext
	// match); post-fix scanners skip via the symlink guard.
	var linkRows int64
	gormDB.Model(&db.Folder{}).Where("path = ?", trackLink).Count(&linkRows)
	if linkRows != 0 {
		t.Errorf("scanner indexed a symlink-named audio file %q: %d rows (Stream would deref and leak)", trackLink, linkRows)
	}

	// Sanity: total folder rows == 2 (music root + real.mp3). linkout
	// is not counted because no rows for it.
	var totalRows int64
	gormDB.Model(&db.Folder{}).Count(&totalRows)
	if totalRows != 2 {
		t.Errorf("expected 2 folder rows (music root + real.mp3), got %d", totalRows)
	}
}
