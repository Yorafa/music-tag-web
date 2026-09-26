package tasks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go-music-tag/internal/testaudio"
)

// The re-arm contract.
//
// The indexer used to run once, at worker boot. Anything that put a file in
// the library afterwards kept duration=NULL, and since durationCandidates
// filters `duration > 0`, the fingerprint stage stopped seeing new tracks as
// candidates — degrading to the size-window fallback that misses exactly the
// re-encodes the stage exists to catch.
//
// The fix re-arms a run that made progress. These tests pin the two halves
// of that, and specifically the failure mode where the indexer keeps waking
// itself up forever.

func TestFpIndexHandler_RearmsAfterIndexing(t *testing.T) {
	if _, err := exec.LookPath("fpcalc"); err != nil {
		t.Skip("fpcalc not installed; the indexer is a no-op on this host")
	}
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	p := testaudio.SeedWAV(t, dir, "a.wav", 3)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	seedFolderRow(t, gdb, p, "music", fi.Size())

	calls := 0
	h := &FpIndexHandler{DB: gdb, Rearm: func() { calls++ }}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if calls != 1 {
		t.Errorf("re-arm called %d times after a productive run, want 1 — without it a "+
			"newly added file never gets a duration and the fingerprint stage goes blind",
			calls)
	}
}

// The convergence property. A run that indexed nothing has caught up with
// the library; re-arming anyway leaves the index waking itself up on an
// unchanged library, forever, for no benefit.
func TestFpIndexHandler_DoesNotRearmWhenIdle(t *testing.T) {
	gdb := newFpIndexDB(t)
	calls := 0
	h := &FpIndexHandler{DB: gdb, Rearm: func() { calls++ }}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if calls != 0 {
		t.Errorf("re-arm called %d times on an empty library, want 0", calls)
	}
}

// Re-arming must key on "made progress", not on "there was pending work".
// A library of files fpcalc cannot decode always has pending work, so
// keying on that would retry the same undecodable files at fpIndex cadence
// indefinitely.
func TestFpIndexHandler_DoesNotRearmWhenNothingIndexed(t *testing.T) {
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	// A real row that fpcalc will refuse, so it stays duration=0 forever.
	p := filepath.Join(dir, "broken.mp3")
	if err := os.WriteFile(p, []byte("not audio at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedFolderRow(t, gdb, p, "music", 15)

	calls := 0
	h := &FpIndexHandler{DB: gdb, Rearm: func() { calls++ }}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if calls != 0 {
		t.Errorf("re-arm called %d times after indexing nothing, want 0 — this is an "+
			"undecodable file, and retrying it on every cadence never succeeds", calls)
	}
}

// A nil Rearm must not panic: the handler is constructed without one
// wherever the task is exercised outside the worker's wiring.
func TestFpIndexHandler_NilRearmIsSafe(t *testing.T) {
	gdb := newFpIndexDB(t)
	h := &FpIndexHandler{DB: gdb, FPcalcPath: filepath.Join(t.TempDir(), "absent")}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
}
