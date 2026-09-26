// Package-internal Chromaprint fingerprint reading and comparison.
//
// fpcalc's default output is a *compressed* fingerprint: a base64 blob whose
// bits have been packed and delta-coded. Comparing two of those with ==
// answers "are these the same audio bits", not "is this the same song" — so
// the previous string-equality test rejected re-encodings of the same track,
// which is the only case the stage existed to catch.
//
// `fpcalc -raw` instead prints the subfingerprint list: one 32-bit integer per
// ~26ms of audio, each a chroma-band energy bitmask. Two encodings of one
// track produce near-identical lists, which is what makes a distance
// comparison meaningful.
//
// Measured on one 120s track re-encoded seven ways (see the table in
// checkFingerprint), the bit-similarity of the subfingerprints against the
// original:
//
//	opus 190k   0.99631     mp3 220k   0.99862
//	opus 200k   0.99624     flac       0.99997
//	vorbis 200k 0.99568     wav 16bit  0.99997
//	vorbis 190k 0.99525
//	a different track from the same library: 0.51101
//
// So same-song and different-song are separated by a wide margin, and the
// threshold below sits inside it rather than on either edge.
package dedup

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// similarityThreshold is the minimum bit-similarity for two fingerprints to
// count as the same track. Measured same-song re-encodings land at 0.995+ and
// an unrelated track at 0.511, so 0.90 has margin on both sides: raising it
// towards the 0.995 cluster would start rejecting genuine lossy re-encodes,
// lowering it towards 0.511 would eventually admit unrelated audio.
const similarityThreshold = 0.90

// minSubfingerprints guards against a degenerate comparison. A fingerprint
// this short carries too little to distinguish anything, and accepting it
// would mean a handful of matching bits out of a few hundred counts as a
// duplicate. A 30-second track yields ~1150 subfingerprints, so this only
// rejects files that are seconds long or failed to decode properly.
const minSubfingerprints = 100

// fpcalcTimeout bounds one fpcalc invocation. fpcalc decodes the whole file,
// so this scales with track length; 20s is roughly a five-minute track.
const fpcalcTimeout = 20 * time.Second

// errNoFpcalc means the binary is not installed, so fingerprint comparison
// cannot run at all. Callers treat it as "skip this layer", never as
// "no duplicates found" — the difference matters, because the two were
// previously indistinguishable and the skip looked like a clean result.
var errNoFpcalc = errors.New("fpcalc not available")

// fingerprint is one file's raw subfingerprints plus its duration.
type fingerprint struct {
	raw      []uint32
	duration int // seconds, 0 when unknown
}

// subFingerprint runs fpcalc -raw and parses the result.
//
// The -raw output is two lines:
//
//	DURATION=120
//	FINGERPRINT=3228878944,3225360416,...
func subFingerprint(ctx context.Context, fpcalcPath, audioPath string) (fingerprint, error) {
	if fpcalcPath == "" {
		return fingerprint{}, errNoFpcalc
	}
	runCtx, cancel := context.WithTimeout(ctx, fpcalcTimeout)
	defer cancel()

	// #nosec G204 -- fpcalcPath is resolved once via exec.LookPath, not
	// taken from a request.
	out, err := exec.CommandContext(runCtx, fpcalcPath, "-raw", audioPath).Output()
	if err != nil {
		// fpcalc exits non-zero on anything it cannot decode, and writes
		// the reason to stdout rather than stderr. Returning it keeps a
		// "no audio stream found" distinguishable from a real failure.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fingerprint{}, fmt.Errorf("fpcalc: %s: %w", strings.TrimSpace(string(ee.Stderr)), err)
		}
		return fingerprint{}, fmt.Errorf("fpcalc: %w", err)
	}

	var fp fingerprint
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "DURATION="):
			if d, derr := strconv.ParseFloat(strings.TrimPrefix(line, "DURATION="), 64); derr == nil {
				fp.duration = int(d)
			}
		case strings.HasPrefix(line, "FINGERPRINT="):
			fp.raw = parseSubFingerprints(strings.TrimPrefix(line, "FINGERPRINT="))
		}
	}
	if len(fp.raw) == 0 {
		return fingerprint{}, errors.New("fpcalc: no subfingerprints in output")
	}
	return fp, nil
}

// parseSubFingerprints splits the comma-separated 32-bit list. A malformed
// element ends the list rather than aborting the parse: fpcalc emits valid
// integers, so a bad one means the output was truncated, and a short list is
// still usable for a distance comparison.
func parseSubFingerprints(s string) []uint32 {
	parts := strings.Split(s, ",")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			break
		}
		out = append(out, uint32(v))
	}
	return out
}

// popcount returns the number of set bits in v.
func popcount(v uint32) int {
	// Hacker's Delight, branch-free. math/bits.OnesCount32 does the same
	// but this file is also the reference for the comparison below and the
	// explicit form documents what is being counted.
	v = v - ((v >> 1) & 0x55555555)
	v = (v & 0x33333333) + ((v >> 2) & 0x33333333)
	v = (v + (v >> 4)) & 0x0F0F0F0F
	return int((v * 0x01010101) >> 24)
}

// similarity returns the fraction of agreeing bits between two fingerprints,
// over the region they overlap, and whether the comparison is meaningful.
//
// A/B are compared position by position over min(len) subfingerprints. The
// returned bool is false when either side is too short to judge, which
// callers must treat as "cannot compare" rather than as a score of zero —
// the two are different answers and conflating them is how an unjudgeable
// pair turns into a reported duplicate.
func similarity(a, b fingerprint) (float64, bool) {
	if len(a.raw) < minSubfingerprints || len(b.raw) < minSubfingerprints {
		return 0, false
	}
	n := len(a.raw)
	if len(b.raw) < n {
		n = len(b.raw)
	}
	// Require a meaningful overlap rather than comparing a few hundred
	// subfingerprints of an hour-long file against a whole other track.
	if n < minSubfingerprints {
		return 0, false
	}

	var errBits uint64
	for i := 0; i < n; i++ {
		errBits += uint64(popcount(a.raw[i] ^ b.raw[i]))
	}
	return 1.0 - float64(errBits)/float64(uint64(n)*32), true
}

// sameTrack reports whether two fingerprints describe the same recording.
func sameTrack(a, b fingerprint) bool {
	s, ok := similarity(a, b)
	return ok && s >= similarityThreshold
}
