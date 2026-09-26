package tasks

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// The scope comes straight out of the request body, and this pass removes
// directories. It used to walk whatever it was handed while its sibling
// pruneVanished skipped out-of-root scopes — so an authenticated caller
// could name any directory the worker can write and have its empty
// subdirectories removed. "Only empty ones" is a property of os.Remove,
// not access control.
//
// The sibling's version of this test is TestPruneVanished_IgnoresOutOfRootScope.
func TestPruneEmpty_IgnoresOutOfRootScope(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSIC_DIR", root)

	outside := t.TempDir()
	// A sibling of the library, not a child: this is the shape that has to
	// be refused, because a child of root cannot be reached by escaping it.
	victim := filepath.Join(outside, "important-empty-dir")
	mustMkdir(t, victim)
	// And a legitimate in-scope directory in the same payload, so the
	// refusal cannot be satisfied by dropping the whole request.
	legit := filepath.Join(root, "in-scope", "empty")
	mustMkdir(t, legit)

	h := &PruneEmptyFoldersHandler{MusicRoot: root}
	removed, err := h.pruneEmpty(context.Background(),
		[][2]string{{"", outside}, {"", filepath.Join(root, "in-scope")}})
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}

	if _, err := os.Stat(victim); err != nil {
		t.Errorf("ESCAPED: an empty directory outside the music root was removed: %v", err)
	}
	for _, r := range removed {
		if strings.HasPrefix(r, "..") {
			t.Errorf("removed = %v, which names a path outside the library", removed)
		}
	}
	// The in-scope half still ran: containment filters scopes, it does not
	// discard the request.
	if !equalStrings(removed, []string{"in-scope", "in-scope/empty"}) {
		t.Errorf("removed = %v, want [in-scope in-scope/empty]", removed)
	}
}

// A scope is a path, not a fragment. "../../etc" is the traversal shape, and
// an absolute path outside the root is the other one — neither may be
// re-read as a relative subpath of the library, which is what SafeJoin
// would do with them.
func TestPruneEmpty_IgnoresTraversalAndForeignAbsoluteScopes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSIC_DIR", root)

	outside := t.TempDir()
	mustMkdir(t, filepath.Join(outside, "empty"))
	mustMkdir(t, filepath.Join(filepath.Dir(root), "sibling-empty"))

	h := &PruneEmptyFoldersHandler{MusicRoot: root}
	for _, scope := range []string{
		outside, // absolute, elsewhere
		filepath.Join(filepath.Dir(root), "sibling-empty"), // absolute, a sibling
		"../" + filepath.Base(outside),                     // traversal
	} {
		removed, err := h.pruneEmpty(context.Background(), [][2]string{{"", scope}})
		if err != nil {
			t.Fatalf("pruneEmpty(%q): %v", scope, err)
		}
		if len(removed) != 0 {
			t.Errorf("scope %q removed %v, want nothing", scope, removed)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "empty")); err != nil {
		t.Errorf("empty dir outside the root was removed: %v", err)
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
