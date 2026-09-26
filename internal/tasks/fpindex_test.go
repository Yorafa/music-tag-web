package tasks

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/db"
	"go-music-tag/internal/testaudio"
)

// newFpIndexDB migrates just the tables this task touches.
func newFpIndexDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return gdb
}

// seedFolderRow inserts a music_folder row the way the scanner would.
// UID is unique-indexed, so it has to be distinct per row or the second
// insert fails on a constraint rather than on anything this task does.
func seedFolderRow(t *testing.T, gdb *gorm.DB, path, fileType string, size int64) {
	t.Helper()
	row := db.Folder{
		Path:     path,
		Name:     filepath.Base(path),
		Size:     size,
		FileType: fileType,
		UID:      fmt.Sprintf("%032x", sha256.Sum256([]byte(path))),
	}
	if err := gdb.Table("music_folder").Create(&row).Error; err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}

func durationOf(t *testing.T, gdb *gorm.DB, path string) int64 {
	t.Helper()
	var got int64
	if err := gdb.Table("music_folder").Where("path = ?", path).
		Pluck("duration", &got).Error; err != nil {
		t.Fatalf("read duration for %s: %v", path, err)
	}
	return got
}

// Without the binary the task must succeed quietly rather than fail: the
// fingerprint stage already degrades to skipping itself, and a task that
// retried into the dead-letter queue would add noise without adding an index.
func TestFpIndexHandler_NoFpcalcSucceedsQuietly(t *testing.T) {
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	seedFolderRow(t, gdb, filepath.Join(dir, "a.mp3"), "music", 10)

	h := &FpIndexHandler{DB: gdb, FPcalcPath: filepath.Join(dir, "no-such-fpcalc")}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Errorf("ProcessTask with no binary returned %v, want nil", err)
	}
}

func TestFpIndexHandler_NoDBIsAnError(t *testing.T) {
	h := &FpIndexHandler{}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err == nil {
		t.Error("a handler with no DB should report an error, not silently do nothing")
	}
}

// The headline behaviour: real durations land in the column the dedup
// candidate query reads.
func TestFpIndexHandler_WritesDurationForRealAudio(t *testing.T) {
	if _, err := exec.LookPath("fpcalc"); err != nil {
		t.Skip("fpcalc not installed; the indexer is a no-op on this host")
	}
	gdb := newFpIndexDB(t)
	dir := t.TempDir()

	// Two different lengths, so a test that only ever saw one value would
	// still catch a constant being written.
	short := testaudio.SeedWAV(t, dir, "short.wav", 3)
	long := testaudio.SeedWAV(t, dir, "long.wav", 7)
	for _, p := range []string{short, long} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat fixture: %v", err)
		}
		seedFolderRow(t, gdb, p, "music", fi.Size())
	}

	if err := (&FpIndexHandler{DB: gdb}).ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}

	shortDur := durationOf(t, gdb, short)
	longDur := durationOf(t, gdb, long)
	if shortDur < 2 || shortDur > 4 {
		t.Errorf("duration for the 3s fixture = %d, want ~3", shortDur)
	}
	if longDur < 6 || longDur > 8 {
		t.Errorf("duration for the 7s fixture = %d, want ~7", longDur)
	}
}

// Re-running must not redo work: the whole point of the column is that the
// expensive decode happens once per track.
func TestFpIndexHandler_SkipsRowsThatAlreadyHaveADuration(t *testing.T) {
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mp3")
	seedFolderRow(t, gdb, p, "music", 1234)
	if err := gdb.Table("music_folder").Where("path = ?", p).
		Update("duration", 4242).Error; err != nil {
		t.Fatalf("preset duration: %v", err)
	}

	if err := (&FpIndexHandler{DB: gdb}).ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if got := durationOf(t, gdb, p); got != 4242 {
		t.Errorf("duration = %d, want the preset 4242; the row was re-indexed", got)
	}
}

// Directory rows carry size 0 and a file_type of their own. Indexing them
// would put a meaningless duration in the column that the candidate query
// filters on.
func TestFpIndexHandler_IgnoresNonAudioRows(t *testing.T) {
	gdb := newFpIndexDB(t)
	dir := t.TempDir()

	// A .jpg inside a music row: the scanner's file_type and the extension
	// have drifted before, and spending 0.4s proving fpcalc cannot decode a
	// cover image is the cost of not checking.
	cover := filepath.Join(dir, "cover.jpg")
	if err := os.WriteFile(cover, []byte("not audio"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	seedFolderRow(t, gdb, cover, "music", 9)

	// A folder row, size 0 as the scanner writes it.
	sub := filepath.Join(dir, "Sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seedFolderRow(t, gdb, sub, "folder", 0)

	// An image row, which the extension check accepts as a path but the
	// file_type filter must not.
	img := filepath.Join(dir, "art.png")
	if err := os.WriteFile(img, []byte("png"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	seedFolderRow(t, gdb, img, "image", 3)

	if err := (&FpIndexHandler{DB: gdb}).ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	for _, p := range []string{cover, sub, img} {
		if d := durationOf(t, gdb, p); d != 0 {
			t.Errorf("duration for %s (%s) = %d, want 0", filepath.Base(p), p, d)
		}
	}
}

// A file fpcalc cannot decode must stay at duration 0, which is the marker
// the dedup query reads as "not indexed yet". Writing 0 as if it were a real
// measurement would make the row look indexed while remaining invisible to
// every duration query, and it would never be retried.
func TestFpIndexHandler_UndecodableFileStaysUnindexed(t *testing.T) {
	if _, err := exec.LookPath("fpcalc"); err != nil {
		t.Skip("fpcalc not installed")
	}
	gdb := newFpIndexDB(t)
	dir := t.TempDir()

	// A .mp3 by extension that is not audio by content.
	bad := filepath.Join(dir, "fake.mp3")
	if err := os.WriteFile(bad, []byte("this is not audio at all"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	seedFolderRow(t, gdb, bad, "music", 21)

	if err := (&FpIndexHandler{DB: gdb}).ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if d := durationOf(t, gdb, bad); d != 0 {
		t.Errorf("duration = %d, want 0 so the row is retried later", d)
	}
}
