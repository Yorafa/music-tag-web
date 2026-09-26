package dedup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-music-tag/internal/testaudio"
)

// Tests that measure the thing the cache promises, rather than inferring it.
//
// Asserting "the second check returned the same verdict" does not distinguish
// a cached run from a re-decode: both answer "unique" for a pair that is not
// a duplicate, so the assertion passes either way. These count actual fpcalc
// invocations, which is the only claim worth making about a cache.

// countingFpcalc installs a wrapper named `fpcalc` at the front of PATH and
// counts how many times it runs.
//
// It has to go on PATH rather than be assigned to Checker.fpcalcPath:
// fpcalcAvailable() resolves the binary with fingerprint.LookPath() inside a
// sync.Once on first use, so it overwrites whatever the field was set to.
//
// The counter is appended to rather than truncated, and the script uses
// single quotes so the shell cannot eat the body — an earlier fake-binary
// fixture in this package lost its quoting and every case degraded into
// "not JSON", passing for the wrong reason.
func countingFpcalc(t *testing.T, dir string) (count func() int) {
	t.Helper()
	real := mustFpcalc(t)
	counter := filepath.Join(dir, "count")
	script := "#!/bin/sh\necho x >> '" + counter + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "fpcalc"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, err := os.ReadFile(counter)
		if err != nil {
			return 0
		}
		return len(strings.Fields(string(b)))
	}
}

// TestSecondCheckDecodesNothing is the headline efficiency claim: after one
// check, an identical check must not invoke fpcalc at all.
func TestSecondCheckDecodesNothing(t *testing.T) {
	gdb := indexDB(t)
	root := t.TempDir()
	calls := countingFpcalc(t, t.TempDir())

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
	first := calls()
	if first == 0 {
		t.Fatal("the first check invoked fpcalc zero times; the fixture is not exercising the stage")
	}

	// Same check again. The files are untouched, so every fingerprint it
	// needs is already stored.
	second := c.Check(context.Background(), a, Options{})
	if got := calls(); got != first {
		t.Errorf("second check invoked fpcalc %d more times (%d total, was %d) — a "+
			"repeat check over unchanged files should decode nothing", got-first, got, first)
	}
	if second.Verdict != "unique" {
		t.Errorf("second verdict = %q, want unique", second.Verdict)
	}
}

// Touching a file must cost exactly one decode, not zero and not two: the
// cache has to notice the change and re-key.
func TestTouchedFileIsDecodedExactlyOnce(t *testing.T) {
	gdb := indexDB(t)
	root := t.TempDir()
	calls := countingFpcalc(t, t.TempDir())

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
	c.Check(context.Background(), a, Options{})
	before := calls()

	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(a, later, later); err != nil {
		t.Skipf("chtimes unavailable: %v", err)
	}

	c.Check(context.Background(), a, Options{})
	if grew := calls() - before; grew != 1 {
		t.Errorf("after touching one file, fpcalc ran %d times, want exactly 1 — "+
			"the other file's cached fingerprint must still be used", grew)
	}
}

// Second resolution is not enough. A re-encode that completes in the same
// second as the cached read is ordinary on a fast disk, and a seconds-granular
// mtime would call that a hit — serving the previous file's fingerprint.
func TestSubSecondMtimeChangeInvalidates(t *testing.T) {
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

	// Land inside the same wall-clock second, offset by half a second.
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	base := fi.ModTime().Truncate(time.Second)
	half := base.Add(500 * time.Millisecond)
	if err := os.Chtimes(p, half, half); err != nil {
		t.Skipf("chtimes unavailable: %v", err)
	}
	if got, _ := os.Stat(p); got.ModTime().Unix() != base.Unix() {
		t.Skip("filesystem truncated the sub-second mtime; nothing to test here")
	}

	if _, ok := readFpCache(gdb, p); ok {
		t.Error("a change within the same second is still a cache hit — the validity " +
			"key needs nanosecond resolution, or a fast re-encode keeps the old fingerprint")
	}
}
