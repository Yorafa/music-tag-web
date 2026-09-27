package handler

import (
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/testaudio"
)

// seedTrack writes a decodable audio file plus the .lrc beside it, which is
// the pairing the app's own writer produces.
func seedTrack(t *testing.T, dir, name, lyrics string) {
	t.Helper()
	testaudio.SeedMP3(t, dir, name)
	if err := os.WriteFile(filepath.Join(dir, name[:len(name)-len(filepath.Ext(name))]+".lrc"),
		[]byte(lyrics), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Deleting a track has to take its lyrics with it.
//
// The UI only ever selects audio rows, so before this the .lrc beside a
// deleted track stayed in the library. That is not a cosmetic leftover: if
// the deleted track was the directory's last one, the directory then held a
// single file and the pruner — whose rule was "no file may remain" — could
// never remove it. The application created the stranded file and then could
// not clean it up.
//
// Carrying it also fixes the restore story. Both files land in the SAME
// batch, so one click brings back the track together with its lyrics instead
// of returning an audio file whose lyrics the app appears to have lost.

func TestDeleteFiles_CarriesTheLyricsIntoTheSameBatch(t *testing.T) {
	music, data := t.TempDir(), t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	seedTrack(t, leaf, "gone.mp3", "[00:01.00]words")

	rep := delRoute(t, []string{"A/gone.mp3"})

	if rep.Deleted != 1 || rep.Failed != 0 {
		t.Fatalf("deleted=%d failed=%d, want 1/0 (%+v)", rep.Deleted, rep.Failed, rep.Results)
	}
	if len(rep.Lyrics) != 1 || rep.Lyrics[0] != "A/gone.lrc" {
		t.Fatalf("lyrics = %v, want [A/gone.lrc] — the user has no way to know a second file went", rep.Lyrics)
	}
	if _, err := os.Stat(filepath.Join(leaf, "gone.lrc")); !os.IsNotExist(err) {
		t.Error("the lyrics stayed in the library; the directory will not prune")
	}
	// Same batch, same relative layout — that is what makes one restore
	// bring back both files.
	batch := filepath.Dir(filepath.Dir(rep.Results[0].TrashPath))
	if got, want := filepath.Join(batch, "A", "gone.lrc"), rep.Results[0].TrashPath; filepath.Dir(got) != filepath.Dir(want) {
		t.Errorf("lyrics landed in a different batch: %q vs the track's %q", got, want)
	}
	body, err := os.ReadFile(filepath.Join(batch, "A", "gone.lrc"))
	if err != nil {
		t.Fatalf("the lyrics did not reach the trash: %v", err)
	}
	if string(body) != "[00:01.00]words" {
		t.Errorf("trashed lyrics = %q; the copy must be byte-exact or a restore is data loss", body)
	}
	// And the directory is now prunable, which was the original problem.
	entries, err := os.ReadDir(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the directory still holds %d entries; the pruner will refuse it", len(entries))
	}
}

// The base is derived from the audio path, so a dot in the name must not
// confuse it: "01. Intro.mp3" owns "01. Intro.lrc", not "01.lrc".
func TestDeleteFiles_CarriesLyricsWithDotsInTheTrackName(t *testing.T) {
	music, data := t.TempDir(), t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	seedTrack(t, leaf, "01. Intro.mp3", "words")

	rep := delRoute(t, []string{"A/01. Intro.mp3"})

	if len(rep.Lyrics) != 1 || rep.Lyrics[0] != "A/01. Intro.lrc" {
		t.Errorf("lyrics = %v, want [A/01. Intro.lrc]", rep.Lyrics)
	}
}

// Two tracks in one directory, one deleted: the other track's lyrics must not
// be taken with it. A rule that keyed on "some .lrc in this directory" would
// take the wrong one.
func TestDeleteFiles_LeavesTheOtherTracksLyricsAlone(t *testing.T) {
	music, data := t.TempDir(), t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	seedTrack(t, leaf, "gone.mp3", "words")
	seedTrack(t, leaf, "stays.mp3", "more words")

	rep := delRoute(t, []string{"A/gone.mp3"})

	if len(rep.Lyrics) != 1 || rep.Lyrics[0] != "A/gone.lrc" {
		t.Errorf("lyrics = %v, want only [A/gone.lrc]", rep.Lyrics)
	}
	if _, err := os.Stat(filepath.Join(leaf, "stays.lrc")); err != nil {
		t.Errorf("a live track lost its lyrics: %v", err)
	}
}

// Deleting the .lrc itself must not try to carry itself: by the time the
// handler gets there the file is in the trash, and a second move would either
// fail confusingly or, worse, move some other file that happens to sit at
// that path.
func TestDeleteFiles_DeletingALyricsFileDoesNotCarryItTwice(t *testing.T) {
	music, data := t.TempDir(), t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	seedTrack(t, leaf, "song.mp3", "words")

	rep := delRoute(t, []string{"A/song.lrc"})

	if rep.Deleted != 1 || rep.Failed != 0 {
		t.Fatalf("deleted=%d failed=%d, want 1/0 (%+v)", rep.Deleted, rep.Failed, rep.Results)
	}
	if len(rep.Lyrics) != 0 {
		t.Errorf("lyrics = %v, want none — the deleted file was itself the lyrics", rep.Lyrics)
	}
}

// The common case, and the one that must not acquire a side effect: most
// tracks have no .lrc, and a delete must not report one.
func TestDeleteFiles_NoLyricsMeansNothingCarried(t *testing.T) {
	music, data := t.TempDir(), t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "bare.ogg"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := delRoute(t, []string{"A/bare.ogg"})

	if rep.Deleted != 1 {
		t.Fatalf("deleted=%d, want 1", rep.Deleted)
	}
	if len(rep.Lyrics) != 0 {
		t.Errorf("lyrics = %v, want none", rep.Lyrics)
	}
}

// A directory where the .lrc is not a regular file must be left where it is.
// Moving it would take a symlink whose target the library never owned, which
// is the same refusal DeleteFiles already applies to the file it was asked
// about.
func TestDeleteFiles_RefusesToCarryASymlinkedLyricsFile(t *testing.T) {
	music, data := t.TempDir(), t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "song.ogg"), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "precious.lrc")
	if err := os.WriteFile(outside, []byte("somebody else's lyrics"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(leaf, "song.lrc")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	rep := delRoute(t, []string{"A/song.ogg"})

	if len(rep.Lyrics) != 0 {
		t.Errorf("lyrics = %v; a symlink must never be followed into the trash", rep.Lyrics)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the file behind the symlink was moved: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(leaf, "song.lrc")); err != nil {
		t.Errorf("the symlink itself was taken: %v", err)
	}
}

// A lyrics file that cannot be moved must not fail the delete. The audio is
// already in the trash; reporting the row as failed would claim a deletion
// that happened, and a retry would find the file missing. The stranded .lrc
// is recoverable by the pruner, which now recognises it as an orphan.
func TestDeleteFiles_AStuckLyricsMoveDoesNotFailTheTrack(t *testing.T) {
	music := t.TempDir()
	// A regular file where the trash directory has to be created.
	data := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(data, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	seedTrack(t, leaf, "song.mp3", "words")

	// Both moves fail here — the batch cannot be created at all — so this
	// asserts the ordering claim differently: the track's own row is
	// reported honestly about what happened to IT.
	rep := delRoute(t, []string{"A/song.mp3"})

	if rep.Failed != 1 || rep.Deleted != 0 {
		t.Fatalf("deleted=%d failed=%d; with no writable trash neither can succeed (%+v)",
			rep.Deleted, rep.Failed, rep.Results)
	}
	if _, err := os.Stat(filepath.Join(leaf, "song.lrc")); err != nil {
		t.Errorf("the lyrics were lost while the audio survived: %v", err)
	}
}

// The self-reference guard, tested where its contract lives.
//
// Through the handler this case looks covered: by the time the helper runs,
// the named .lrc has already been moved to the trash, so the plain existence
// check refuses it too. That is a coincidence of ordering, and it is exactly
// the kind of accidental coverage a refactor destroys silently. Calling the
// helper directly — with the file still sitting on disk, as it would be if
// anyone ever checked before the move — is what actually pins the rule.
func TestLyricsBeside_RefusesToDeriveALyricsFileFromALyricsFile(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "song.lrc")
	if err := os.WriteFile(abs, []byte("words"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rel, ok := lyricsBeside("A/song.lrc", abs); ok {
		t.Errorf("lyricsBeside derived %q from a .lrc that is present on disk; the caller would move the file it is deleting", rel)
	}
}

// And the two shapes it must accept, with the file present, so the guard
// above cannot be "satisfied" by refusing everything.
func TestLyricsBeside_AcceptsOnlyTheTrackBesideIt(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "song.mp3")
	if err := os.WriteFile(audio, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "song.lrc"), []byte("words"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, ok := lyricsBeside("A/song.mp3", audio)
	if !ok || rel != "A/song.lrc" {
		t.Errorf("lyricsBeside = (%q, %v), want (A/song.lrc, true)", rel, ok)
	}
	// A sibling that is not named after this track must not be picked up.
	other := filepath.Join(dir, "other.mp3")
	if err := os.WriteFile(other, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.lrc"), []byte("words"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rel, ok := lyricsBeside("A/song.mp3", audio); !ok || rel != "A/song.lrc" {
		t.Errorf("lyricsBeside = (%q, %v); other.lrc must not be taken with song.mp3", rel, ok)
	}
	// A track with no lyrics: nothing to carry, and no error.
	bare := filepath.Join(dir, "bare.mp3")
	if err := os.WriteFile(bare, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rel, ok := lyricsBeside("A/bare.mp3", bare); ok {
		t.Errorf("lyricsBeside = (%q, true), want no match", rel)
	}
	// No extension at all: there is no base to derive from.
	if rel, ok := lyricsBeside("A/README", filepath.Join(dir, "README")); ok {
		t.Errorf("lyricsBeside = (%q, true), want no match for an extensionless name", rel)
	}
}
