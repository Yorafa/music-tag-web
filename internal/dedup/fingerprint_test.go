package dedup

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"go-music-tag/internal/testaudio"
)

// requireFpcalc skips unless fpcalc is on PATH, and returns its path.
//
// The fingerprint stage is optional by design — Checker.fpcalcAvailable()
// probes once with exec.LookPath and disables the stage for the process if the
// binary is absent. That is correct at runtime, but it also means a suite can
// be green on a machine that never exercises the stage at all, which is
// exactly how this stage stayed dead for so long: it shipped disabled in
// every image and nothing failed. So the test skips loudly rather than
// silently, and Dockerfile.gateway is expected to carry fpcalc (it installs
// the chromaprint package) — a build that drops it loses this coverage.
func requireFpcalc(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("fpcalc")
	if err != nil {
		t.Skip("fpcalc not installed; the fingerprint stage is disabled on this host")
	}
	return p
}

// TestFingerprintStageIsEnabled is the regression test for the stage having
// been switched off in every image.
//
// It asserts the stage is *executed*, which is the claim that was false: with
// no fpcalc in the image, fpcalcAvailable() returned false, Check() skipped
// the stage entirely, and Result.Run never mentioned it — so a caller could
// not even tell the stage had been passed over.
//
// Note what this deliberately does NOT assert: that the stage produces a
// Duplicate verdict. That needs two files whose audio decodes to identical
// PCM while their bytes differ, and the repo carries no such fixture —
// testaudio's MP3 is a few KB of synthetic frames that fpcalc refuses to
// decode at all (exit status 2). Building one would mean committing real
// audio or shelling out to an encoder, and a matching assertion resting on
// either is not worth the dependency. See the match-rule note on
// checkFingerprint for why a match is a narrow case anyway.
func TestFingerprintStageIsEnabled(t *testing.T) {
	_ = requireFpcalc(t)

	root := t.TempDir()
	target := testaudio.SeedMP3(t, root, "song.mp3")

	c := New(nil, root)
	if !c.fpcalcAvailable() {
		t.Fatal("fpcalc is on PATH but fpcalcAvailable() is false; the stage is still disabled")
	}

	got := c.Check(context.Background(), target, Options{})
	if !containsRun(got.Run, stageFingerprint) {
		t.Errorf("Run = %v, does not mention %q: the stage was passed over rather than run",
			got.Run, stageFingerprint)
	}
}

// TestFingerprintStageDoesNotBlockOnUndecodableAudio is the safety half.
//
// An enabled stage that answers Duplicate for everything would be far worse
// than a disabled one: Check()'s caller treats VerdictDuplicate as "refuse
// the write", so a stage that over-matches silently eats the user's tagging
// work. fpcalc cannot decode the synthetic fixture, so the stage must
// decline and leave the file writable.
func TestFingerprintStageDoesNotBlockOnUndecodableAudio(t *testing.T) {
	_ = requireFpcalc(t)

	root := t.TempDir()
	target := testaudio.SeedMP3(t, root, "song.mp3")

	got := New(nil, root).Check(context.Background(), target, Options{})
	if got.Verdict == VerdictDuplicate {
		t.Errorf("Verdict = Duplicate (MatchField %q) on audio fpcalc cannot decode; "+
			"the stage must decline rather than block the write", got.MatchField)
	}
}

func containsRun(run []string, stage string) bool {
	for _, r := range run {
		if strings.TrimSpace(r) == stage {
			return true
		}
	}
	return false
}
