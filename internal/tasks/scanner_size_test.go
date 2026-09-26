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

// newScanDB builds the minimal schema the scan handlers touch.
func newScanDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gormDB.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatal(err)
	}
	return gormDB
}

// writeTrack creates a file with a deterministic non-zero size. Size 0 would
// pass a test that only checks "the column got written", so the payload has
// to be big enough that a missing assignment reads as 0 and fails.
func writeTrack(t *testing.T, dir, name string, nbytes int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, nbytes), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestScanRecordsFileSize is what makes the dedup index usable at all.
//
// db.Folder has carried a `size` column, and flushBatch's OnConflict clause
// has always listed "size" among the columns it refreshes — but neither
// fullScan nor updateScan ever assigned it. Every indexed file row therefore
// had size = 0, which is exactly the column internal/dedup selects candidates
// by (`WHERE size = ?` for the hash stage, `size BETWEEN` for the
// fingerprint stage).
//
// The consequence was silent: both stages queried a column that was
// structurally always 0, found nothing, and fell through to a full
// filesystem walk. Correct results, no index.
//
// Each subtest builds its own fixture. A shared one silently breaks this file:
// the first subtest rewrites the fixture file to test the rescan path, so a
// later subtest scanning the same directory never sees the original size and
// its assertion passes no matter what the handler does.
func TestScanRecordsFileSize(t *testing.T) {
	// Distinct sizes so a cross-row mix-up cannot pass by coincidence.
	for _, tc := range []struct {
		name    string
		handler string // "fullScan" | "updateScan"
	}{
		{"fullScan", "fullScan"},
		{"updateScan", "updateScan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			music := filepath.Join(t.TempDir(), "music")
			track := writeTrack(t, filepath.Join(music, "Album"), "a.ogg", 4096)
			cover := writeTrack(t, filepath.Join(music, "Album"), "cover.jpg", 777)
			other := writeTrack(t, filepath.Join(music, "Other"), "b.ogg", 1234)

			gormDB := newScanDB(t)
			runScan(t, tc.handler, gormDB, music, nil)

			for _, want := range []struct {
				path string
				size int64
			}{{track, 4096}, {cover, 777}, {other, 1234}} {
				var got db.Folder
				if err := gormDB.Where("path = ?", want.path).First(&got).Error; err != nil {
					t.Fatalf("no row for %s: %v", want.path, err)
				}
				if got.Size != want.size {
					t.Errorf("%s: size = %d, want %d",
						filepath.Base(want.path), got.Size, want.size)
				}
			}
		})
	}
}

// runScan drives one of the two scan handlers.
func runScan(t *testing.T, which string, gormDB *gorm.DB, music string, sub [][2]string) {
	t.Helper()
	var err error
	switch which {
	case "fullScan":
		err = (&FullScanHandler{DB: gormDB, MusicRoot: music}).fullScan(context.Background(), sub)
	case "updateScan":
		err = (&UpdateScanHandler{DB: gormDB, MusicRoot: music}).updateScan(context.Background(), sub)
	default:
		t.Fatalf("unknown scan %q", which)
	}
	if err != nil {
		t.Fatalf("%s: %v", which, err)
	}
}

// TestScanRefreshesFileSizeOnRescan pins the second half: a rescan of a file
// whose size changed must update the stored size.
//
// The insert path alone is not enough. fullScan's OnConflict clause lists
// "size" in DoUpdates so an INSERT-set value would survive a rescan — but
// updateScan takes a different branch for a path that already has a row, and
// that branch builds its own Updates map. A size added to only one of the
// two paths leaves a stale size in the index, which is worse than no size at
// all: the hash stage would then never consider the re-encoded file, and the
// duplicate would slip through.
func TestScanRefreshesFileSizeOnRescan(t *testing.T) {
	for _, which := range []string{"fullScan", "updateScan"} {
		t.Run(which, func(t *testing.T) {
			// Fresh fixture per subtest — see TestScanRecordsFileSize.
			music := filepath.Join(t.TempDir(), "music")
			track := writeTrack(t, filepath.Join(music, "Album"), "a.ogg", 4096)
			gormDB := newScanDB(t)

			runScan(t, which, gormDB, music, nil)

			if err := os.WriteFile(track, make([]byte, 9000), 0o644); err != nil {
				t.Fatal(err)
			}
			runScan(t, which, gormDB, music, nil)

			var got db.Folder
			if err := gormDB.Where("path = ?", track).First(&got).Error; err != nil {
				t.Fatal(err)
			}
			if got.Size != 9000 {
				t.Errorf("size after rescan = %d, want 9000", got.Size)
			}
		})
	}
}

// TestFullScanRecordsSizeForFilePassedDirectly covers fullScan's second
// file branch — the `else` that runs when the popped stack entry is a file
// rather than a directory.
//
// It is only reachable when a caller hands fullScan a file path in
// sub_paths, because the directory walk records file children inline and
// pushes only subdirectories. The gateway's FullScanPayload.SubPaths is
// exactly that shape ([(parentUID, path), ...]), and dedup's on-demand
// index refresh passes directory paths down the same route — so a file path
// reaching here is a supported input, not a theoretical one.
//
// Without this the branch is unverified: dropping the Size assignment there
// failed no test at all, which is how a second, separate always-zero
// assignment would have shipped next to the first.
func TestFullScanRecordsSizeForFilePassedDirectly(t *testing.T) {
	// Both file types, because the branch has two independent Size
	// assignments and covering only the audio one leaves the cover one
	// unverified.
	for _, tc := range []struct {
		name     string
		file     string
		wantSize int64
		wantType string
	}{
		{"music", "a.ogg", 5555, "music"},
		{"image", "cover.jpg", 313, "image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			music := filepath.Join(t.TempDir(), "music")
			file := writeTrack(t, filepath.Join(music, "Album"), tc.file, int(tc.wantSize))
			gormDB := newScanDB(t)

			// parentUID is a real folder's uid, matching how a caller
			// that expanded a directory would pass it.
			runScan(t, "fullScan", gormDB, music, [][2]string{{"parent-uid-0000", file}})

			var got db.Folder
			if err := gormDB.Where("path = ?", file).First(&got).Error; err != nil {
				t.Fatalf("no row for the directly-passed file %s: %v", file, err)
			}
			if got.Size != tc.wantSize {
				t.Errorf("size = %d, want %d", got.Size, tc.wantSize)
			}
			if got.FileType != tc.wantType {
				t.Errorf("file_type = %q, want %q", got.FileType, tc.wantType)
			}
			if got.ParentID != "parent-uid-0000" {
				t.Errorf("parent_id = %q, want \"parent-uid-0000\"", got.ParentID)
			}
		})
	}
}
