package tasks

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestPruneEmpty_RemovesOnlyEmptyDirs is the feature: TidyFolder leaves the
// directories it emptied behind, and those should go.
func TestPruneEmpty_RemovesOnlyEmptyDirs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSIC_DIR", root)

	mustMkdir(t, filepath.Join(root, "Artist", "Album"))
	mustMkdir(t, filepath.Join(root, "Empty One"))
	mustMkdir(t, filepath.Join(root, "Empty Two", "Nested Empty"))
	mustWrite(t, filepath.Join(root, "Artist", "Album", "track.ogg"), "x")

	h := &PruneEmptyFoldersHandler{MusicRoot: root}
	removed, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	sort.Strings(removed)

	// "Empty Two" is in the list because it held nothing but the nested
	// empty directory: once that goes, "Empty Two" is empty too and the
	// upward cascade takes it in the same pass. Its parent "Empty Two/.."
	// (the library root) obviously stays.
	want := []string{"Empty One", "Empty Two", "Empty Two/Nested Empty"}
	if !equalStrings(removed, want) {
		t.Errorf("removed = %v, want %v", removed, want)
	}

	// The track and the directory holding it survive.
	if _, err := os.Stat(filepath.Join(root, "Artist", "Album", "track.ogg")); err != nil {
		t.Errorf("track was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Artist", "Album")); err != nil {
		t.Errorf("non-empty album dir was deleted: %v", err)
	}
}

// TestPruneEmpty_CascadesUpward is why the walk is bottom-up: a directory
// whose only contents were empty subdirectories is itself empty afterwards,
// and should not need a second run to disappear.
func TestPruneEmpty_CascadesUpward(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSIC_DIR", root)

	mustMkdir(t, filepath.Join(root, "A", "B", "C"))

	h := &PruneEmptyFoldersHandler{MusicRoot: root}
	removed, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	sort.Strings(removed)

	want := []string{"A", "A/B", "A/B/C"}
	if !equalStrings(removed, want) {
		t.Errorf("removed = %v, want %v (one pass should collapse the chain)", removed, want)
	}
	if _, err := os.Stat(filepath.Join(root, "A")); !os.IsNotExist(err) {
		t.Error("A should be gone after one pass")
	}
}

// TestPruneEmpty_KeepsSidecarOnlyDirs is the case that decides whether this
// feature is safe. Tidying moves the audio but a cover can be left behind, so
// "empty" has to mean literally no entries — a directory holding only a
// cover.jpg still has something worth keeping, and deleting it would destroy
// album art the tidy step failed to carry across.
func TestPruneEmpty_KeepsSidecarOnlyDirs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSIC_DIR", root)

	mustMkdir(t, filepath.Join(root, "Album"))
	mustWrite(t, filepath.Join(root, "Album", "cover.jpg"), "img")
	mustWrite(t, filepath.Join(root, "Album", "track.lrc"), "la")

	h := &PruneEmptyFoldersHandler{MusicRoot: root}
	removed, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want none — a dir with a cover and an .lrc is not empty", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "Album", "cover.jpg")); err != nil {
		t.Errorf("cover was deleted: %v", err)
	}
}

// TestPruneEmpty_NeverRemovesRoot guards the one directory whose removal
// would take the library with it.
func TestPruneEmpty_NeverRemovesRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSIC_DIR", root)
	mustMkdir(t, filepath.Join(root, "Empty"))

	h := &PruneEmptyFoldersHandler{MusicRoot: root}
	if _, err := h.pruneEmpty(context.Background(), nil); err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("the music root itself was removed: %v", err)
	}
}

// TestPruneEmpty_SkipsDataDir guards the app's own state. data/ lives under
// the music root and routinely contains empty subdirectories.
func TestPruneEmpty_SkipsDataDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSIC_DIR", root)

	mustMkdir(t, filepath.Join(root, "data", "covers", "album-1"))
	mustWrite(t, filepath.Join(root, "data", "keep.sqlite"), "db")

	h := &PruneEmptyFoldersHandler{MusicRoot: root}
	removed, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	for _, p := range removed {
		if p == "data" || filepath.Dir(p) == "data" || len(p) > 5 && p[:5] == "data/" {
			t.Errorf("removed %q from the protected data/ tree", p)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "data", "covers", "album-1")); err != nil {
		t.Errorf("data/covers/album-1 was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "keep.sqlite")); err != nil {
		t.Errorf("data/keep.sqlite was removed: %v", err)
	}
}

// TestPruneEmpty_ScopedToSubPaths covers the payload path: a caller can
// restrict the sweep, and the sweep still refuses to climb out of the scope
// it was given.
func TestPruneEmpty_ScopedToSubPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSIC_DIR", root)

	mustMkdir(t, filepath.Join(root, "in-scope", "empty-a"))
	mustMkdir(t, filepath.Join(root, "out-of-scope", "empty-b"))

	h := &PruneEmptyFoldersHandler{MusicRoot: root}
	removed, err := h.pruneEmpty(context.Background(),
		[][2]string{{"", filepath.Join(root, "in-scope")}})
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	// "in-scope" cascades too: it held only the empty child.
	if !equalStrings(removed, []string{"in-scope", "in-scope/empty-a"}) {
		t.Errorf("removed = %v, want [in-scope in-scope/empty-a]", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "out-of-scope", "empty-b")); err != nil {
		t.Errorf("out-of-scope dir was removed: %v", err)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
