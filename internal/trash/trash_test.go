package trash

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMoveAside_FallsBackWhenRenameCannotWork covers the cross-device path.
//
// In the default compose layout MUSIC_DIR and DATA_DIR are two separate
// bind mounts, so os.Rename returns EXDEV and a rename-only implementation
// works in `go test` (one temp dir) and fails in every real deployment.
// The rename is injected because a single temp dir can never produce a
// real EXDEV.
//
// This used to live in the gateway handler package, next to DeleteFiles. The
// helper moved here when the pruner started putting files in the trash too,
// and so did its test: a test for a helper that is no longer in the package
// has nowhere to reach it from.
func TestMoveAside_FallsBackWhenRenameCannotWork(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp3")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "nested", "trash", "src.mp3")

	exdev := func(string, string) error {
		return &os.LinkError{Op: "rename", Old: src, New: dest, Err: syscall.EXDEV}
	}
	if err := moveAsideWith(src, dest, exdev); err != nil {
		t.Fatalf("moveAsideWith: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be gone")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("dest content = %q, want payload — the copy must be byte-exact", got)
	}
	// The fallback creates intermediate dirs; a rename-only version would
	// have left the copy impossible to place.
	if _, err := os.Stat(filepath.Dir(dest)); err != nil {
		t.Errorf("trash subdir not created: %v", err)
	}
}

// The copy path is reached for ANY rename error, not just EXDEV — a rename
// can fail on a read-only destination or a name the kernel dislikes, and
// copy+remove may still succeed where rename did not. What it must never do
// is report success while leaving the source in place, because every caller
// treats a nil error as "this file has left the library".
func TestMoveAside_FallbackRemovesTheSourceItCopied(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "album.nfo")
	if err := os.WriteFile(src, []byte("nfo"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "trash", "album.nfo")

	denied := func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EPERM} }
	if err := moveAsideWith(src, dest, denied); err != nil {
		t.Fatalf("moveAsideWith: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source survived a move that reported success — the library and the trash now hold the same file")
	}
}

// The rename fast path must be the path that actually runs when the two
// locations are on one device; otherwise the specs above would pass against a
// helper that only ever copies, and nothing would notice the extra IO.
func TestMoveAside_UsesRenameWhenItCan(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "song.lrc")
	if err := os.WriteFile(src, []byte("[00:01.00]hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "trash", "song.lrc")

	attempts := 0
	ok := func(s, d string) error {
		attempts++
		return os.Rename(s, d)
	}
	if err := moveAsideWith(src, dest, ok); err != nil {
		t.Fatalf("moveAsideWith: %v", err)
	}
	if attempts != 1 {
		t.Errorf("rename attempted %d times, want exactly 1 — a second attempt means the copy path ran too", attempts)
	}
}

// The batch directory is the unit the trash listing and the audit log speak
// in, so its name has to be stable and sorted the same way the listing sorts.
// A format change here silently orphans every existing batch.
func TestBatchDir_LayoutAndFormat(t *testing.T) {
	got := BatchDir("/app/data", time.Date(2026, 9, 27, 15, 27, 3, 0, time.UTC))
	want := "/app/data/.trash/20260927-152703"
	if got != want {
		t.Errorf("BatchDir = %q, want %q", got, want)
	}
	if !strings.HasPrefix(filepath.ToSlash(got), "/app/data/.trash/") {
		t.Error("batches must live under DATA_DIR/.trash — that is where ListTrash looks")
	}
}
