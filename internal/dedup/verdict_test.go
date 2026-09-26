package dedup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/testaudio"
)

// TestFilenameClashIsNotStrongEvidence pins the rule that makes duplicate
// detection safe to leave enabled.
//
// Stage 1 fires on a filename match, and a filename is weak evidence:
// "track01.mp3", "01 - Song.mp3" and the same title under two albums all
// collide while being different recordings. runDedupCheck treats
// VerdictDuplicate as "refuse the write", so returning Duplicate here would
// silently reject a user's save. Same shape as the meta stage, which is
// likewise advisory.
func TestFilenameClashIsNotStrongEvidence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUSIC_DIR", dir)

	// A different recording that happens to share a name.
	a := testaudio.WriteMP3(t, filepath.Join(dir, "Album", "track01.mp3"))
	b := testaudio.WriteMP3(t, filepath.Join(dir, "Other", "track01.mp3"))
	if a == b {
		t.Fatal("expected two distinct files")
	}
	if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
		t.Fatal(err)
	}
	first := testaudio.WriteMP3(t, filepath.Join(dir, "Album", "track01.mp3"))
	if err := copyFile(first, b); err != nil {
		t.Fatal(err)
	}
	// Give the two files different content so the hash stage cannot fire.
	if err := appendDistinctByte(first, b); err != nil {
		t.Fatal(err)
	}

	c := New(nil, dir)
	res := c.Check(context.Background(), first, Options{})

	if res.Verdict == VerdictDuplicate {
		t.Errorf("filename clash reported as Duplicate; that would refuse the write "+
			"(match_field=%q dup=%q)", res.MatchField, res.DuplicatePath)
	}
	if res.Verdict != VerdictLikelyDuplicate {
		t.Errorf("verdict = %q, want %q (filename is weak evidence)", res.Verdict, VerdictLikelyDuplicate)
	}
	if res.MatchField != stageFilename {
		t.Errorf("match_field = %q, want %q", res.MatchField, stageFilename)
	}
	if res.DuplicatePath == "" {
		t.Error("expected the clashing path to be reported for the warning")
	}
}

// TestIdenticalContentIsStrongEvidence is the other half of the rule: when
// the audio really is byte-identical, the write must be refused. Otherwise
// "only warn" would have quietly become "never block".
func TestIdenticalContentIsStrongEvidence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUSIC_DIR", dir)

	if err := os.MkdirAll(filepath.Join(dir, "Album"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "Other"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := testaudio.WriteMP3(t, filepath.Join(dir, "Album", "one.mp3"))
	b := filepath.Join(dir, "Other", "two.mp3")
	if err := copyFile(a, b); err != nil {
		t.Fatal(err)
	}

	c := New(nil, dir)
	res := c.Check(context.Background(), a, Options{})

	if res.Verdict != VerdictDuplicate {
		t.Errorf("verdict = %q, want %q — byte-identical audio must block "+
			"(run=%v reason=%q)", res.Verdict, VerdictDuplicate, res.Run, res.Reason)
	}
	if res.MatchField != stageHash {
		t.Errorf("match_field = %q, want %q", res.MatchField, stageHash)
	}
}

// TestUniqueFileIsUnique is the negative control: without it, a checker that
// returned LikelyDuplicate unconditionally would pass the test above.
func TestUniqueFileIsUnique(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUSIC_DIR", dir)

	a := testaudio.WriteMP3(t, filepath.Join(dir, "Album", "solo.mp3"))
	other := testaudio.WriteMP3(t, filepath.Join(dir, "Album", "other.mp3"))
	if err := appendDistinctByte(a, other); err != nil {
		t.Fatal(err)
	}

	c := New(nil, dir)
	if res := c.Check(context.Background(), a, Options{}); res.Verdict != VerdictUnique {
		t.Errorf("verdict = %q, want %q (run=%v reason=%q)",
			res.Verdict, VerdictUnique, res.Run, res.Reason)
	}
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// appendDistinctByte makes dst differ from src in its trailing bytes. It
// rewrites the file rather than appending, because the seeded MP3 carries
// size in its header and a raw append would leave two same-length files
// whose content differs — which is exactly the shape the hash stage needs to
// reject, but which the size stage would otherwise make ambiguous.
func appendDistinctByte(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return os.ErrInvalid
	}
	b[len(b)-1] ^= 0xFF
	return os.WriteFile(dst, b, 0o644)
}
