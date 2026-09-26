package dedup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"go-music-tag/internal/db"
	"go-music-tag/internal/fingerprint"
	"go-music-tag/internal/testaudio"
)

// mustFpcalc resolves fpcalc or skips. Every cache test needs a real decode:
// a fixture that avoided fpcalc would leave the cache untested, which is the
// failure mode this file exists to prevent.
func mustFpcalc(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("fpcalc")
	if err != nil {
		t.Skip("fpcalc not installed; the cache is bypassed on this host")
	}
	return p
}

// cacheRow reads back the cache columns for a path.
//
// Scans into db.Folder, for the same reason readFpCache does: GORM maps the
// field FPMTime to "fpm_time", so a local struct would read a column that
// does not exist and report a zero mtime.
func cacheRow(t *testing.T, gormDB *gorm.DB, path string) db.Folder {
	t.Helper()
	var row db.Folder
	if err := gormDB.Table("music_folder").
		Select("fingerprint", "fp_size", "fp_mtime").
		Where("path = ?", path).
		Scan(&row).Error; err != nil {
		t.Fatalf("read cache row: %v", err)
	}
	return row
}

// The fingerprint cache.
//
// A check decodes the file under test plus every candidate in the ±5s
// duration window, at ~0.4s each. Without a cache, re-running the same check
// pays that whole bill again to reach the same verdict.
//
// The dangerous half is invalidation. A file re-encoded in place keeps its
// path and its music_folder row, so a cache with no validity check does not
// merely go stale — it keeps reporting the OLD track's duplicates for as
// long as the row lives, which is worse than never caching at all.

// TestFpCache_RoundTripPreservesTheFingerprint is the property the cache
// rests on: a stored fingerprint has to compare identically to the one that
// was decoded, or every cached verdict is subtly wrong.
func TestFpCache_RoundTripPreservesTheFingerprint(t *testing.T) {
	fpcalc := mustFpcalc(t)
	dir := t.TempDir()
	p := testaudio.SeedWAV(t, dir, "a.wav", 30)

	orig, err := fingerprint.Raw(context.Background(), fpcalc, p)
	if err != nil {
		t.Fatalf("Raw: %v", err)
	}
	blob := orig.Encode()
	if len(blob) <= fingerprint.EncodeHeaderBytes {
		t.Fatalf("Encode produced %d bytes, want more than the %d-byte header",
			len(blob), fingerprint.EncodeHeaderBytes)
	}
	if (len(blob)-fingerprint.EncodeHeaderBytes)%4 != 0 {
		t.Errorf("Encode produced %d bytes, leaving a partial subfingerprint", len(blob))
	}
	back, err := fingerprint.Decode(blob)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if back.Len() != orig.Len() {
		t.Fatalf("Len after round trip = %d, want %d", back.Len(), orig.Len())
	}
	if back.Duration() != orig.Duration() {
		t.Errorf("Duration after round trip = %d, want %d", back.Duration(), orig.Duration())
	}
	// Similarity against itself must be exactly 1 — a codec that lost or
	// reordered a value would still be "close" and the test would pass.
	s, ok := fingerprint.Similarity(orig, back)
	if !ok || s != 1.0 {
		t.Errorf("Similarity(orig, decoded) = %v (ok=%v), want exactly 1", s, ok)
	}
}

// A truncated or corrupt blob must be refused, not decoded partially. A
// short prefix would compare as "similar" — and short fingerprints are
// exactly what minSubfingerprints exists to reject.
func TestDecode_RejectsCorruptBlobs(t *testing.T) {
	for _, tc := range []struct {
		name string
		blob []byte
	}{
		{"empty", nil},
		{"shorter than the header", []byte{1, 2, 3}},
		{"header only, no subfingerprints", []byte{0, 0, 0, 5}},
		{"partial trailing subfingerprint", []byte{0, 0, 0, 5, 1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fingerprint.Decode(tc.blob); err == nil {
				t.Errorf("Decode(%d bytes) returned no error, want a rejection", len(tc.blob))
			}
		})
	}
}

// TestFpCache_InvalidatesWhenTheFileChanges is the safety property. Same
// path, same row, new audio — a cache that trusted size alone (or nothing)
// would keep reporting the previous track's duplicates.
func TestFpCache_InvalidatesWhenTheFileChanges(t *testing.T) {
	fpcalc := mustFpcalc(t)
	gormDB := indexDB(t)
	root := t.TempDir()

	p := filepath.Join(root, "a.wav")
	testaudio.SeedWAV(t, root, "a.wav", 30)
	indexRow(t, gormDB, p, "music")

	c := New(gormDB, root)
	c.fpcalcPath = fpcalc

	first, err := c.cachedFingerprint(context.Background(), p)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	// A hit must now be visible: the blob is stored.
	if _, ok := readFpCache(gormDB, p); !ok {
		t.Fatal("after a read, the fingerprint was not cached — the second read will decode again")
	}

	// Replace the file with different audio, as a re-encode in place would.
	// The path and the music_folder row are unchanged.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedWAV(t, root, "a.wav", 12)

	second, err := c.cachedFingerprint(context.Background(), p)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if second.Len() == first.Len() && second.Duration() == first.Duration() {
		t.Errorf("after replacing the file, the cached fingerprint was reused "+
			"(len %d, duration %d) — stale cache would report the old track's duplicates",
			second.Len(), second.Duration())
	}
	// And the stored key must have been refreshed, or the next read is a
	// miss forever rather than a hit.
	row := cacheRow(t, gormDB, p)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if row.FPSize != fi.Size() || row.FPMTime != fi.ModTime().UnixNano() {
		t.Errorf("cache key = (size %d, mtime %d), want (%d, %d) — the validity "+
			"key was not refreshed after the rewrite", row.FPSize, row.FPMTime,
			fi.Size(), fi.ModTime().UnixNano())
	}
}

// An untouched file must stay a hit, or the cache is pure overhead.
func TestFpCache_UntouchedFileStaysAHit(t *testing.T) {
	fpcalc := mustFpcalc(t)
	gormDB := indexDB(t)
	root := t.TempDir()

	p := filepath.Join(root, "a.wav")
	testaudio.SeedWAV(t, root, "a.wav", 30)
	indexRow(t, gormDB, p, "music")

	c := New(gormDB, root)
	c.fpcalcPath = fpcalc
	if _, err := c.cachedFingerprint(context.Background(), p); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if _, ok := readFpCache(gormDB, p); !ok {
		t.Fatal("first read did not populate the cache")
	}
	if _, ok := readFpCache(gormDB, p); !ok {
		t.Error("second read of an untouched file missed the cache")
	}
}

// A row with no cached fingerprint is a miss, not an error.
func TestFpCache_MissOnEmptyRow(t *testing.T) {
	gormDB := indexDB(t)
	root := t.TempDir()
	p := filepath.Join(root, "a.wav")
	testaudio.SeedWAV(t, root, "a.wav", 30)
	indexRow(t, gormDB, p, "music")

	if _, ok := readFpCache(gormDB, p); ok {
		t.Error("an unindexed row reported a cache hit")
	}
}

// A row outlives its file.
//
// Deleting a track out from under the app leaves its music_folder row
// behind — neither the scanner nor tidy prunes rows whose file has
// vanished. A cached fingerprint on such a row is a complete description
// of audio that no longer exists, and the size/mtime check cannot catch
// it: there is no file left to stat and compare against. Handing that
// blob back would make a deleted track a permanent duplicate candidate,
// pointing at a path the user cannot play or open.
func TestFpCache_MissesWhenTheFileIsGone(t *testing.T) {
	mustFpcalc(t)
	gormDB := indexDB(t)
	root := t.TempDir()
	p := filepath.Join(root, "doomed.wav")
	testaudio.SeedWAV(t, root, "doomed.wav", 30)
	indexRow(t, gormDB, p, "music")

	// Warm the cache, so the row really does carry a fingerprint.
	c := &Checker{db: gormDB, fpcalcPath: mustFpcalc(t)}
	if _, err := c.cachedFingerprint(context.Background(), p); err != nil {
		t.Fatalf("warm cache: %v", err)
	}
	if len(cacheRow(t, gormDB, p).Fingerprint) == 0 {
		t.Fatal("cache did not warm; the test would pass for the wrong reason")
	}

	// The file goes away, the row stays.
	if err := os.Remove(p); err != nil {
		t.Fatalf("remove file: %v", err)
	}

	if fp, ok := readFpCache(gormDB, p); ok {
		t.Errorf("a row for a deleted file reported a cache hit (duration %v)", fp.Duration())
	}
}
