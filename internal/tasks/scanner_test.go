package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/db"
)

// TestFullScanSkipsSymlinks covers P1.5 issue F — H5: fullScan must NOT
// index paths whose final segment is a symlink, even when the segment has
// an audio extension. The earlier draft used a directory-style symlink
// (linkout/), which the existing IsDir() check would already filter;
// that didn't pin the regression. The actual H5 exploit chain is:
//
//  1. attacker places `<music>/evil.mp3` as a regular-named SYMLINK
//     to an arbitrary target (e.g. /etc/passwd).
//  2. fullScan iterates `os.ReadDir(music)`; e.IsDir() returns false
//     for the symlink (Lstat → ModeSymlink, not ModeDir), so the
//     `else` branch checks ext="mp3" → audioExt match → row recorded.
//  3. db.Track/Path now points INSIDE music, so SafeAbs(MediaRoot, ...)
//     at Stream time passes. http.ServeFile then follows the symlink
//     and leaks the target file content.
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

	gormDB := openTestDB(t)
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

// TestFullScan_Idempotent is the acceptance criterion for REVIEW.md P2-5:
// "重复全量扫描后行数不变".
//
// The pre-fix scanner minted a fresh uid per entry and blind-INSERTed it,
// so every run added a complete second (third, fourth...) copy of the
// library. This asserts the row count is stable across repeated scans, and
// — just as important — that the uids are stable too: children point at
// their parent's uid, so a scanner that only deduplicated by path while
// still churning uids would orphan the whole tree.
func TestFullScan_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	music := filepath.Join(tmp, "music")
	album := filepath.Join(music, "album")
	if err := os.MkdirAll(album, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		filepath.Join(music, "root.mp3"),
		filepath.Join(album, "one.mp3"),
		filepath.Join(album, "cover.jpg"),
		filepath.Join(album, "notes.txt"), // not indexed
	} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	gormDB := openTestDB(t)
	if err := gormDB.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatal(err)
	}

	h := &FullScanHandler{DB: gormDB, MusicRoot: music}
	snapshot := func() map[string]string {
		t.Helper()
		var rows []db.Folder
		if err := gormDB.Find(&rows).Error; err != nil {
			t.Fatalf("read folders: %v", err)
		}
		out := make(map[string]string, len(rows))
		for _, r := range rows {
			out[r.Path] = r.UID
		}
		return out
	}

	if err := h.fullScan(context.Background(), nil); err != nil {
		t.Fatalf("first fullScan: %v", err)
	}
	first := snapshot()
	// music root + album + root.mp3 + one.mp3 + cover.jpg
	if len(first) != 5 {
		t.Fatalf("first scan indexed %d paths, want 5: %v", len(first), first)
	}

	for run := 2; run <= 4; run++ {
		if err := h.fullScan(context.Background(), nil); err != nil {
			t.Fatalf("fullScan run %d: %v", run, err)
		}
		got := snapshot()
		if len(got) != len(first) {
			t.Fatalf("run %d changed the row count: got %d paths, want %d", run, len(got), len(first))
		}
		for path, uid := range first {
			if got[path] != uid {
				t.Errorf("run %d changed uid for %q: %q -> %q", run, path, uid, got[path])
			}
		}
	}

	// And the tree is still connected: every parent_id resolves to a live
	// row. A dedupe that kept row counts stable but let uids drift would
	// leave the library browsable as a pile of orphans.
	var rows []db.Folder
	if err := gormDB.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{}
	for _, r := range rows {
		live[r.UID] = true
	}
	for _, r := range rows {
		if r.ParentID != "" && !live[r.ParentID] {
			t.Errorf("row %q (%s) has dangling parent_id %q", r.UID, r.Path, r.ParentID)
		}
	}
	var song db.Folder
	if err := gormDB.Where("path = ?", filepath.Join(album, "one.mp3")).First(&song).Error; err != nil {
		t.Fatal(err)
	}
	var dir db.Folder
	if err := gormDB.Where("path = ?", album).First(&dir).Error; err != nil {
		t.Fatal(err)
	}
	if song.ParentID != dir.UID {
		t.Errorf("one.mp3 parent = %q, want the album dir uid %q", song.ParentID, dir.UID)
	}
}

// TestFullScan_RepeatedDirInSubPaths covers the intra-scan duplicate that
// upsert alone would turn into a constraint violation: a caller-supplied
// sub_paths list naming the same directory twice must not produce two rows
// fighting over the unique index on path.
func TestFullScan_RepeatedDirInSubPaths(t *testing.T) {
	tmp := t.TempDir()
	music := filepath.Join(tmp, "music")
	if err := os.MkdirAll(music, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(music, "a.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	gormDB := openTestDB(t)
	if err := gormDB.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatal(err)
	}

	h := &FullScanHandler{DB: gormDB, MusicRoot: music}
	dup := [][2]string{{"", music}, {"", music}}
	if err := h.fullScan(context.Background(), dup); err != nil {
		t.Fatalf("fullScan: %v", err)
	}
	var total int64
	gormDB.Model(&db.Folder{}).Count(&total)
	if total != 2 {
		t.Errorf("repeated sub_path produced %d rows, want 2 (music + a.mp3)", total)
	}
}

// TestFullScan_RefreshesExistingRows is the half of the upsert that a
// row-count assertion cannot see.
//
// With the unique index in place, a scanner that plain-INSERTs would make
// the second scan's batch fail and change nothing — the count stays right
// and the uids stay right, so TestFullScan_Idempotent would still pass. The
// distinguishing evidence is that the row is *rewritten* in place, so we
// scribble over the stored columns and require the next scan to restore
// them.
func TestFullScan_RefreshesExistingRows(t *testing.T) {
	tmp := t.TempDir()
	music := filepath.Join(tmp, "music")
	if err := os.MkdirAll(music, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(music, "a.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(music, "a.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	gormDB := openTestDB(t)
	if err := gormDB.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatal(err)
	}
	h := &FullScanHandler{DB: gormDB, MusicRoot: music}
	if err := h.fullScan(context.Background(), nil); err != nil {
		t.Fatalf("first fullScan: %v", err)
	}

	// Corrupt exactly the scan-derived columns the upsert owns.
	if err := gormDB.Model(&db.Folder{}).Where("1 = 1").Updates(map[string]any{
		"name": "STALE", "file_type": "bogus", "size": 999,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := h.fullScan(context.Background(), nil); err != nil {
		t.Fatalf("second fullScan: %v", err)
	}

	var rows []db.Folder
	if err := gormDB.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("row count changed: %d", len(rows))
	}
	for _, r := range rows {
		if r.Name == "STALE" || r.FileType == "bogus" || r.Size == 999 {
			t.Errorf("row %q was not refreshed in place: name=%q file_type=%q size=%d",
				r.Path, r.Name, r.FileType, r.Size)
		}
	}
}
