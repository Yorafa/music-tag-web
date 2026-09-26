package tasks

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"testing"
)

// Two small things, both about what the prune REPORTS rather than what it
// does. Deletion was already correct in both cases, which is exactly why
// they went unnoticed: the file was gone either way, only the count and the
// list were wrong.

// A refused scope was logged once per pass, so a single out-of-root scope
// produced two identical lines. That line is what an operator reads when
// something was skipped, so a doubled one reads as two separate problems.
//
// Asserted on the log rather than on a return value because the doubling had
// no other observable effect: both passes refused the scope, so both lists
// came back correct either way. The defect was entirely in what got said.
func TestPrune_LogsARefusedScopeOnce(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)
	mustMkdir(t, filepath.Join(root, "empty"))
	addRow(t, gdb, filepath.Join(root, "gone.ogg"), "music")
	outside := t.TempDir()

	var buf bytes.Buffer
	restore := logOutput(&buf)
	defer restore()

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	payload := [][2]string{{"", outside}}

	if err := h.ProcessTask(context.Background(), Task{
		Type:    TypePruneEmptyFolders,
		Payload: &PruneEmptyFoldersPayload{SubPaths: payload},
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if n := bytes.Count(buf.Bytes(), []byte("out-of-root scope")); n != 1 {
		t.Errorf("ProcessTask logged the refusal %d times, want 1:\n%s", n, buf.String())
	}

	// The preview has the same shape and the same doubling.
	buf.Reset()
	if _, _, err := h.PreviewPrune(context.Background(), payload); err != nil {
		t.Fatalf("PreviewPrune: %v", err)
	}
	if n := bytes.Count(buf.Bytes(), []byte("out-of-root scope")); n != 1 {
		t.Errorf("PreviewPrune logged the refusal %d times, want 1:\n%s", n, buf.String())
	}
}

// logOutput redirects the standard logger for the duration of a test and
// returns the function that puts it back.
func logOutput(w *bytes.Buffer) func() {
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(w)
	log.SetFlags(0)
	return func() { log.SetOutput(prev); log.SetFlags(prevFlags) }
}

// Overlapping scopes walk the same ground twice. Deletion is idempotent so
// nothing breaks, but the dry run reports every occurrence, and the dialog's
// whole job is to be a list the user can check against what they remember
// deleting.
func TestPrune_DryRunReportsEachTargetOnce(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)
	mustMkdir(t, filepath.Join(root, "sub", "empty"))
	mustMkdir(t, filepath.Join(root, "lone-empty"))
	addRow(t, gdb, filepath.Join(root, "sub", "gone.ogg"), "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	// root and root/sub overlap, so root/sub is visited twice.
	overlapping := [][2]string{{"", root}, {"", filepath.Join(root, "sub")}}

	dirs, rows, err := h.PreviewPrune(context.Background(), overlapping)
	if err != nil {
		t.Fatalf("PreviewPrune: %v", err)
	}
	assertNoDuplicates(t, "empty_dirs", dirs)
	assertNoDuplicates(t, "vanished_rows", rows)

	// And the real run has to agree with what the preview promised, which is
	// the property the dialog rests on.
	realDirs, err := h.pruneEmpty(context.Background(), overlapping)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if !equalStrings(dirs, realDirs) {
		t.Errorf("preview listed %v but the run removed %v", dirs, realDirs)
	}
	realRows := h.pruneVanished(context.Background(), overlapping)
	if !equalStrings(rows, realRows) {
		t.Errorf("preview listed rows %v but the run removed %v", rows, realRows)
	}
}

func assertNoDuplicates(t *testing.T, label string, list []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, v := range list {
		if seen[v] {
			t.Errorf("%s lists %q twice: %v", label, v, list)
		}
		seen[v] = true
	}
}

// The overlapping-scope case above must not quietly become the ONLY case. A
// single scope has to keep producing a full list, or the dedupe has eaten the
// answer.
func TestPrune_SingleScopeStillReportsEverything(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)
	mustMkdir(t, filepath.Join(root, "a", "empty"))
	mustMkdir(t, filepath.Join(root, "b", "empty"))
	addRow(t, gdb, filepath.Join(root, "a", "gone.ogg"), "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	dirs, rows, err := h.PreviewPrune(context.Background(), nil)
	if err != nil {
		t.Fatalf("PreviewPrune: %v", err)
	}
	// a and b hold only an empty child, so the cascade takes all four.
	if !equalStrings(dirs, []string{"a", "a/empty", "b", "b/empty"}) {
		t.Errorf("dirs = %v, want [a a/empty b b/empty]", dirs)
	}
	if len(rows) != 1 {
		t.Errorf("rows = %v, want the one vanished row", rows)
	}
}
