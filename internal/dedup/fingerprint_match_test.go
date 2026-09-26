package dedup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/db"
	"go-music-tag/internal/testaudio"
)

// This file covers the claim the fingerprint stage was built on and never
// earned: that it finds the same track re-encoded under a different format,
// bitrate and container.
//
// It could not be tested before for two reasons that are now fixed. The
// matcher compared fpcalc's *compressed* fingerprint with string equality,
// which rejects re-encodings by construction — measured, the same track
// re-encoded scores 0.995+ on subfingerprint bit-similarity and an unrelated
// track 0.511, so no amount of tuning an equality test would have found it.
// And the stage picked candidates by byte size, so a re-encode that shrank
// the file by 25x was never even considered; it now picks them by duration.

// matchTestSeconds is the length of the audio fixtures below.
//
// It is not arbitrary: chromaprint emits roughly 7.4 subfingerprints per
// second, and the matcher refuses to judge a comparison over fewer than
// minSubfingerprints (100) of them, so anything under about 14 seconds cannot
// be compared at all. An 8-second fixture produced 43 subfingerprints and
// every "is this a duplicate" assertion silently became a test of the length
// guard instead — it would have passed no matter what the matcher did.
const matchTestSeconds = 30

// fingerprintDB builds a music_folder index seeded with the given files.
func fingerprintDB(t *testing.T, rows map[string]int64) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for path, dur := range rows {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		row := db.Folder{
			Path:     path,
			Name:     filepath.Base(path),
			Size:     fi.Size(),
			FileType: "music",
			Duration: dur,
		}
		if err := gdb.Table("music_folder").Create(&row).Error; err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	return gdb
}

// A WAV and a lossy re-encode of the same audio, as two library files.
//
// The candidate is found through the duration index, exactly as it is in
// production. ffmpeg is required because producing a genuinely different
// encoding in pure Go is not practical; the test skips without it rather than
// falling back to a byte-identical copy, which stage 2 already covers and
// which would prove nothing about this stage.
func TestFingerprintFindsReEncodedDuplicate(t *testing.T) {
	fpcalcPath := requireFpcalc(t)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; cannot produce a re-encoded fixture")
	}

	root := t.TempDir()
	original := testaudio.SeedWAV(t, root, "original.wav", matchTestSeconds)
	// A deliberately different container and bitrate, so the file differs in
	// size as well as in bytes.
	reencoded := filepath.Join(root, "reencoded.mp3")
	if out, err := exec.Command(ffmpeg, "-loglevel", "error", "-y",
		"-i", original, "-c:a", "libmp3lame", "-b:a", "64k", reencoded).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not re-encode the fixture: %v (%s)", err, out)
	}

	origDur := indexedDuration(t, fpcalcPath, original)
	candDur := indexedDuration(t, fpcalcPath, reencoded)
	if origDur == 0 || candDur == 0 {
		t.Fatalf("fixture durations are %d/%d; the test cannot proceed", origDur, candDur)
	}

	gdb := fingerprintDB(t, map[string]int64{reencoded: candDur})
	c := New(gdb, root)
	c.SetMusicRoot(root)

	got := c.Check(context.Background(), original, Options{MusicRoot: root})
	if got.Verdict != VerdictDuplicate {
		t.Fatalf("Verdict = %v (MatchField %q, Run %v); a re-encode of the same "+
			"audio should be reported as a duplicate", got.Verdict, got.MatchField, got.Run)
	}
	if got.MatchField != stageFingerprint {
		t.Errorf("MatchField = %q, want %q — the match came from the wrong stage", got.MatchField, stageFingerprint)
	}
	if got.DuplicatePath != relOrSelf(reencoded, root) {
		t.Errorf("DuplicatePath = %q, want %q", got.DuplicatePath, relOrSelf(reencoded, root))
	}
}

// The safety half, and the more important one. Check()'s caller treats
// VerdictDuplicate as "refuse the write", so a stage that over-matches
// silently eats the user's tagging work. Two different pieces of audio of
// the same length must come back clean.
func TestFingerprintDeclinesUnrelatedAudioOfTheSameLength(t *testing.T) {
	requireFpcalc(t)

	root := t.TempDir()
	// Same length on purpose: without that, a verdict of "unique" could just
	// be the duration filter never surfacing the candidate, which would
	// make this test pass for the wrong reason.
	first := testaudio.SeedWAV(t, root, "first.wav", matchTestSeconds)
	second := testaudio.SeedDissonantWAV(t, root, "second.wav", matchTestSeconds)

	gdb := fingerprintDB(t, map[string]int64{second: matchTestSeconds})
	c := New(gdb, root)
	c.SetMusicRoot(root)

	got := c.Check(context.Background(), first, Options{MusicRoot: root, DisableMeta: true})
	if got.Verdict == VerdictDuplicate {
		t.Errorf("Verdict = Duplicate (MatchField %q, path %q) for two different "+
			"pieces of audio of the same length", got.MatchField, got.DuplicatePath)
	}
}

// An unindexed candidate is invisible to a duration query. The stage has to
// fall back rather than quietly returning "no duplicates", because an empty
// answer and a skipped stage look identical to the caller.
func TestFingerprintStageRunsWithoutADurationIndex(t *testing.T) {
	requireFpcalc(t)

	root := t.TempDir()
	target := testaudio.SeedWAV(t, root, "solo.wav", matchTestSeconds)

	// No index at all.
	c := New(nil, root)
	c.SetMusicRoot(root)
	got := c.Check(context.Background(), target, Options{MusicRoot: root})
	if !containsRun(got.Run, stageFingerprint) {
		t.Errorf("Run = %v; the stage should still be attempted without an index", got.Run)
	}
	if got.Verdict == VerdictDuplicate {
		t.Errorf("Verdict = Duplicate with no library to compare against; a single "+
			"file must not be a duplicate of itself (MatchField %q)", got.MatchField)
	}
}

// A file must never be reported as its own duplicate, which is easy to get
// wrong once candidates come from a table that contains the file under test.
func TestFingerprintNeverMatchesAFileAgainstItself(t *testing.T) {
	requireFpcalc(t)

	root := t.TempDir()
	target := testaudio.SeedWAV(t, root, "self.wav", matchTestSeconds)

	// The index contains the file under test itself, at its own duration.
	gdb := fingerprintDB(t, map[string]int64{target: matchTestSeconds})
	c := New(gdb, root)
	c.SetMusicRoot(root)

	got := c.Check(context.Background(), target, Options{MusicRoot: root, DisableMeta: true})
	if got.Verdict == VerdictDuplicate {
		t.Errorf("Verdict = Duplicate against itself (path %q)", got.DuplicatePath)
	}
}

// Duration rows that the index has not reached yet must not be swept in.
//
// Every unindexed row sits at duration=0, so the window around a *short*
// file reaches zero and would otherwise match every row in the library —
// then fingerprint all of them, at ~0.4s each. The target here is
// deliberately far below the tolerance: querying for a 30-second file
// produces a window of 25..35, which excludes 0 anyway, so the test would
// pass whether or not the filter existed.
func TestDurationCandidatesExcludeUnindexedRows(t *testing.T) {
	requireFpcalc(t)

	root := t.TempDir()
	short := 2 // seconds; a window of -3..7, which contains 0
	target := testaudio.SeedWAV(t, root, "tiny.wav", matchTestSeconds)
	other := testaudio.SeedWAV(t, root, "other.wav", matchTestSeconds)

	// The other file is present in the index but at duration 0 — the state
	// the indexer leaves a row in until it reaches it.
	gdb := fingerprintDB(t, map[string]int64{other: 0})
	c := New(gdb, root)
	c.SetMusicRoot(root)

	if got := c.durationCandidates(short, target); len(got) != 0 {
		t.Errorf("durationCandidates(%ds) returned %v; rows at duration 0 are not "+
			"indexed yet and must not be offered as candidates", short, got)
	}
}

// Encoders do not preserve duration exactly: a track can gain or lose a
// fraction of a second, and containers pad. A ±0s window would make the
// stage miss genuine duplicates, which is the failure this whole change
// exists to fix, so the tolerance has to be real.
func TestDurationCandidatesTolerateSmallLengthDifferences(t *testing.T) {
	requireFpcalc(t)

	root := t.TempDir()
	target := testaudio.SeedWAV(t, root, "a.wav", matchTestSeconds)
	near := testaudio.SeedWAV(t, root, "b.wav", matchTestSeconds)

	// One second apart, from a 30-second track: within the 5s tolerance,
	// outside a zero-width window.
	gdb := fingerprintDB(t, map[string]int64{near: 29})
	c := New(gdb, root)
	c.SetMusicRoot(root)

	if got := c.durationCandidates(30, target); len(got) != 1 || got[0] != near {
		t.Errorf("durationCandidates(30) = %v, want [%s]; a 1s difference must "+
			"still be offered as a candidate", got, near)
	}
}

// indexedDuration reads a file's duration the way the indexer does, so the
// test seeds the column with the value production would have written.
func indexedDuration(t *testing.T, fpcalcPath, path string) int64 {
	t.Helper()
	fp, err := subFingerprint(context.Background(), fpcalcPath, path)
	if err != nil {
		t.Fatalf("fpcalc on %s: %v", filepath.Base(path), err)
	}
	return int64(fp.duration)
}
