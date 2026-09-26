package tasks

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"gorm.io/gorm"

	"go-music-tag/internal/db"
	"go-music-tag/internal/testaudio"
)

// requireFpcalcForIndex skips when the binary is absent — without it the
// indexer is a no-op and every assertion about what it wrote would pass
// vacuously.
func requireFpcalcForIndex(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fpcalc"); err != nil {
		t.Skip("fpcalc not installed; the indexer is a no-op on this host")
	}
}

// testaudioSeed writes a real decodable WAV and returns its path.
func testaudioSeed(t *testing.T, dir, name string, seconds float64) string {
	t.Helper()
	return testaudio.SeedWAV(t, dir, name, seconds)
}

// fileSizeOf stats a fixture, failing the test rather than seeding a
// size=0 row (which every query filters out, so the test would pass for the
// wrong reason).
func fileSizeOf(t *testing.T, p string) int64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	if fi.Size() == 0 {
		t.Fatalf("%s is empty; a size=0 row is filtered out of every query", p)
	}
	return fi.Size()
}

// Tests that exist because mutation testing said the obvious version was not
// enough. Each one covers a case the neighbouring test happened not to
// distinguish.

// TestFpIndexHandler_IndexesDownloadedRowsInTheLibrary is the regression test
// for the file_type bug, from the indexer's side.
//
// Every other fixture here seeds file_type='music', because that is what the
// scanner writes — so a filter reading `file_type = 'music'` passed every one
// of them. The rows that actually broke were the ones yt_dl wrote, carrying
// the download source instead. Those files are in the library, they are
// audio, and they must get a duration or the fingerprint stage never
// considers them.
func TestFpIndexHandler_IndexesDownloadedRowsInTheLibrary(t *testing.T) {
	gdb := newFpIndexDB(t)
	dir := t.TempDir()

	downloaded := testaudioSeed(t, dir, "downloaded.wav", 5)
	// The value the downloader used to write. A row still carrying it in an
	// existing database is the whole reason the filter had to change: an
	// AutoMigrate does not rewrite history.
	seedFolderRow(t, gdb, downloaded, "youtube", fileSizeOf(t, downloaded))

	if err := (&FpIndexHandler{DB: gdb}).ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}

	if d := durationOf(t, gdb, downloaded); d <= 0 {
		t.Errorf("duration for a downloaded library track = %d, want > 0 — with no "+
			"duration it is invisible to the fingerprint stage's candidate query", d)
	}
}

// A stale cached fingerprint must send the file back through the indexer.
// This is the repair path for a re-encode in place: the path, the row and the
// duration all survive, so nothing else would ever look at the file again.
func TestFpIndexHandler_ReindexesAFileWhoseCacheIsStale(t *testing.T) {
	requireFpcalcForIndex(t)
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	p := testaudioSeed(t, dir, "song.wav", 5)
	seedFolderRow(t, gdb, p, "music", fileSizeOf(t, p))

	h := &FpIndexHandler{DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if d := durationOf(t, gdb, p); d <= 0 {
		t.Fatalf("first run left duration at %d", d)
	}

	// Rewrite the file in place with DIFFERENT audio of a DIFFERENT
	// length, so both the size and the mtime move. This is what a
	// re-encode looks like to the index.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	testaudioSeed(t, dir, "song.wav", 9)
	if err := gdb.Table("music_folder").Where("path = ?", p).
		Update("size", fileSizeOf(t, p)).Error; err != nil {
		t.Fatal(err)
	}

	stale, err := h.staleIndexedFiles()
	if err != nil {
		t.Fatalf("staleIndexedFiles: %v", err)
	}
	if len(stale) != 1 || stale[0].Path != p {
		t.Fatalf("staleIndexedFiles = %+v, want exactly %q — a changed file must be "+
			"queued for re-indexing or its old fingerprint outlives its audio", stale, p)
	}
}

// The complement: an untouched file must NOT be re-queued. Otherwise every
// re-arm re-decodes the entire library, which is the cost the cache exists to
// avoid.
func TestFpIndexHandler_DoesNotReindexAnUntouchedFile(t *testing.T) {
	requireFpcalcForIndex(t)
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	p := testaudioSeed(t, dir, "song.wav", 5)
	seedFolderRow(t, gdb, p, "music", fileSizeOf(t, p))

	h := &FpIndexHandler{DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	stale, err := h.staleIndexedFiles()
	if err != nil {
		t.Fatalf("staleIndexedFiles: %v", err)
	}
	for _, r := range stale {
		if r.Path == p {
			t.Errorf("%q was reported stale immediately after being indexed — every "+
				"re-arm would re-decode the whole library", p)
		}
	}
}

// A change that moves only the size must also be caught. Restoring the
// original mtime after a rewrite isolates the size arm: a size-only check
// misses it, and an mtime-only check misses the reverse case, so both arms
// need their own fixture.
func TestFpIndexHandler_StalenessTracksSizeIndependently(t *testing.T) {
	requireFpcalcForIndex(t)
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	p := testaudioSeed(t, dir, "song.wav", 5)
	seedFolderRow(t, gdb, p, "music", fileSizeOf(t, p))

	h := &FpIndexHandler{DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	orig, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	origMTime := orig.ModTime()

	// Different audio, so a different byte count, then put the mtime back
	// exactly where it was.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	testaudioSeed(t, dir, "song.wav", 11)
	if err := os.Chtimes(p, origMTime, origMTime); err != nil {
		t.Skipf("chtimes unavailable: %v", err)
	}
	now, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if now.Size() == orig.Size() {
		t.Skip("fixture produced the same size; the size arm is not isolated here")
	}
	if now.ModTime() != origMTime {
		t.Skip("filesystem refused to restore the mtime; the size arm is not isolated here")
	}

	stale, err := h.staleIndexedFiles()
	if err != nil {
		t.Fatalf("staleIndexedFiles: %v", err)
	}
	found := false
	for _, r := range stale {
		if r.Path == p {
			found = true
		}
	}
	if !found {
		t.Error("a same-mtime file with a different size was not reported stale — an " +
			"mtime-only check misses a rewrite that preserves the timestamp")
	}
}

// A touch that preserves both size and mtime is not a change; a touch that
// moves only the mtime IS. The second case is the one a size-only check
// misses, and it is the common one: a tag editor rewriting a file in place
// often produces the same byte count.
func TestFpIndexHandler_StalenessTracksMTimeIndependently(t *testing.T) {
	requireFpcalcForIndex(t)
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	p := testaudioSeed(t, dir, "song.wav", 5)
	seedFolderRow(t, gdb, p, "music", fileSizeOf(t, p))

	h := &FpIndexHandler{DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Same byte count, newer mtime.
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Skipf("chtimes unavailable: %v", err)
	}

	stale, err := h.staleIndexedFiles()
	if err != nil {
		t.Fatalf("staleIndexedFiles: %v", err)
	}
	found := false
	for _, r := range stale {
		if r.Path == p {
			found = true
		}
	}
	if !found {
		t.Error("a same-size file with a newer mtime was not reported stale — a " +
			"size-only check misses an in-place rewrite, which is what a tag edit is")
	}
}

// A library indexed before the fingerprint columns existed has a duration on
// every row and no fingerprint on any of them. The duration query never
// looks at those rows again, so treating "no cache yet" as "nothing to do"
// leaves every pre-existing library permanently uncached — which is the
// entire cost the cache was added to remove.
func TestFpIndexHandler_BackfillsFingerprintsForPreFingerprintRows(t *testing.T) {
	requireFpcalcForIndex(t)
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	p := testaudioSeed(t, dir, "legacy.wav", 5)
	seedFolderRow(t, gdb, p, "music", fileSizeOf(t, p))

	// Exactly the state an upgrade leaves behind: duration known, no blob.
	if err := gdb.Table("music_folder").Where("path = ?", p).
		Update("duration", 5).Error; err != nil {
		t.Fatal(err)
	}
	if len(cacheBlob(t, gdb, p)) != 0 {
		t.Fatal("fixture already has a fingerprint; it is not testing the backfill")
	}

	h := &FpIndexHandler{DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}

	if got := len(cacheBlob(t, gdb, p)); got == 0 {
		t.Error("a row with a duration but no cached fingerprint was left uncached — " +
			"every library indexed before the fp columns existed stays that way")
	}
}

func cacheBlob(t *testing.T, gdb *gorm.DB, path string) []byte {
	t.Helper()
	var row db.Folder
	if err := gdb.Table("music_folder").
		Select("fingerprint", "fp_size", "fp_mtime").
		Where("path = ?", path).Scan(&row).Error; err != nil {
		t.Fatalf("read cache row: %v", err)
	}
	return row.Fingerprint
}
