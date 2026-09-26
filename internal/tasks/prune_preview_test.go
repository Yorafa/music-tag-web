package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The confirmation dialog is only worth showing if the list it renders is
// the list the task will act on, and if rendering it changed nothing. Both
// halves are testable, and both have to hold: a preview that deleted
// something would be the worst possible bug in this feature, and one that
// disagreed with the real pass would make the confirmation a lie.

func TestPreviewPrune_RemovesNothing(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	// An empty directory, and a row for a file that is not there.
	mustMkdir(t, filepath.Join(root, "Artist", "Emptied Album"))
	orphan := filepath.Join(root, "Artist", "gone.ogg")
	addRow(t, gdb, orphan, "music")
	// A live row, which must not be listed as a candidate.
	live := filepath.Join(root, "Artist", "live.ogg")
	mustWriteNested(t, live, "still here")
	addRow(t, gdb, live, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	dirs, rows, err := h.PreviewPrune(context.Background(), nil)
	if err != nil {
		t.Fatalf("PreviewPrune: %v", err)
	}

	// The preview names the real targets. "Artist" itself still holds
	// live.ogg, so it is not a candidate — the real pass would leave it
	// alone too, and a preview that listed it would be over-reporting.
	if !equalStrings(dirs, []string{"Artist/Emptied Album"}) {
		t.Errorf("empty_dirs = %v, want [Artist/Emptied Album]", dirs)
	}
	if !equalStrings(rows, []string{"Artist/gone.ogg"}) {
		t.Errorf("vanished_rows = %v, want [Artist/gone.ogg]", rows)
	}

	// ...and touched neither the filesystem nor the table.
	if _, err := os.Stat(filepath.Join(root, "Artist", "Emptied Album")); err != nil {
		t.Errorf("the preview deleted a directory: %v", err)
	}
	if !rowExists(t, gdb, orphan) {
		t.Error("the preview deleted an index row")
	}
	if !rowExists(t, gdb, live) {
		t.Error("the preview deleted the row of a file that exists")
	}
}

// The point of the dialog: what it shows is what the real run does. A
// preview that drifted from the implementation would make the confirmation
// worse than no confirmation at all — the user approves a list, and
// something else happens.
func TestPreviewPrune_AgreesWithTheRealRun(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	mustMkdir(t, filepath.Join(root, "empty-a"))
	mustMkdir(t, filepath.Join(root, "nested", "empty-b"))
	addRow(t, gdb, filepath.Join(root, "nested", "gone.ogg"), "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	previewDirs, previewRows, err := h.PreviewPrune(context.Background(), nil)
	if err != nil {
		t.Fatalf("PreviewPrune: %v", err)
	}

	realDirs, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	realRows := h.pruneVanished(context.Background(), nil)

	if !equalStrings(previewDirs, realDirs) {
		t.Errorf("preview listed dirs %v but the real pass removed %v", previewDirs, realDirs)
	}
	if !equalStrings(previewRows, realRows) {
		t.Errorf("preview listed rows %v but the real pass removed %v", previewRows, realRows)
	}

	// And the real pass really did remove them, so the agreement above is
	// not two empty lists trivially matching.
	if _, err := os.Stat(filepath.Join(root, "empty-a")); !os.IsNotExist(err) {
		t.Errorf("empty-a survived the real pass: %v", err)
	}
	if rowExists(t, gdb, filepath.Join(root, "nested", "gone.ogg")) {
		t.Error("the vanished row survived the real pass")
	}
}

// A clean library must produce an empty preview, not an error and not a
// list full of folders: the dialog's whole job is to say "nothing to do".
func TestPreviewPrune_CleanLibraryReportsNothing(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	mustWriteNested(t, filepath.Join(root, "Artist", "track.ogg"), "audio")
	addRow(t, gdb, filepath.Join(root, "Artist", "track.ogg"), "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	dirs, rows, err := h.PreviewPrune(context.Background(), nil)
	if err != nil {
		t.Fatalf("PreviewPrune: %v", err)
	}
	if len(dirs) != 0 || len(rows) != 0 {
		t.Errorf("preview on a clean library = %v dirs, %v rows; want both empty", dirs, rows)
	}
}

// The preview is a walk, so it honours cancellation like the real pass —
// and says so, rather than returning a partial list that reads as complete.
func TestPreviewPrune_StopsOnCancelledContext(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)
	mustMkdir(t, filepath.Join(root, "empty-a"))
	addRow(t, gdb, filepath.Join(root, "gone.ogg"), "music")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	dirs, rows, err := h.PreviewPrune(ctx, nil)
	if err == nil {
		t.Fatalf("PreviewPrune returned no error for a cancelled context: %v / %v", dirs, rows)
	}
	if len(dirs) != 0 || len(rows) != 0 {
		t.Errorf("preview after cancellation = %v / %v, want both empty", dirs, rows)
	}
}
