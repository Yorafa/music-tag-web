package tag

import (
	"os"
	"path/filepath"
	"testing"
)

// moveFailures filters the report down to the entries that did not land,
// which is what callers surface as warnings.
func moveFailures(moves []SidecarMove) []SidecarMove {
	var out []SidecarMove
	for _, m := range moves {
		if m.Err != nil {
			out = append(out, m)
		}
	}
	return out
}

// TestMoveSidecars_LrcFollowsRename is the tracer bullet: renaming an
// audio file must carry its .lrc along.
//
// The .lrc is named after the audio file's base name, so a rename that
// moves only the audio leaves the lyrics behind under the old name. The
// library then shows a track whose lyrics file is an orphan, and any
// later save writes a second .lrc beside the new name.
func TestMoveSidecars_LrcFollowsRename(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.mp3")
	newPath := filepath.Join(dir, "new.mp3")
	oldLrc := filepath.Join(dir, "old.lrc")
	if err := os.WriteFile(oldLrc, []byte("la la la"), 0o644); err != nil {
		t.Fatal(err)
	}

	if fails := moveFailures(MoveSidecars(oldPath, newPath)); len(fails) != 0 {
		t.Fatalf("MoveSidecars reported %d failures: %v", len(fails), fails)
	}

	if b, err := os.ReadFile(filepath.Join(dir, "new.lrc")); err != nil {
		t.Errorf("new.lrc not written: %v", err)
	} else if string(b) != "la la la" {
		t.Errorf("new.lrc = %q, want %q", b, "la la la")
	}
	if _, err := os.Stat(oldLrc); !os.IsNotExist(err) {
		t.Errorf("old.lrc survived the move — it is now an orphan")
	}
}

// A stale sidecar from a since-deleted file must not block the move. The
// audio rename already claimed the target name, so a `new.lrc` sitting
// there belongs to a track that is no longer in the library and would
// otherwise be shown as the lyrics of the track that just took its place.
func TestMoveSidecars_OverwritesStaleDestination(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.mp3")
	newPath := filepath.Join(dir, "new.mp3")
	if err := os.WriteFile(filepath.Join(dir, "old.lrc"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.lrc"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if fails := moveFailures(MoveSidecars(oldPath, newPath)); len(fails) != 0 {
		t.Fatalf("a pre-existing sidecar blocked the move: %v", fails)
	}

	b, err := os.ReadFile(filepath.Join(dir, "new.lrc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "current" {
		t.Errorf("new.lrc = %q, want %q — the stale sidecar won", b, "current")
	}
}

// Most tracks have no .lrc at all. That is not a failure and must not
// produce a warning, or every rename of an unaccompanied file would
// report a problem that does not exist.
func TestMoveSidecars_NoSidecarIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	moves := MoveSidecars(filepath.Join(dir, "old.mp3"), filepath.Join(dir, "new.mp3"))
	if fails := moveFailures(moves); len(fails) != 0 {
		t.Errorf("no sidecar existed, yet %d failures were reported: %v", len(fails), fails)
	}
	if len(moves) != 0 {
		t.Errorf("MoveSidecars = %v, want no entries when there is nothing to move", moves)
	}
}

// Tidy-folder moves a track into a different directory while keeping its
// base name. Everything filed beside it has to travel too, or the album
// is left split across two folders: audio in the new one, lyrics and
// cover in the old one. Nothing in the library joins those back up.
func TestMoveSidecars_DirectoryMoveCarriesCover(t *testing.T) {
	oldDir := t.TempDir()
	newDir := filepath.Join(t.TempDir(), "NewAlbum")
	oldPath := filepath.Join(oldDir, "song.mp3")
	newPath := filepath.Join(newDir, "song.mp3")

	for _, f := range []struct{ name, body string }{
		{"song.lrc", "words"},
		{"cover-MyAlbum.jpg", "JPEGDATA"},
		{"cover-MyAlbum.png", "PNGDATA"},
	} {
		if err := os.WriteFile(filepath.Join(oldDir, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if fails := moveFailures(MoveSidecars(oldPath, newPath)); len(fails) != 0 {
		t.Fatalf("MoveSidecars reported %d failures: %v", len(fails), fails)
	}

	for _, name := range []string{"song.lrc", "cover-MyAlbum.jpg", "cover-MyAlbum.png"} {
		if _, err := os.Stat(filepath.Join(newDir, name)); err != nil {
			t.Errorf("%s did not follow the audio into the new folder: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(oldDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was left behind in the old folder", name)
		}
	}
}

// A base-name-only rename must NOT drag the cover along: covers are keyed
// by album, the album did not change, and the cover is still correct
// where it is. Moving it would needlessly churn a file shared by every
// track in the folder.
func TestMoveSidecars_BaseRenameLeavesCoverAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cover-MyAlbum.jpg"), []byte("JPEGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}

	MoveSidecars(filepath.Join(dir, "old.mp3"), filepath.Join(dir, "new.mp3"))

	if _, err := os.Stat(filepath.Join(dir, "cover-MyAlbum.jpg")); err != nil {
		t.Errorf("cover was moved by a base-name rename: %v", err)
	}
}

// Only real sidecars follow. The folder is full of unrelated files and
// the tidy job walks all of it, so a `cover-` prefix alone is not enough
// to sweep something up.
func TestMoveSidecars_IgnoresNonSidecarFiles(t *testing.T) {
	oldDir := t.TempDir()
	newDir := filepath.Join(t.TempDir(), "NewAlbum")
	for _, name := range []string{
		"cover-MyAlbum.txt",     // not an image
		"cover-.jpg",            // no album segment
		"cover-MyAlbum.exe",     // not an image
		"notacover-MyAlbum.jpg", // wrong prefix
		"album.nfo",             // unrelated
		"cover-MyAlbum.jpg.bak", // wrong extension
	} {
		if err := os.WriteFile(filepath.Join(oldDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	newDirEntries, _ := os.ReadDir(oldDir)
	MoveSidecars(filepath.Join(oldDir, "song.mp3"), filepath.Join(newDir, "song.mp3"))

	got, _ := os.ReadDir(newDir)
	if len(got) != 0 {
		names := []string{}
		for _, e := range got {
			names = append(names, e.Name())
		}
		t.Errorf("non-sidecar files were carried across: %v", names)
	}
	if len(newDirEntries) == 0 {
		t.Fatal("fixture setup failed")
	}
}
