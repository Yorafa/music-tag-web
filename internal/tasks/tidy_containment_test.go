package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/testaudio"
)

// tidyOne used to take p.RootPath straight from the request body and pass it
// to utils.SafeJoin as its TRUSTED root — the one value SafeJoin assumes it
// can trust. So a caller could name any directory: the worker created the
// tree under it and then os.Rename'd library files into it. music_paths had
// no check at all, in either direction.
//
// It is easy to miss because the neighbouring defence is real:
// sanitizeTidySeg refuses tag-derived first_dir / second_dir segments
// precisely so they cannot carry a file out of the music root. The segments
// were checked; the root was not.

func tidyFixture(t *testing.T) (music string, track string) {
	t.Helper()
	base := t.TempDir()
	music = filepath.Join(base, "music")
	mustMkdir(t, filepath.Join(music, "Loose"))
	track = testaudio.SeedWAV(t, filepath.Join(music, "Loose"), "song.wav", 1)
	return music, track
}

func tidyTask(t *testing.T, music string, payload TidyFolderPayload) error {
	t.Helper()
	return (&TidyFolderHandler{MusicRoot: music}).ProcessTask(
		context.Background(), Task{Type: TypeTidyFolder, Payload: &payload})
}

func anyFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// The feature: a root_path outside the library is refused, and the file does
// not move.
func TestTidy_RejectsAnOutOfLibraryRootPath(t *testing.T) {
	music, track := tidyFixture(t)
	outside := t.TempDir()

	err := tidyTask(t, music, TidyFolderPayload{
		MusicPaths: []string{track},
		RootPath:   outside,
		FirstDir:   "artist",
	})
	if err == nil {
		t.Error("ProcessTask accepted a root_path outside the library")
	}
	if _, sErr := os.Stat(track); sErr != nil {
		t.Errorf("the track moved despite the refused root: %v", sErr)
	}
	if got := anyFilesUnder(t, outside); len(got) != 0 {
		t.Errorf("ESCAPED: %v were written outside the library", got)
	}
}

// A root_path that merely shares a prefix is still outside.
func TestTidy_RejectsASiblingOfTheLibrary(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "media")
	sibling := filepath.Join(base, "media-backup")
	mustMkdir(t, filepath.Join(music, "Loose"))
	track := testaudio.SeedWAV(t, filepath.Join(music, "Loose"), "song.wav", 1)

	err := tidyTask(t, music, TidyFolderPayload{
		MusicPaths: []string{track},
		RootPath:   sibling,
		FirstDir:   "artist",
	})
	if err == nil {
		t.Errorf("ProcessTask accepted %s as a root under %s", sibling, music)
	}
	if _, sErr := os.Stat(track); sErr != nil {
		t.Errorf("the track moved: %v", sErr)
	}
}

// The other direction: a source that is not in the library must not be
// moved. Without this the feature above is only half a control — a caller
// could still pull a file from anywhere on the filesystem into the library,
// or, with an out-of-root root_path, out of it.
func TestTidy_RejectsAnOutOfLibrarySource(t *testing.T) {
	music, keep := tidyFixture(t)
	strayDir := t.TempDir()
	stray := testaudio.SeedWAV(t, strayDir, "stray.wav", 1)

	// A legitimate root, so only the source is at fault.
	if err := tidyTask(t, music, TidyFolderPayload{
		MusicPaths: []string{stray},
		RootPath:   music,
		FirstDir:   "artist",
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if _, sErr := os.Stat(stray); sErr != nil {
		t.Errorf("ESCAPED: a file from outside the library was moved: %v", sErr)
	}
	// Nothing new may have appeared in the library either. The fixture
	// starts with exactly one track, so a second file would mean the stray
	// was pulled in.
	got := anyFilesUnder(t, music)
	if len(got) != 1 || got[0] != keep {
		t.Errorf("library files = %v, want only the original %s", got, keep)
	}
}

// A batch is per-file, not all-or-nothing: one bad row must not cost the
// other 499. This is the behaviour that decided the shape — a whole-task
// rejection would be simpler to write and wrong to ship.
func TestTidy_OneBadRowDoesNotSinkTheBatch(t *testing.T) {
	music, track := tidyFixture(t)
	outside := t.TempDir()
	stray := testaudio.SeedWAV(t, outside, "stray.wav", 1)

	if err := tidyTask(t, music, TidyFolderPayload{
		MusicPaths: []string{stray, track},
		RootPath:   music,
		FirstDir:   "artist",
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if _, sErr := os.Stat(track); sErr == nil {
		t.Error("the in-library track should have been tidied")
	}
	if _, sErr := os.Stat(stray); sErr != nil {
		t.Errorf("the out-of-library file should have been left alone: %v", sErr)
	}
}

// Containment must not break the feature. A legitimate root and a legitimate
// subdirectory both have to keep working, or the fix is "disable tidy".
func TestTidy_StillMovesFilesWithinTheLibrary(t *testing.T) {
	music, track := tidyFixture(t)
	sub := filepath.Join(music, "Loose") // a real subdirectory, not the root

	if err := tidyTask(t, music, TidyFolderPayload{
		MusicPaths: []string{track},
		RootPath:   sub,
		FirstDir:   "artist",
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if _, sErr := os.Stat(track); sErr == nil {
		t.Error("the track did not move")
	}
	moved := filepath.Join(sub, "未知", filepath.Base(track))
	if _, sErr := os.Stat(moved); sErr != nil {
		t.Errorf("expected the track at %s: %v", moved, sErr)
	}
}

// With no music root there is no way to know what is in bounds, so nothing
// moves. The read-only consumers treat an empty root as "no scope" and
// degrade to a narrower answer; a mutating one cannot.
func TestTidy_NoMusicRootMovesNothing(t *testing.T) {
	music, track := tidyFixture(t)
	h := &TidyFolderHandler{} // MusicRoot deliberately unset
	err := h.ProcessTask(context.Background(), Task{
		Type:    TypeTidyFolder,
		Payload: &TidyFolderPayload{MusicPaths: []string{track}, RootPath: music, FirstDir: "artist"},
	})
	if err == nil {
		t.Error("ProcessTask ran with no music root configured")
	}
	if _, sErr := os.Stat(track); sErr != nil {
		t.Errorf("the track moved with no root configured: %v", sErr)
	}
}
