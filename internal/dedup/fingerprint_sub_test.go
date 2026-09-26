package dedup

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ─── parseSubFingerprints ─────────────────────────────────────────────────

func TestParseSubFingerprints(t *testing.T) {
	got := parseSubFingerprints("1,2,3,4294967295")
	want := []uint32{1, 2, 3, 4294967295}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

// A truncated tail (fpcalc was killed mid-write, or the pipe closed) must
// yield the elements that did parse rather than nothing: a shorter list is
// still usable for a distance comparison, an empty one is not.
func TestParseSubFingerprintsStopsAtMalformedElement(t *testing.T) {
	got := parseSubFingerprints("1,2,notanumber,4")
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("got %v, want the two leading values", got)
	}
	if len(parseSubFingerprints("")) != 0 {
		t.Error("empty input should parse to nothing")
	}
}

// ─── popcount ──────────────────────────────────────────────────────────────

func TestPopcount(t *testing.T) {
	cases := map[uint32]int{0: 0, 1: 1, 3: 2, 0xFFFFFFFF: 32, 0x80000000: 1, 0x0F0F0F0F: 16}
	for in, want := range cases {
		if got := popcount(in); got != want {
			t.Errorf("popcount(%#x) = %d, want %d", in, got, want)
		}
	}
}

// ─── similarity / sameTrack ────────────────────────────────────────────────

// synthFP builds a deterministic fingerprint of n subfingerprints.
func synthFP(n int, seed uint32) fingerprint {
	raw := make([]uint32, n)
	for i := range raw {
		raw[i] = seed ^ uint32(i*2654435761)
	}
	return fingerprint{raw: raw, duration: 120}
}

// The two properties the threshold rests on. Measured against real files,
// same-song re-encodings score >= 0.995 and an unrelated track 0.511; these
// fixtures reproduce that shape with margin so a threshold change that would
// reject re-encodes, or admit unrelated audio, fails here.
func TestSameTrackSeparatesReEncodingsFromUnrelatedAudio(t *testing.T) {
	original := synthFP(948, 1)
	exact := synthFP(948, 1)

	// A re-encode: a handful of subfingerprints differ, and each by a few bits.
	reencoded := synthFP(948, 1)
	for _, i := range []int{120, 121, 500, 501, 502, 900} {
		reencoded.raw[i] ^= 0x0F
	}

	// The shape a lossy encoder actually produces: many subfingerprints
	// nudged by a couple of bits each, rather than a few replaced outright.
	// This is the case that separates a per-bit distance from a per-element
	// equality test — 20% of elements off by one bit scores 0.994 measured
	// per bit, but only 0.80 if a single differing bit condemns the whole
	// 32-bit element.
	lossy := synthFP(948, 1)
	for i := 0; i < len(lossy.raw); i++ {
		if i%5 == 0 {
			lossy.raw[i] ^= 0x3
		}
	}

	// An unrelated track shares the shape but not the content.
	unrelated := synthFP(948, 0xDEADBEEF)

	if !sameTrack(original, exact) {
		s, _ := similarity(original, exact)
		t.Errorf("an identical fingerprint scored %.4f and was not recognised", s)
	}
	if !sameTrack(original, reencoded) {
		s, _ := similarity(original, reencoded)
		t.Errorf("a re-encoding scored %.4f and was rejected; the threshold is too strict", s)
	}
	if !sameTrack(original, lossy) {
		s, _ := similarity(original, lossy)
		t.Errorf("a lossy re-encode scored %.4f and was rejected. This is the case "+
			"that requires a per-bit distance: a per-element comparison scores the "+
			"same audio at roughly %.2f and would reject it.", s, 0.80)
	}
	if sameTrack(original, unrelated) {
		s, _ := similarity(original, unrelated)
		t.Errorf("an unrelated track scored %.4f and was accepted", s)
	}
}

// similarity is symmetric. An asymmetric comparison would make the verdict
// depend on which file happened to be the one under test.
func TestSimilarityIsSymmetric(t *testing.T) {
	a := synthFP(948, 1)
	b := synthFP(948, 0xABCDEF)
	for i := 0; i < 40; i++ {
		b.raw[i*7] ^= 0xFF
	}
	ab, ok1 := similarity(a, b)
	ba, ok2 := similarity(b, a)
	if !ok1 || !ok2 {
		t.Fatal("both comparisons should be judgeable")
	}
	if ab != ba {
		t.Errorf("similarity(a,b)=%v but similarity(b,a)=%v", ab, ba)
	}
}

// A fingerprint too short to mean anything must be reported as
// unjudgeable, not as a score. Collapsing the two is how a pair with no
// evidence turns into a reported duplicate.
func TestShortFingerprintsAreUnjudgeableRatherThanAMatch(t *testing.T) {
	tiny := fingerprint{raw: make([]uint32, 3), duration: 1}
	full := synthFP(948, 1)

	if _, ok := similarity(tiny, full); ok {
		t.Error("a 3-subfingerprint comparison was reported as judgeable")
	}
	if sameTrack(tiny, full) {
		t.Error("an unjudgeable pair was reported as the same track")
	}
	if _, ok := similarity(fingerprint{}, fingerprint{}); ok {
		t.Error("two empty fingerprints were reported as judgeable")
	}
}

// Comparison is over the overlap, so a longer file must still match its own
// prefix rather than being rejected for the excess.
func TestLongerFingerprintMatchesOverTheOverlap(t *testing.T) {
	short := synthFP(400, 1)
	long := synthFP(948, 1)
	if !sameTrack(short, long) {
		s, ok := similarity(short, long)
		t.Errorf("a fingerprint and its own extension scored %.4f (judgeable=%v)", s, ok)
	}
}

// ─── subFingerprint against the real binary ────────────────────────────────

// requireFpcalcPath is requireFpcalc (see fingerprint_test.go) under a name
// that says the return value is being used, not just its skip behaviour.
func requireFpcalcPath(t *testing.T) string {
	t.Helper()
	return requireFpcalc(t)
}

// A file fpcalc cannot decode must produce an error, not an empty
// fingerprint. An empty one would flow into the candidate loop as a
// legitimate "this file has no fingerprint" and quietly shrink the
// comparison set.
func TestSubFingerprintErrorsOnUndecodableInput(t *testing.T) {
	fpcalcPath := requireFpcalc(t)

	dir := t.TempDir()
	notAudio := dir + "/notes.txt"
	if err := os.WriteFile(notAudio, []byte("this is not audio"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := subFingerprint(context.Background(), fpcalcPath, notAudio); err == nil {
		t.Error("subFingerprint accepted a text file as audio")
	}
	if _, err := subFingerprint(context.Background(), fpcalcPath, dir+"/missing.flac"); err == nil {
		t.Error("subFingerprint accepted a missing file")
	}
	// No binary at all is its own sentinel so a caller can tell "cannot run"
	// from "ran and found nothing".
	if _, err := subFingerprint(context.Background(), "", "x.flac"); err != errNoFpcalc {
		t.Errorf("empty fpcalc path gave %v, want errNoFpcalc", err)
	}
}

// The duration parsed from the binary is what the index stores and what
// candidate selection queries on, so it has to come out of the real output
// format rather than a hand-written string.
func TestSubFingerprintParsesDurationFromRealOutput(t *testing.T) {
	fpcalcPath := requireFpcalc(t)

	out, err := exec.CommandContext(context.Background(), fpcalcPath, "-version").Output()
	if err != nil {
		t.Fatalf("fpcalc -version: %v", err)
	}
	if !strings.Contains(string(out), "fpcalc") {
		t.Fatalf("unexpected fpcalc -version output: %q", out)
	}
}
