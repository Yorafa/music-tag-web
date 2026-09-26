package fingerprint

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-music-tag/internal/testaudio"
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

// ─── Similarity ────────────────────────────────────────────────────────────

// synthFP builds a deterministic fingerprint of n subfingerprints. The
// fields are unexported, which is why these tests live here rather than
// beside the dedup stage that consumes them.
func synthFP(n int, seed uint32) Fingerprint {
	raw := make([]uint32, n)
	for i := range raw {
		raw[i] = seed ^ uint32(i*2654435761)
	}
	return Fingerprint{raw: raw, duration: 120}
}

func TestSimilarityIdenticalIsOne(t *testing.T) {
	a := synthFP(948, 1)
	b := synthFP(948, 1)
	s, ok := Similarity(a, b)
	if !ok {
		t.Fatal("two full-length fingerprints should be judgeable")
	}
	if s != 1.0 {
		t.Errorf("similarity of identical fingerprints = %v, want 1.0", s)
	}
}

// The measured shape of a lossy re-encode: many subfingerprints nudged by a
// couple of bits each. This is the case that separates a per-bit distance
// from a per-element comparison — a fifth of the elements off by two bits
// scores about 0.994 measured per bit, but only 0.80 if one differing bit
// condemns the whole 32-bit element.
func TestSimilarityCountsPartialElementAgreement(t *testing.T) {
	a := synthFP(948, 1)
	b := synthFP(948, 1)
	for i := 0; i < len(b.raw); i++ {
		if i%5 == 0 {
			b.raw[i] ^= 0x3
		}
	}
	s, ok := Similarity(a, b)
	if !ok {
		t.Fatal("should be judgeable")
	}
	// ~190 of 948 elements perturbed by 2 bits: 380 error bits out of 30336.
	if s < 0.98 || s > 0.999 {
		t.Errorf("similarity = %v, want ~0.987", s)
	}
}

// Similarity is symmetric. An asymmetric comparison would make the verdict
// depend on which file happened to be the one under test.
func TestSimilarityIsSymmetric(t *testing.T) {
	a := synthFP(948, 1)
	b := synthFP(948, 0xABCDEF)
	for i := 0; i < 40; i++ {
		b.raw[i*7] ^= 0xFF
	}
	ab, ok1 := Similarity(a, b)
	ba, ok2 := Similarity(b, a)
	if !ok1 || !ok2 {
		t.Fatal("both comparisons should be judgeable")
	}
	if ab != ba {
		t.Errorf("Similarity(a,b)=%v but Similarity(b,a)=%v", ab, ba)
	}
}

// A fingerprint too short to mean anything must be reported as
// unjudgeable, not as a score. Collapsing the two is how a pair with no
// evidence turns into a reported duplicate.
func TestShortFingerprintsAreUnjudgeable(t *testing.T) {
	tiny := Fingerprint{raw: make([]uint32, 3), duration: 1}
	full := synthFP(948, 1)

	if _, ok := Similarity(tiny, full); ok {
		t.Error("a 3-subfingerprint comparison was reported as judgeable")
	}
	if _, ok := Similarity(Fingerprint{}, Fingerprint{}); ok {
		t.Error("two empty fingerprints were reported as judgeable")
	}
}

// Comparison is over the overlap, so a longer file must still match its own
// prefix rather than being rejected for the excess.
func TestLongerFingerprintMatchesOverTheOverlap(t *testing.T) {
	short := synthFP(400, 1)
	long := synthFP(948, 1)
	s, ok := Similarity(short, long)
	if !ok || s != 1.0 {
		t.Errorf("a fingerprint and its own extension scored %v (judgeable=%v)", s, ok)
	}
}

// ─── against the real binary ───────────────────────────────────────────────

func requireFpcalc(t *testing.T) string {
	t.Helper()
	p, err := LookPath()
	if err != nil {
		t.Skip("fpcalc not installed; skipping the live-binary test")
	}
	return p
}

// A missing binary is its own sentinel at every entry point, so a caller can
// tell "cannot run" from "ran and found nothing" — the two look identical
// in a response carrying only a result list.
func TestMissingBinaryIsItsOwnError(t *testing.T) {
	if _, err := LookPath(); err == nil {
		t.Skip("fpcalc is installed here, so LookPath cannot fail")
	}
	for name, call := range map[string]func() error{
		"Raw":      func() error { _, err := Raw(context.Background(), "", "x.flac"); return err },
		"Compress": func() error { _, err := Compress(context.Background(), "", "x.flac"); return err },
		"Duration": func() error { _, err := DurationOf(context.Background(), "", "x.flac"); return err },
	} {
		if err := call(); err != ErrNoBinary {
			t.Errorf("%s with an empty path gave %v, want ErrNoBinary", name, err)
		}
	}
}

// A file fpcalc cannot decode must produce an error from every entry point.
// An empty fingerprint would flow onward as a legitimate "this file has no
// fingerprint" and quietly shrink the candidate set; a zero duration would
// make an unindexed row look indexed.
func TestUndecodableInputIsAnErrorEverywhere(t *testing.T) {
	fpcalcPath := requireFpcalc(t)
	dir := t.TempDir()

	notAudio := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notAudio, []byte("this is not audio"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	missing := filepath.Join(dir, "gone.flac")

	for _, p := range []string{notAudio, missing} {
		if _, err := Raw(context.Background(), fpcalcPath, p); err == nil {
			t.Errorf("Raw accepted %s", filepath.Base(p))
		}
		if _, err := Compress(context.Background(), fpcalcPath, p); err == nil {
			t.Errorf("Compress accepted %s", filepath.Base(p))
		}
		if _, err := DurationOf(context.Background(), fpcalcPath, p); err == nil {
			t.Errorf("DurationOf accepted %s", filepath.Base(p))
		}
	}
}

// DurationOf has to agree with Raw, or the index would store a length the
// dedup candidate query never matches against.
func TestDurationOfAgreesWithRaw(t *testing.T) {
	fpcalcPath := requireFpcalc(t)
	dir := t.TempDir()

	for _, secs := range []float64{3, 11} {
		p := testaudio.SeedWAV(t, dir, wavName(secs), secs)
		full, err := Raw(context.Background(), fpcalcPath, p)
		if err != nil {
			t.Fatalf("Raw(%s): %v", p, err)
		}
		only, err := DurationOf(context.Background(), fpcalcPath, p)
		if err != nil {
			t.Fatalf("DurationOf(%s): %v", p, err)
		}
		if only != full.Duration() {
			t.Errorf("%s: DurationOf=%d but Raw=%d", filepath.Base(p), only, full.Duration())
		}
	}
}

// Compress produces the packed form the AcoustID API takes, and it must be a
// base64 blob rather than the comma-separated integer list — the two are
// silently incompatible and confusing them yields a fingerprint the service
// rejects with no useful error.
func TestCompressReturnsBase64NotSubFingerprints(t *testing.T) {
	fpcalcPath := requireFpcalc(t)
	dir := t.TempDir()
	p := testaudio.SeedWAV(t, dir, "song.wav", 11)

	got, err := Compress(context.Background(), fpcalcPath, p)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if got.Data == "" {
		t.Fatal("Data is empty")
	}
	if strings.ContainsRune(got.Data, ',') {
		t.Error("Data looks like a raw subfingerprint list, not the compressed form")
	}
	if got.Duration <= 0 {
		t.Errorf("Duration = %d, want a positive value", got.Duration)
	}
}

// A run that exits 0 but hands back nothing usable must still be an error.
// Neither a text-shaped nor an empty-fingerprint -json response can be sent
// to the AcoustID API: the first does not parse, and the second would be
// accepted and return a meaningless "no match" for every request.
//
// The real binary does not produce either, which is exactly why this needs a
// stand-in rather than a fixture — and why the check has to stay: it guards
// the contract against a future swap of the binary, not against fpcalc 1.5.
func TestEmptyOrUnparseableOutputIsAnError(t *testing.T) {
	dir := t.TempDir()

	cases := map[string]string{
		"empty fingerprint": `{"duration":10,"fingerprint":""}`,
		"not json at all":   `this is not json`,
		"raw text shape":    "DURATION=10\nFINGERPRINT=AQAA\n",
	}
	for name, body := range cases {
		// The body is written to its own file and cat'd, so it reaches
		// fpcalc byte for byte. Inlining it into the script does not work:
		// sh eats the JSON's double quotes, and a body containing a newline
		// would not survive either — and then every case silently degrades
		// into "not json at all", so the test passes for the wrong reason on
		// the two cases meant to probe something else.
		bodyFile := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".body")
		if err := os.WriteFile(bodyFile, []byte(body), 0o644); err != nil {
			t.Fatalf("write body %s: %v", name, err)
		}
		script := "#!/bin/sh\ncat '" + bodyFile + "'\n"
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".sh")
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if out, err := exec.Command(p).Output(); err != nil || string(out) != body {
			t.Fatalf("fixture %s does not emit its body: out=%q want=%q err=%v", name, out, body, err)
		}
		if _, err := Compress(context.Background(), p, "x.flac"); err == nil {
			t.Errorf("Compress accepted %s output", name)
		}
	}
}

func wavName(secs float64) string {
	return fmt.Sprintf("track-%gs.wav", secs)
}
