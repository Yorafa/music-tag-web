package dedup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-music-tag/internal/fingerprint"
	"go-music-tag/internal/testaudio"
)

// Tests added because mutation testing showed the obvious assertions were not
// distinguishing the behaviour they claimed to pin.

// TestDurationCandidates_ExcludesTheDownloadCache covers the containment check
// on the *duration* query specifically.
//
// The cache-exclusion test elsewhere goes through libraryFiles, whose rows
// have duration 0, so `duration > 0` filters them out before containment
// ever runs. A cached download that HAD been indexed would slip past, and it
// is byte-identical to the library track — the exact false positive the check
// exists to prevent.
func TestDurationCandidates_ExcludesTheDownloadCache(t *testing.T) {
	gdb := indexDB(t)
	root := emptyRoot(t)

	cacheHit := filepath.Join(t.TempDir(), "audio_cache", "youtube", "vid.wav")
	if err := os.MkdirAll(filepath.Dir(cacheHit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheHit, []byte("cached audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Indexed AND duration-stamped, so only root containment can exclude it.
	indexRow(t, gdb, cacheHit, "music")
	if err := gdb.Table("music_folder").Where("path = ?", cacheHit).
		Update("duration", 120).Error; err != nil {
		t.Fatal(err)
	}

	c := New(gdb, root)
	got := c.durationCandidates(120, "")
	for _, p := range got {
		if p == cacheHit {
			t.Errorf("durationCandidates returned the download cache %q — a cached "+
				"copy of the track is not a library duplicate", cacheHit)
		}
	}
}

// The same query must still return a genuine in-library candidate, or the
// containment check is simply excluding everything.
func TestDurationCandidates_StillFindsLibraryTracks(t *testing.T) {
	gdb := indexDB(t)
	root := emptyRoot(t)

	inside := filepath.Join(root, "song.wav")
	if err := os.WriteFile(inside, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	indexRow(t, gdb, inside, "youtube") // pre-fix file_type, still in the library
	if err := gdb.Table("music_folder").Where("path = ?", inside).
		Update("duration", 120).Error; err != nil {
		t.Fatal(err)
	}

	c := New(gdb, root)
	got := c.durationCandidates(120, "")
	found := false
	for _, p := range got {
		if p == inside {
			found = true
		}
	}
	if !found {
		t.Errorf("durationCandidates = %v, want it to include %q", got, inside)
	}
}

// A same-size rewrite must invalidate. A tag editor or a same-length
// re-encode can preserve the byte count, so a size-only check would keep
// serving a fingerprint for audio that is no longer there.
func TestFpCache_InvalidatesOnSameSizeRewrite(t *testing.T) {
	fpcalc := mustFpcalc(t)
	gdb := indexDB(t)
	root := t.TempDir()

	p := filepath.Join(root, "a.wav")
	testaudio.SeedWAV(t, root, "a.wav", 30)
	indexRow(t, gdb, p, "music")

	c := New(gdb, root)
	c.fpcalcPath = fpcalc
	if _, err := c.cachedFingerprint(context.Background(), p); err != nil {
		t.Fatalf("warm: %v", err)
	}
	before := cacheRow(t, gdb, p)

	// Same byte count, newer mtime. os.Chtimes is the only lever here: a
	// same-length WAV with different audio would need a different fixture
	// generator, and the point of the test is the mtime arm.
	later := time.Now().Add(3 * time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Skipf("chtimes unavailable: %v", err)
	}
	if _, ok := readFpCache(gdb, p); ok {
		t.Error("a file whose mtime moved is still served from the cache — the " +
			"fingerprint no longer describes what is on disk")
	}

	// The next read must re-decode AND re-key, or every subsequent read is
	// a miss forever rather than a hit.
	if _, err := c.cachedFingerprint(context.Background(), p); err != nil {
		t.Fatalf("re-read after touch: %v", err)
	}
	after := cacheRow(t, gdb, p)
	if after.FPMTime == before.FPMTime {
		t.Error("cache key mtime was not refreshed after the re-read — every later " +
			"read would miss forever instead of hitting")
	}
	if _, ok := readFpCache(gdb, p); !ok {
		t.Error("the re-read did not restore a usable cache entry")
	}
}

// A blob with a valid header and a valid leading subfingerprint but a
// truncated tail must be refused. The "no subfingerprints at all" case is
// already covered; this is the one that would decode into a short-but-
// non-empty fingerprint.
func TestDecode_RejectsTruncatedTailWithValidWords(t *testing.T) {
	blob := make([]byte, 0, 11)
	blob = append(blob, 0, 0, 0, 120) // duration header
	blob = append(blob, 1, 2, 3, 4)   // one whole subfingerprint
	blob = append(blob, 5, 6, 7)      // ...and a partial one

	if _, err := fingerprint.Decode(blob); err == nil {
		t.Error("Decode accepted a blob with a partial trailing subfingerprint; a " +
			"short prefix compares as similar, which is what minSubfingerprints exists to stop")
	}
}

// Candidates must go through the cache too, not just the file under test.
// Fingerprinting every candidate on every check is the cost this whole
// change exists to remove.
func TestFpCache_CandidatesAreCachedToo(t *testing.T) {
	requireFpcalc(t)
	gdb := indexDB(t)
	root := t.TempDir()

	a := testaudio.SeedWAV(t, root, "a.wav", 30)
	b := testaudio.SeedDissonantWAV(t, root, "b.wav", 30)
	for _, p := range []string{a, b} {
		indexRow(t, gdb, p, "music")
		if err := gdb.Table("music_folder").Where("path = ?", p).
			Update("duration", 30).Error; err != nil {
			t.Fatal(err)
		}
	}

	c := New(gdb, root)
	if got := c.Check(context.Background(), a, Options{}); !containsRun(got.Run, stageFingerprint) {
		t.Fatalf("fingerprint stage did not run: %v", got.Run)
	}

	// b was a candidate, so it should now be cached too. Asserting on the
	// row rather than on a count, so a stage that quietly skipped it fails.
	if _, ok := readFpCache(gdb, b); !ok {
		t.Error("the candidate's fingerprint was not cached — every later check " +
			"re-decodes each candidate in the duration window")
	}
}
