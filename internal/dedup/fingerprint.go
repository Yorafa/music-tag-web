// Dedup's fingerprint-stage policy, on top of internal/fingerprint.
//
// Everything about reading fpcalc and measuring subfingerprint distance
// lives in that package now. What is left here is the part that is a
// *decision*: which similarity counts as a duplicate, and how a verdict is
// worded.

package dedup

import (
	"context"

	"go-music-tag/internal/fingerprint"
)

// similarityThreshold is the minimum bit-similarity for two fingerprints to
// count as the same track.
//
// Measured same-song re-encodings land at 0.995+ and an unrelated track at
// 0.511, so 0.90 has margin on both sides: raising it towards the 0.995
// cluster would start rejecting genuine lossy re-encodes, lowering it towards
// 0.511 would eventually admit unrelated audio. The numbers, and the seven
// encodings they came from, are in the internal/fingerprint package comment.
const similarityThreshold = 0.90

// sameTrack reports whether two fingerprints describe the same recording.
func sameTrack(a, b fingerprint.Fingerprint) bool {
	s, ok := fingerprint.Similarity(a, b)
	return ok && s >= similarityThreshold
}

// subFingerprint is a thin alias so the stage reads the same as it did before
// the shared package existed, and so there is exactly one name in this file
// for "the fingerprint of this file".
func subFingerprint(ctx context.Context, fpcalcPath, path string) (fingerprint.Fingerprint, error) {
	return fingerprint.Raw(ctx, fpcalcPath, path)
}
