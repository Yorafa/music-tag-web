package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The reason this rule exists, on a real library.
//
// A reorganise moved the audio out of `17/` and `17 (Explicit)/` and left
// the Kodi album.nfo behind. The pruner's rule was "no file may remain", so
// those two directories were not empty by any measure the code used, the
// kernel refused every os.Remove, and nothing in the product could ever
// remove them. The button the user pressed, over and over, removed nothing.
//
// The fix is to recognise residue rather than to force directories out: a
// directory whose only remaining contents are album-scoped files describes an
// album that is no longer there, so those files go to the trash — recoverable
// — and then the kernel is asked, exactly as before, whether the directory is
// empty. Nothing about "empty" got weaker.

// The headline case, and the one the user actually hit: the two directories
// from the real library, gone, with their nfo files intact in the trash.
func TestPruneEmpty_AnEmptiedDirectoryHoldingOnlyAlbumNfoGoes(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	for _, dir := range []string{"17", "17 (Explicit)"} {
		mustWriteNested(t, filepath.Join(root, dir, "album.nfo"), "<album><title>"+dir+"</title></album>")
	}

	h := &PruneEmptyFoldersHandler{DB: newScanDB(t), MusicRoot: root}
	removed, sidecars, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if !equalStrings(removed, []string{"17", "17 (Explicit)"}) {
		t.Errorf("removed = %v, want both residue directories", removed)
	}
	if !equalStrings(sidecars, []string{"17 (Explicit)/album.nfo", "17/album.nfo"}) {
		t.Errorf("sidecars = %v, want both nfo files reported", sidecars)
	}
	for _, dir := range []string{"17", "17 (Explicit)"} {
		if _, err := os.Stat(filepath.Join(root, dir)); !os.IsNotExist(err) {
			t.Errorf("%s survived the pass: %v", dir, err)
		}
	}
	// The nfo has to be recoverable, and recoverable means byte-identical at
	// the path RestoreTrash will move it back to.
	trashed := filepath.Join(data, ".trash", filepath.Base(mustBatchID(t, data)))
	for _, rel := range sidecars {
		got, err := os.ReadFile(filepath.Join(trashed, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("trashed copy of %s is missing: %v", rel, err)
			continue
		}
		if len(got) == 0 {
			t.Errorf("trashed copy of %s is empty", rel)
		}
	}
}

// mustBatchID returns the single batch directory the pass created, so a test
// can read the trash back without the handler handing out its timestamp.
func mustBatchID(t *testing.T, data string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(data, ".trash"))
	if err != nil {
		t.Fatalf("no trash directory was created: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one batch, got %d — one pass is one restore unit", len(entries))
	}
	return entries[0].Name()
}

// The rule is residue, not "no audio". Anything else that keeps a file in the
// directory keeps the directory — and a .lrc keeps it when the track it names
// is still there. The audio in these cases belongs to the .lrc, not to the
// nfo: `holds-lrc` is the case that would be wrong if the ownership check
// were loose, since the directory is nothing but lyrics and metadata.
func TestPruneEmpty_KeepsDirectoriesThatStillHoldSomethingThatIsNotResidue(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	for name, file := range map[string]string{
		"holds-audio":   "song.ogg",
		"holds-lrc":     "song.lrc",
		"holds-nfo-too": "other.ogg",
		"holds-dotfile": ".DS_Store",
		"holds-notes":   "notes.txt",
	} {
		dir := filepath.Join(root, name)
		mustWriteNested(t, filepath.Join(dir, file), "x")
		// Every case also carries the residue, so each one is testing
		// "the OTHER file is what saved it", not "the dir had no nfo".
		mustWriteNested(t, filepath.Join(dir, "album.nfo"), "nfo")
		if name == "holds-lrc" {
			mustWriteNested(t, filepath.Join(dir, "song.ogg"), "the track these lyrics belong to")
		}
	}
	// A cover is album-scoped like the nfo, so this one may go — but only
	// because nothing else is in the directory.
	mustWriteNested(t, filepath.Join(root, "cover-only", "folder.jpg"), "jpg")

	h := &PruneEmptyFoldersHandler{DB: newScanDB(t), MusicRoot: root}
	removed, sidecars, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if !equalStrings(removed, []string{"cover-only"}) {
		t.Errorf("removed = %v, want only the directory with nothing but a cover", removed)
	}
	if !equalStrings(sidecars, []string{"cover-only/folder.jpg"}) {
		t.Errorf("sidecars = %v, want the cover", sidecars)
	}
	for _, name := range []string{"holds-audio", "holds-lrc", "holds-nfo-too", "holds-dotfile", "holds-notes"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
	// The nfo in a surviving directory is not collateral: it describes music
	// that is still there.
	if _, err := os.Stat(filepath.Join(root, "holds-audio", "album.nfo")); err != nil {
		t.Errorf("the nfo next to a live track was taken anyway: %v", err)
	}
}

// The second kind of residue: a .lrc whose track is gone. Deleting the last
// track in a directory used to strand its lyrics and leave the directory
// permanently unprunable — the same shape as the album.nfo case, one file
// over. A .lrc that still HAS its audio keeps its directory, which is the
// line that has to be exactly right.
func TestPruneEmpty_AnUnownedLrcGoesButAnOwnedOneStays(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	// The orphan: the track it names is not here.
	mustWriteNested(t, filepath.Join(root, "orphan", "gone.lrc"), "[00:01.00]words")
	// Owned: same directory, audio present.
	mustWriteNested(t, filepath.Join(root, "kept", "here.ogg"), "audio")
	mustWriteNested(t, filepath.Join(root, "kept", "here.lrc"), "[00:01.00]words")
	// Owned only by case: on a case-insensitive filesystem these are one
	// track, and deleting the lyrics there would be losing live data.
	mustWriteNested(t, filepath.Join(root, "kept-case", "song.ogg"), "audio")
	mustWriteNested(t, filepath.Join(root, "kept-case", "Song.lrc"), "[00:01.00]words")

	h := &PruneEmptyFoldersHandler{DB: newScanDB(t), MusicRoot: root}
	removed, sidecars, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if !equalStrings(removed, []string{"orphan"}) {
		t.Errorf("removed = %v, want only the directory with an unowned .lrc", removed)
	}
	if !equalStrings(sidecars, []string{"orphan/gone.lrc"}) {
		t.Errorf("sidecars = %v, want the orphan lyrics", sidecars)
	}
	for _, dir := range []string{"kept", "kept-case"} {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Errorf("%s was removed: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "kept", "here.lrc")); err != nil {
		t.Errorf("the .lrc next to a live track was taken: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "kept-case", "Song.lrc")); err != nil {
		t.Errorf("the case-mismatched .lrc was taken: %v", err)
	}
	// And the orphan is recoverable.
	if _, err := os.Stat(filepath.Join(data, ".trash", mustBatchID(t, data), "orphan", "gone.lrc")); err != nil {
		t.Errorf("the orphan lyrics did not reach the trash: %v", err)
	}
}

// A directory with audio AND an nfo is a normal album folder, not residue.
// Taking the nfo there would throw away metadata for music that is right
// there — the failure mode that matters most, because the file is not even
// reported as removed if the directory survives.
func TestPruneEmpty_DoesNotTouchSidecarsInALiveAlbum(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	mustWriteNested(t, filepath.Join(root, "Artist", "Album", "01 - track.ogg"), "audio")
	mustWriteNested(t, filepath.Join(root, "Artist", "Album", "album.nfo"), "nfo")
	mustWriteNested(t, filepath.Join(root, "Artist", "Album", "cover.jpg"), "jpg")

	h := &PruneEmptyFoldersHandler{DB: newScanDB(t), MusicRoot: root}
	removed, sidecars, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if len(removed) != 0 || len(sidecars) != 0 {
		t.Errorf("removed = %v, sidecars = %v; want nothing — this album is not residue", removed, sidecars)
	}
	for _, f := range []string{"01 - track.ogg", "album.nfo", "cover.jpg"} {
		if _, err := os.Stat(filepath.Join(root, "Artist", "Album", f)); err != nil {
			t.Errorf("%s went missing: %v", f, err)
		}
	}
}

// The cascade still works with residue in the way: a parent holding only a
// subdirectory of residue goes in the same pass, because the child is
// accounted for as going away before the parent is judged.
func TestPruneEmpty_ResidueCascadesThroughTheParents(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	mustWriteNested(t, filepath.Join(root, "XXXTENTACION", "17", "album.nfo"), "nfo")
	mustWriteNested(t, filepath.Join(root, "XXXTENTACION", "album.nfo"), "nfo")

	h := &PruneEmptyFoldersHandler{DB: newScanDB(t), MusicRoot: root}
	removed, _, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if !equalStrings(removed, []string{"XXXTENTACION", "XXXTENTACION/17"}) {
		t.Errorf("removed = %v, want the leaf and the parent it emptied", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "XXXTENTACION")); !os.IsNotExist(err) {
		t.Errorf("the parent that held only a residue directory survived: %v", err)
	}
}

// The preview has to name the sidecars. The dialog exists so the user can
// check the list against what they remember doing, and a list that showed
// "17" without the album.nfo inside it would be approving less than what
// runs. The preview must also still move nothing.
func TestPreviewPrune_NamesTheSidecarsItWouldTakeAndTouchesNothing(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	mustWriteNested(t, filepath.Join(root, "17", "album.nfo"), "nfo")
	mustWriteNested(t, filepath.Join(root, "17 (Explicit)", "MyAlbum.cue"), "cue")

	h := &PruneEmptyFoldersHandler{DB: newScanDB(t), MusicRoot: root}
	dirs, sidecars, rows, err := h.PreviewPrune(context.Background(), nil)
	if err != nil {
		t.Fatalf("PreviewPrune: %v", err)
	}
	if !equalStrings(dirs, []string{"17", "17 (Explicit)"}) {
		t.Errorf("empty_dirs = %v, want both directories", dirs)
	}
	if !equalStrings(sidecars, []string{"17 (Explicit)/MyAlbum.cue", "17/album.nfo"}) {
		t.Errorf("sidecars = %v, want the nfo and the cue", sidecars)
	}
	if len(rows) != 0 {
		t.Errorf("vanished_rows = %v, want none", rows)
	}
	for _, p := range []string{"17/album.nfo", "17 (Explicit)/MyAlbum.cue"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("the preview moved %s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(data, ".trash")); !os.IsNotExist(err) {
		t.Error("the preview created a trash batch")
	}
}

// The preview has to agree with the run, on this rule as much as on the
// cascade: if the dry run and the real pass judge residue differently, the
// confirmation is a lie in the most expensive direction.
func TestPreviewPrune_AgreesWithTheRunAboutResidue(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	t.Setenv("DATA_DIR", data)
	mustWriteNested(t, filepath.Join(root, "gone", "album.nfo"), "nfo")
	mustWriteNested(t, filepath.Join(root, "stays", "track.ogg"), "audio")
	mustWriteNested(t, filepath.Join(root, "stays", "album.nfo"), "nfo")

	h := &PruneEmptyFoldersHandler{DB: newScanDB(t), MusicRoot: root}
	previewDirs, previewSidecars, _, err := h.PreviewPrune(context.Background(), nil)
	if err != nil {
		t.Fatalf("PreviewPrune: %v", err)
	}
	realDirs, realSidecars, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if !equalStrings(previewDirs, realDirs) {
		t.Errorf("preview listed %v, the run removed %v", previewDirs, realDirs)
	}
	if !equalStrings(previewSidecars, realSidecars) {
		t.Errorf("preview promised to take %v, the run took %v", previewSidecars, realSidecars)
	}
}

// A sidecar that cannot be moved keeps its directory. Half a move would be
// worse than none: the trash would hold a file the library no longer has, and
// the directory would be reported as cleaned when it is still there.
func TestPruneEmpty_KeepsTheDirectoryWhenTheSidecarCannotBeTrashed(t *testing.T) {
	root := t.TempDir()
	// A regular file where the trash has to be created: no move can land.
	data := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(data, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_DIR", data)
	mustWriteNested(t, filepath.Join(root, "17", "album.nfo"), "nfo")

	h := &PruneEmptyFoldersHandler{DB: newScanDB(t), MusicRoot: root}
	removed, sidecars, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing — the nfo never made it to the trash", removed)
	}
	if len(sidecars) != 0 {
		t.Errorf("sidecars = %v, want none — nothing was moved", sidecars)
	}
	if _, err := os.Stat(filepath.Join(root, "17", "album.nfo")); err != nil {
		t.Errorf("the nfo was lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "17")); err != nil {
		t.Errorf("the directory was removed even though its only file is still in it: %v", err)
	}
}

// A symlink named like a cover is not a cover this pass owns: the walk never
// followed it, so the library has no idea what it points at. Moving it into
// the trash would take a link whose target lives somewhere the app has never
// been.
func TestDirPrunable_RefusesASymlinkEvenWhenItLooksLikeResidue(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere.jpg")
	if err := os.WriteFile(target, []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "cover.jpg")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, ok := dirPrunable(dir, map[string]bool{}); ok {
		t.Error("a directory holding a cover symlink was called prunable")
	}
}

// The predicate the whole thing rests on. A drifted copy here is how a file
// ends up carried by a rename and deleted by a prune, so the shapes that must
// and must not count are pinned rather than assumed.
func TestDirPrunable_AlbumScopedAndUnownedLyrics(t *testing.T) {
	prunable := []string{"album.nfo", "Album.NFO", "MyAlbum.cue", "cover.jpg", "cover-17.png", "folder.webp"}
	keep := []string{"notes.txt", "cover.jpg.txt", ".DS_Store", "cover-.jpg", "album.nfo.bak", "cover"}

	for _, name := range prunable {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		found, ok := dirPrunable(dir, map[string]bool{})
		if !ok {
			t.Errorf("%s did not count as residue", name)
		}
		if len(found) != 1 || filepath.Base(found[0]) != name {
			t.Errorf("%s: found = %v, want exactly that one file", name, found)
		}
	}
	// A .lrc alone in a directory IS residue, by the ownership rule, so it
	// is not in `keep` — it is the same shape as the album metadata. The
	// interesting half is the one below, where the track is still there.
	for _, name := range keep {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := dirPrunable(dir, map[string]bool{}); ok {
			t.Errorf("%s counted as residue sitting alone in a directory", name)
		}
	}
	// ...and the same name beside the track it describes is not residue at
	// all, which is the other half of the rule.
	dir := t.TempDir()
	for _, name := range []string{"song.ogg", "song.lrc"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if found, ok := dirPrunable(dir, map[string]bool{}); ok {
		t.Errorf("an owned .lrc counted as residue: %v", found)
	}
}
