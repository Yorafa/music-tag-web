package handler

import (
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/testaudio"
)

// TestApplyFileUpdate_RenameCarriesSidecar is the behaviour the 文件名 row
// promises: renaming a track in the detail dialog must take its lyrics
// with it.
//
// The .lrc is named after the audio file, so a rename that moves only the
// audio leaves the lyrics behind. The library then lists a track whose
// lyrics belong to a file that is no longer there, and the next save
// writes a second .lrc beside the new name — so the folder slowly fills
// with orphans that nothing ever cleans up.
func TestApplyFileUpdate_RenameCarriesSidecar(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	src := testaudio.SeedMP3(t, music, "old.mp3")

	// Save once with lyrics so the sidecar exists under the old name.
	if _, err := applyFileUpdate(src, map[string]interface{}{
		"title":               "Song",
		"is_save_lyrics_file": true,
		"lyrics":              "la la la",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(music, "old.lrc")); err != nil {
		t.Fatalf("fixture .lrc was not written: %v", err)
	}

	res, err := applyFileUpdate(src, map[string]interface{}{"filename": "new"})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if res.RenamedTo != "new.mp3" {
		t.Fatalf("RenamedTo = %q, want new.mp3", res.RenamedTo)
	}

	if _, err := os.Stat(filepath.Join(music, "new.lrc")); err != nil {
		t.Errorf("the .lrc did not follow the rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(music, "old.lrc")); !os.IsNotExist(err) {
		t.Errorf("old.lrc was left behind as an orphan")
	}
	for _, m := range res.FailedSidecars() {
		t.Errorf("unexpected sidecar failure: %+v", m)
	}
}

// A save that writes the .lrc AND renames in one request is the normal
// case from the dialog. The sidecar is written under the old name first,
// so it still has to be carried afterwards.
func TestApplyFileUpdate_SaveAndRenameInOneRequestCarriesSidecar(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	src := testaudio.SeedMP3(t, music, "old.mp3")

	res, err := applyFileUpdate(src, map[string]interface{}{
		"filename":            "new",
		"is_save_lyrics_file": true,
		"lyrics":              "fresh words",
	})
	if err != nil {
		t.Fatalf("applyFileUpdate: %v", err)
	}
	if res.RenamedTo != "new.mp3" {
		t.Fatalf("RenamedTo = %q, want new.mp3", res.RenamedTo)
	}
	if b, err := os.ReadFile(filepath.Join(music, "new.lrc")); err != nil {
		t.Errorf("new.lrc missing: %v", err)
	} else if string(b) != "fresh words" {
		t.Errorf("new.lrc = %q, want %q", b, "fresh words")
	}
	if _, err := os.Stat(filepath.Join(music, "old.lrc")); !os.IsNotExist(err) {
		t.Errorf("old.lrc was left behind as an orphan")
	}
}

// A save with no rename must not touch sidecars at all — the track is
// staying put, so there is nothing to carry.
func TestApplyFileUpdate_NoRenameLeavesSidecarInPlace(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	src := testaudio.SeedMP3(t, music, "song.mp3")

	res, err := applyFileUpdate(src, map[string]interface{}{
		"is_save_lyrics_file": true,
		"lyrics":              "words",
	})
	if err != nil {
		t.Fatalf("applyFileUpdate: %v", err)
	}
	if len(res.Sidecars) != 0 {
		t.Errorf("Sidecars = %v, want none for a save with no rename", res.Sidecars)
	}
	if _, err := os.Stat(filepath.Join(music, "song.lrc")); err != nil {
		t.Errorf("song.lrc went missing on a plain save: %v", err)
	}
}

// A refused rename must not carry anything: the file never moved, so
// moving its sidecar would strand it in the opposite direction.
func TestApplyFileUpdate_RefusedRenameCarriesNothing(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	src := testaudio.SeedMP3(t, music, "source.mp3")
	testaudio.SeedMP3(t, music, "taken.mp3")
	if err := os.WriteFile(filepath.Join(music, "source.lrc"), []byte("words"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := applyFileUpdate(src, map[string]interface{}{"filename": "taken"})
	if err == nil {
		t.Fatal("expected a collision error")
	}
	if len(res.Sidecars) != 0 {
		t.Errorf("Sidecars = %v, want none when the rename was refused", res.Sidecars)
	}
	if _, err := os.Stat(filepath.Join(music, "source.lrc")); err != nil {
		t.Errorf("source.lrc was moved even though the rename was refused: %v", err)
	}
}
