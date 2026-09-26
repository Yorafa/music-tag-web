package dedup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/testaudio"
)

// The end-to-end payoff of the cache.
//
// A check decodes the file under test plus every candidate whose duration
// lands in the ±5s window, at ~0.4s each. Without a cache, running the same
// check twice over the same album pays that bill twice to reach the same
// verdict — and the Worklist's 「查重」 button is exactly a check the user
// may well run again after selecting more rows.

// TestRepeatedCheckIsServedFromTheCache asserts the second Check returns the
// same verdict while decoding nothing, by pointing the checker at an fpcalc
// that would fail loudly if it were invoked.
//
// A timing assertion would be the obvious choice and the wrong one: it is
// flaky on a loaded machine and it cannot distinguish "cached" from "the
// disk was fast". Removing the binary after the first check makes the
// second one either correct-by-cache or a hard error.
func TestRepeatedCheckIsServedFromTheCache(t *testing.T) {
	requireFpcalc(t)
	gdb := indexDB(t)
	root := t.TempDir()

	// Two tracks of the SAME length so the duration window admits them as
	// candidates for each other, but with DIFFERENT audio — two SeedWAV
	// calls are byte-identical (the generator is deterministic), so the
	// hash stage would decide the check before the fingerprint stage ever
	// ran, and this test would pass without exercising the cache at all.
	a := testaudio.SeedWAV(t, root, "a.wav", 30)
	b := testaudio.SeedDissonantWAV(t, root, "b.wav", 30)
	indexRow(t, gdb, a, "music")
	indexRow(t, gdb, b, "music")

	// Seed the index the way the duration task does, so the candidate
	// query has something to select.
	if err := gdb.Table("music_folder").
		Where("path IN ?", []string{a, b}).
		Update("duration", 30).Error; err != nil {
		t.Fatal(err)
	}

	c := New(gdb, root)
	first := c.Check(context.Background(), a, Options{})
	if !containsRun(first.Run, stageFingerprint) {
		t.Fatalf("first check did not run the fingerprint stage: %v", first.Run)
	}

	// Both files are now cached. Take the binary away: a second check that
	// still returns a fingerprint-stage verdict cannot have decoded
	// anything.
	c.fpcalcPath = filepath.Join(root, "fpcalc-that-does-not-exist")
	second := c.Check(context.Background(), a, Options{})

	if second.Verdict != first.Verdict {
		t.Errorf("verdict changed between runs: %v then %v — the cached run is not "+
			"reproducing the decoded one", first.Verdict, second.Verdict)
	}
	if second.MatchField != first.MatchField {
		t.Errorf("match field changed between runs: %q then %q",
			first.MatchField, second.MatchField)
	}
}

// A cache must never turn "cannot compare" into "not a duplicate". With the
// binary gone and nothing cached, the stage has to report no conclusion
// rather than a clean bill of health.
func TestCacheMissWithNoBinaryIsNotADuplicateVerdict(t *testing.T) {
	requireFpcalc(t)
	gdb := indexDB(t)
	root := t.TempDir()

	a := testaudio.SeedWAV(t, root, "a.wav", 30)
	unindexed := testaudio.SeedDissonantWAV(t, root, "unindexed.wav", 30)
	indexRow(t, gdb, a, "music")

	c := New(gdb, root)
	// New() leaves fpcalcPath empty for fpcalcAvailable() to resolve on
	// first use; cachedFingerprint is called directly, so set it here or
	// every read fails with ErrNoBinary — including the warm-up.
	c.fpcalcPath = requireFpcalc(t)
	// Cache it, then remove the binary.
	if _, err := c.cachedFingerprint(context.Background(), a); err != nil {
		t.Fatalf("warm the cache: %v", err)
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal(err)
	}
	// A file the index has never seen, with no binary to decode it.
	c.fpcalcPath = filepath.Join(root, "gone")

	fp, err := c.cachedFingerprint(context.Background(), unindexed)
	if err == nil {
		t.Error("a cache miss with no binary returned a fingerprint; it must report the failure")
	}
	if fp.Len() != 0 {
		t.Errorf("failed read returned %d subfingerprints, want none", fp.Len())
	}
}
