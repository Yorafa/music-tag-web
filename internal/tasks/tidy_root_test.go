package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TidyRoot's empty case. This is the whole reason 整理目录 was unusable:
// the dialog gated its preview button on the root field, nothing in the
// API reports MUSIC_DIR, so the only way to enable it was to guess the
// server's absolute path.
func TestTidyRoot_EmptyMeansTheLibraryRoot(t *testing.T) {
	got, err := TidyRoot("/app/media", "")
	if err != nil {
		t.Fatalf("an empty root was refused: %v", err)
	}
	if got != "/app/media" {
		t.Errorf("root = %q, want the library root itself", got)
	}
}

func TestTidyRoot_BlankIsTreatedAsEmpty(t *testing.T) {
	got, err := TidyRoot("/app/media", "   ")
	if err != nil {
		t.Fatalf("a whitespace root was refused: %v", err)
	}
	if got != "/app/media" {
		t.Errorf("root = %q, want the library root itself", got)
	}
}

// The strictness still holds where the typo risk actually is: a value the
// user typed. Relaxing the empty case must not have relaxed this one.
func TestTidyRoot_NonEmptyStillMustBeAbsolute(t *testing.T) {
	if _, err := TidyRoot("/app/media", "SomeAlbum"); err == nil {
		t.Fatal("a relative root was accepted; a stray word would build a nested tree")
	}
}

func TestTidyRoot_EmptyWithNoLibraryRootStillRefuses(t *testing.T) {
	// Both empty is not "the library root" — there is no library root to
	// mean. SafeAbs refused this before, and it must keep refusing.
	if _, err := TidyRoot("", ""); err == nil {
		t.Fatal("an empty root with no configured library was accepted")
	}
}

// A tidy with no root_path lands directly in the library root's own
// tree — the "reorganise in place" case, which is the one the dialog now
// leads with. It must actually move files, not just resolve a string.
func TestTidy_EmptyRootReorganisesInPlace(t *testing.T) {
	music, track := tidyFixture(t)

	if err := tidyTask(t, music, TidyFolderPayload{
		MusicPaths: []string{track},
		Segments:   []string{"${artist}", "${album}"},
	}); err != nil {
		t.Fatalf("tidy with no root_path failed: %v", err)
	}

	want := filepath.Join(music, "未知", "未知", "song.wav")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("file is not at %s: %v", want, err)
	}
}

// The preview has to agree with the move here too, or the plan the
// operator approved is not the thing that runs.
func TestPreviewTidy_EmptyRootPlansWhereTheMoveGoes(t *testing.T) {
	music, track := tidyFixture(t)

	h := &TidyFolderHandler{MusicRoot: music}
	rows, err := h.PreviewTidy(context.Background(), []string{track}, "", []string{"${artist}"})
	if err != nil {
		t.Fatalf("PreviewTidy with no root_path: %v", err)
	}
	if rows[0].Status != TidyPlanMove {
		t.Fatalf("status = %q (%s), want move", rows[0].Status, rows[0].Reason)
	}
	planned := rows[0].NewPath

	if err := h.tidyOne(context.Background(), track, TidyFolderPayload{
		// tidyOne is documented as taking an ALREADY-VALIDATED payload:
		// ProcessTask resolves the root once for the whole batch before
		// the loop. Mirror that here rather than making tidyOne resolve
		// its own root, which would give the batch two answers to the
		// same question.
		RootPath: music,
		Segments: []string{"${artist}"},
	}); err != nil {
		t.Fatalf("tidyOne: %v", err)
	}
	if _, err := os.Stat(planned); err != nil {
		t.Errorf("the plan said %s but the file is not there: %v", planned, err)
	}
}
