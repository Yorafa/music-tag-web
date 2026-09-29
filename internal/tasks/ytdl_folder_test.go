package tasks

import (
	"path/filepath"
	"testing"
	"time"

	"go-music-tag/internal/db"
)

// TestUpsertDownloadFolder_AdoptsScannedPath covers the collision the
// unique index on Folder.Path creates for the download
// path: the scanner has usually already walked the destination, so the
// video lands on a path that already has a row — under a random scan uid,
// not the video id. A uid-keyed write plain fails
// on the unique constraint, and failing there fails the whole task even
// though the audio is already on disk.
func TestUpsertDownloadFolder_AdoptsScannedPath(t *testing.T) {
	gdb := openTestDB(t)
	if err := gdb.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatal(err)
	}
	music := filepath.Join(t.TempDir(), "music")
	dest := filepath.Join(music, "Artist", "Song.m4a")

	// What the scanner leaves behind.
	if err := gdb.Create(&db.Folder{
		Name: "Song.m4a", Path: dest, UID: "scan-uid-1", FileType: "music", State: "none",
	}).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if err := upsertDownloadFolder(gdb, db.Folder{
		UID: "yt-abc", Name: "Song.m4a", Path: dest, FileType: "youtube",
		Size: 1234, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsertDownloadFolder over a scanned path: %v", err)
	}

	var rows []db.Folder
	if err := gdb.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected the scanned row to be adopted, got %d rows: %+v", len(rows), rows)
	}
	got := rows[0]
	if got.UID != "yt-abc" {
		t.Errorf("uid = %q, want the video id to take over the free slot", got.UID)
	}
	if got.FileType != "youtube" || got.Size != 1234 {
		t.Errorf("row not refreshed: file_type=%q size=%d", got.FileType, got.Size)
	}
}

// TestUpsertDownloadFolder_KeepsUIDAlreadyHeld is the other direction: a
// re-download that lands at a *new* path finds the video id still held by
// the old row. Handing it over as well would leave two rows claiming the
// same uid and break the folder tree, so the scan row keeps its own.
func TestUpsertDownloadFolder_KeepsUIDAlreadyHeld(t *testing.T) {
	gdb := openTestDB(t)
	if err := gdb.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatal(err)
	}
	music := filepath.Join(t.TempDir(), "music")
	moved := filepath.Join(music, "Artist", "Song (renamed).m4a")

	// The video id still points at the old location...
	if err := gdb.Create(&db.Folder{
		UID: "yt-abc", Name: "Song.m4a", Path: filepath.Join(music, "Artist", "Song.m4a"),
		FileType: "youtube",
	}).Error; err != nil {
		t.Fatal(err)
	}
	// ...and the new location was indexed independently.
	if err := gdb.Create(&db.Folder{
		UID: "scan-uid-2", Name: "Song (renamed).m4a", Path: moved, FileType: "music",
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := upsertDownloadFolder(gdb, db.Folder{
		UID: "yt-abc", Name: "Song (renamed).m4a", Path: moved, FileType: "youtube", Size: 99,
	}); err != nil {
		t.Fatalf("upsertDownloadFolder: %v", err)
	}

	var rows []db.Folder
	if err := gdb.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("row count changed: %d", len(rows))
	}
	if rows[0].UID != "yt-abc" {
		t.Errorf("old row lost the video id to %q", rows[1].UID)
	}
	if rows[1].UID == "yt-abc" {
		t.Errorf("two rows now claim uid yt-abc; the folder tree would fork")
	}
	if rows[1].FileType != "youtube" || rows[1].Size != 99 {
		t.Errorf("new row not refreshed: file_type=%q size=%d", rows[1].FileType, rows[1].Size)
	}
}

// TestUpsertDownloadFolder_SameVideoNewPath covers the "uid known, path
// free" branch: a re-download that produced a differently-named file moves
// the existing row rather than leaving a second one behind.
func TestUpsertDownloadFolder_SameVideoNewPath(t *testing.T) {
	gdb := openTestDB(t)
	if err := gdb.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatal(err)
	}
	music := filepath.Join(t.TempDir(), "music")
	oldPath := filepath.Join(music, "Artist", "Song.m4a")
	newPath := filepath.Join(music, "Artist", "Song (1080p).m4a")
	if err := gdb.Create(&db.Folder{
		UID: "yt-abc", Name: "Song.m4a", Path: oldPath, FileType: "youtube", Size: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := upsertDownloadFolder(gdb, db.Folder{
		UID: "yt-abc", Name: "Song (1080p).m4a", Path: newPath, FileType: "youtube", Size: 2222,
	}); err != nil {
		t.Fatalf("upsertDownloadFolder: %v", err)
	}

	var rows []db.Folder
	if err := gdb.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected the row to move, got %d rows: %+v", len(rows), rows)
	}
	if rows[0].Path != newPath || rows[0].Size != 2222 {
		t.Errorf("row = path %q size %d, want %q / 2222", rows[0].Path, rows[0].Size, newPath)
	}
}

// TestUpsertDownloadFolder_InsertsWhenBothUnknown is the baseline branch,
// and the one a naive "update where uid = ?" implementation would silently
// turn into a no-op.
func TestUpsertDownloadFolder_InsertsWhenBothUnknown(t *testing.T) {
	gdb := openTestDB(t)
	if err := gdb.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatal(err)
	}
	if err := upsertDownloadFolder(gdb, db.Folder{
		UID: "yt-new", Name: "New.m4a", Path: filepath.Join(t.TempDir(), "New.m4a"),
		FileType: "youtube", Size: 7,
	}); err != nil {
		t.Fatalf("upsertDownloadFolder: %v", err)
	}
	var rows []db.Folder
	if err := gdb.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UID != "yt-new" || rows[0].Size != 7 {
		t.Fatalf("row not inserted: %+v", rows)
	}
}
