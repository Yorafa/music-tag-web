package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"go-music-tag/internal/db"
)

// mustWriteNested is mustWrite for a path whose parent directory has not
// been created. The existing mustWrite does not mkdir, so a first call with
// "Artist/live.ogg" fails to create the file and the test goes on to assert
// against a path that was never there.
func mustWriteNested(t *testing.T, p, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(p))
	mustWrite(t, p, content)
}

// addRow inserts an index row without creating the file, which is the whole
// point: a row whose file is absent is what pruneVanished exists to clear.
func addRow(t *testing.T, gdb *gorm.DB, path, fileType string) {
	t.Helper()
	if err := gdb.Create(&db.Folder{
		Name: filepath.Base(path), Path: path, FileType: fileType, UID: path,
	}).Error; err != nil {
		t.Fatalf("insert row %s: %v", path, err)
	}
}

func rowExists(t *testing.T, gdb *gorm.DB, path string) bool {
	t.Helper()
	var n int64
	if err := gdb.Model(&db.Folder{}).Where("path = ?", path).Count(&n).Error; err != nil {
		t.Fatalf("count %s: %v", path, err)
	}
	return n > 0
}

// The feature: a file deleted outside the app leaves a row behind, and the
// row should go.
func TestPruneVanished_RemovesRowsForDeletedFiles(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	live := filepath.Join(root, "Artist", "live.ogg")
	orphan := filepath.Join(root, "Artist", "orphan.ogg")
	mustWriteNested(t, live, "still here")
	addRow(t, gdb, live, "music")
	addRow(t, gdb, orphan, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removed := h.pruneVanished(context.Background(), nil)

	if !equalStrings(removed, []string{"Artist/orphan.ogg"}) {
		t.Errorf("removed = %v, want [Artist/orphan.ogg]", removed)
	}
	if rowExists(t, gdb, orphan) {
		t.Error("the row for the deleted file survived")
	}
	if !rowExists(t, gdb, live) {
		t.Error("the row for a file that still exists was deleted")
	}
}

// The property the whole design rests on: a row is deleted because the
// kernel says its file is gone, never because a walk failed to visit it.
//
// The banned alternative (scanner.go: WHERE path NOT IN) would delete this
// row too — the scan never saw the file — and with a wider scope it would
// take the entire unseen library with it.
func TestPruneVanished_JudgesEachRowByTheFilesystemNotByTheWalk(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	untouched := filepath.Join(root, "elsewhere", "unseen.ogg")
	mustWriteNested(t, untouched, "exists, but no walk came here")
	addRow(t, gdb, untouched, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removed := h.pruneVanished(context.Background(), nil)

	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing: the file exists", removed)
	}
	if !rowExists(t, gdb, untouched) {
		t.Error("a row whose file exists was deleted")
	}
}

// Scoped to a subtree, a row outside it must survive even when its file is
// genuinely gone. Without the scope check a partial prune would still reach
// the whole library.
func TestPruneVanished_ScopedToSubPaths(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	inScope := filepath.Join(root, "in-scope", "gone.ogg")
	outOfScope := filepath.Join(root, "out-of-scope", "also-gone.ogg")
	addRow(t, gdb, inScope, "music")
	addRow(t, gdb, outOfScope, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removed := h.pruneVanished(context.Background(),
		[][2]string{{"", filepath.Join(root, "in-scope")}})

	if !equalStrings(removed, []string{"in-scope/gone.ogg"}) {
		t.Errorf("removed = %v, want [in-scope/gone.ogg]", removed)
	}
	if !rowExists(t, gdb, outOfScope) {
		t.Error("a row outside the requested scope was deleted")
	}
}

// A payload path is untrusted input. A scope that escapes the library must
// not become a licence to delete rows anywhere on the filesystem.
func TestPruneVanished_IgnoresOutOfRootScope(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	gdb := newScanDB(t)

	elsewhere := filepath.Join(outside, "not-mine.ogg")
	addRow(t, gdb, elsewhere, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removed := h.pruneVanished(context.Background(), [][2]string{{"", outside}})

	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing: %s is outside the library", removed, outside)
	}
	if !rowExists(t, gdb, elsewhere) {
		t.Error("a row outside the music root was deleted")
	}
}

// Rows the download cache writes live outside MUSIC_DIR and describe files
// the app manages itself. Deleting them would be losing state, not tidying.
func TestPruneVanished_LeavesRowsOutsideTheLibrary(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	cached := "/tmp/audio_cache/youtube/abc123.mp3"
	addRow(t, gdb, cached, "youtube")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removed := h.pruneVanished(context.Background(), nil)

	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing: a cache row is not library state", removed)
	}
	if !rowExists(t, gdb, cached) {
		t.Error("a cache row outside the library was deleted")
	}
}

// Folder rows are not file rows. Their children's parent_id points at them,
// so removing one can strand the subtree below it — which is the failure
// TestScanKeepsTreeConnected exists to catch.
func TestPruneVanished_KeepsFolderRows(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	goneDir := filepath.Join(root, "deleted-album")
	addRow(t, gdb, goneDir, "folder")
	goneFile := filepath.Join(root, "deleted-album", "track.ogg")
	addRow(t, gdb, goneFile, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	h.pruneVanished(context.Background(), nil)

	if !rowExists(t, gdb, goneDir) {
		t.Error("a folder row was deleted; its children's parent_id would dangle")
	}
	if rowExists(t, gdb, goneFile) {
		t.Error("the file row under it should still have been removed")
	}
}

// The sibling is named so that an unescaped '_' matches it too: one
// character standing for any character is exactly how a single-character
// wildcard behaves. This is the test the ESCAPE clause exists for, and it
// is the one a path without an underscore in its name cannot make — which
// is how the missing ESCAPE went unnoticed in the first place.
func TestPruneVanished_HandlesUnderscoreInThePath(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	inside := filepath.Join(root, "Album_One", "gone.ogg")
	addRow(t, gdb, inside, "music")
	sibling := filepath.Join(root, "AlbumXOne", "also-gone.ogg")
	addRow(t, gdb, sibling, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removed := h.pruneVanished(context.Background(),
		[][2]string{{"", filepath.Join(root, "Album_One")}})

	if !equalStrings(removed, []string{"Album_One/gone.ogg"}) {
		t.Errorf("removed = %v, want only the row inside the scope", removed)
	}
	if !rowExists(t, gdb, sibling) {
		t.Error("a row in a sibling directory matched the '_' wildcard and was deleted")
	}
}

// "I could not tell" is not "it is gone".
//
// The stat gate is the only thing standing between a transient filesystem
// error and a deleted index row, so it has to be tested with a failure that
// is not ENOENT. A file standing where a directory should be gives ENOTDIR:
// the path still cannot be opened, but it is emphatically not proof that the
// audio is gone. (Chmod-based permission tests are not an option here — the
// suite runs as root, which reads through mode 000.)
func TestPruneVanished_KeepsRowsWhoseStatFailsForAnotherReason(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	// A regular file occupies the directory component of this path.
	blocker := filepath.Join(root, "blocker")
	mustWriteNested(t, blocker, "not a directory")
	unstatable := filepath.Join(blocker, "gone.ogg")
	addRow(t, gdb, unstatable, "music")

	// The premise has to hold: the point is a stat failure that is NOT
	// ENOENT, so a run where the platform reports plain "no such file"
	// would quietly stop testing anything.
	if _, err := os.Stat(unstatable); os.IsNotExist(err) {
		t.Skipf("stat reported ENOENT rather than the expected ENOTDIR: %v", err)
	}

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removed := h.pruneVanished(context.Background(), nil)

	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing: the stat failed for a reason other than ENOENT", removed)
	}
	if !rowExists(t, gdb, unstatable) {
		t.Error("a row was deleted on a stat error that does not mean the file is gone")
	}
}

// Two rows for one path are impossible, and that is worth pinning: it is
// why the pass needs no de-duplication of its own. An earlier version kept a
// `seen` set to make sure a path was not reported twice, and a mutation that
// removed it survived — because the guarantee it was providing is already
// enforced by the schema. If this test ever fails, that set is needed after
// all.
func TestPruneVanished_PathIsUniqueSoNoDedupeIsNeeded(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	gone := filepath.Join(root, "gone.ogg")
	if err := gdb.Create(&db.Folder{
		Name: "gone.ogg", Path: gone, FileType: "music", UID: "uid-one",
	}).Error; err != nil {
		t.Fatal(err)
	}
	err := gdb.Create(&db.Folder{
		Name: "gone.ogg", Path: gone, FileType: "music", UID: "uid-two",
	}).Error
	if err == nil {
		t.Fatal("music_folder accepted two rows for one path; the prune pass " +
			"would need to de-duplicate its report after all")
	}
}

// A music directory called "100% Hits" is not exotic. Unescaped, the '%'
// would match any run of characters, so the scope would cover rows in
// sibling directories and delete them along with the rest.
func TestPruneVanished_HandlesLikeWildcardsInThePath(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	// A row that really is inside the wildcarded directory.
	inside := filepath.Join(root, "100% Hits", "gone.ogg")
	addRow(t, gdb, inside, "music")
	// A sibling whose name merely starts with the same characters. The '%'
	// in the scope would match this directory name and sweep it in.
	sibling := filepath.Join(root, "1000 Hits", "gone-too.ogg")
	addRow(t, gdb, sibling, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	// Scope to the wildcarded directory only.
	removed := h.pruneVanished(context.Background(),
		[][2]string{{"", filepath.Join(root, "100% Hits")}})

	if !equalStrings(removed, []string{"100% Hits/gone.ogg"}) {
		t.Errorf("removed = %v, want only the row inside the scope", removed)
	}
	if !rowExists(t, gdb, sibling) {
		t.Error("a row in a sibling directory matched the LIKE wildcard and was deleted")
	}
}

// Running the pass twice must not double-count, so the audit entry keeps
// matching what happened to the table. The second scope finds nothing — the
// first already deleted the row.
func TestPruneVanished_CountsEachRowOnce(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	gone := filepath.Join(root, "gone.ogg")
	addRow(t, gdb, gone, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	scope := [][2]string{{"", root}, {"", root}}
	removed := h.pruneVanished(context.Background(), scope)

	if len(removed) != 1 {
		t.Errorf("removed = %v, want exactly one entry for a single row", removed)
	}
}

// An empty library root must not turn into "delete everything": a
// misconfigured MusicRoot resolves to a path with no rows under it, and the
// scope has to stay that narrow.
func TestPruneVanished_EmptyRootDeletesNothing(t *testing.T) {
	gdb := newScanDB(t)
	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: ""}
	if removed := h.pruneVanished(context.Background(), nil); len(removed) != 0 {
		t.Errorf("removed = %v with no music root configured, want nothing", removed)
	}
}

// The list the audit log records has to be what actually left the table —
// not what the code intended. Asserting "removed count == remaining count"
// would be nonsense; the invariant is that the table ends up with exactly the
// rows the function did not claim.
func TestPruneVanished_RemovedListMatchesTheTable(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	keep := filepath.Join(root, "Artist", "live.ogg")
	mustWriteNested(t, keep, "still here")
	addRow(t, gdb, keep, "music")
	addRow(t, gdb, filepath.Join(root, "Artist", "gone-a.ogg"), "music")
	addRow(t, gdb, filepath.Join(root, "b.ogg"), "music")

	var before int64
	if err := gdb.Model(&db.Folder{}).Count(&before).Error; err != nil {
		t.Fatal(err)
	}

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removed := h.pruneVanished(context.Background(), nil)

	var after int64
	if err := gdb.Model(&db.Folder{}).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Errorf("removed = %v, want the two vanished rows", removed)
	}
	if after != before-int64(len(removed)) {
		t.Errorf("%d rows before, %d reported removed, %d left; the table and the audit list disagree",
			before, len(removed), after)
	}
	if !rowExists(t, gdb, keep) {
		t.Error("the live file's row was deleted")
	}
}

// Cancelling the context stops the pass. A prune that ignored it would keep
// deleting after the task was told to stop.
func TestPruneVanished_StopsOnCancelledContext(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)
	addRow(t, gdb, filepath.Join(root, "a.ogg"), "music")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	if removed := h.pruneVanished(ctx, nil); len(removed) != 0 {
		t.Errorf("removed = %v, want nothing once the context was already cancelled", removed)
	}
	if !rowExists(t, gdb, filepath.Join(root, "a.ogg")) {
		t.Error("a row was deleted after cancellation")
	}
}

// The on-disk file is what decides. Recreating a file at a vanished row's
// path brings the row back into scope as a live one, which is the behaviour
// that makes stat-per-row safe to run against a live library.
func TestPruneVanished_KeepsARowWhoseFileReappears(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	back := filepath.Join(root, "back.ogg")
	addRow(t, gdb, back, "music")
	mustWriteNested(t, back, "recreated before the prune ran")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removed := h.pruneVanished(context.Background(), nil)

	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing: the file is present again", removed)
	}
	if !rowExists(t, gdb, back) {
		t.Error("the row was deleted even though its file exists")
	}
}

// The two halves of the task stay independent: clearing empty directories
// must not touch index rows, or a stray empty dir would cost the user their
// whole index.
func TestPruneEmpty_AloneLeavesRowsAlone(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	gone := filepath.Join(root, "gone.ogg")
	addRow(t, gdb, gone, "music")

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	if _, err := h.pruneEmpty(context.Background(), nil); err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	if !rowExists(t, gdb, gone) {
		t.Error("pruneEmpty deleted an index row; it only removes directories")
	}
}

// A real directory tree, scanned and then emptied behind the app's back —
// the scenario the two halves exist for, in the order a user hits it.
func TestPruneEmptyThenVanished_AfterFilesDisappear(t *testing.T) {
	root := t.TempDir()
	gdb := newScanDB(t)

	album := filepath.Join(root, "Artist", "Album")
	trackRow := filepath.Join(album, "track.ogg")
	mustWriteNested(t, trackRow, "audio")
	addRow(t, gdb, trackRow, "music")
	addRow(t, gdb, album, "folder")

	// The file leaves; the directory it emptied stays behind.
	if err := os.Remove(trackRow); err != nil {
		t.Fatal(err)
	}

	h := &PruneEmptyFoldersHandler{DB: gdb, MusicRoot: root}
	removedDirs, err := h.pruneEmpty(context.Background(), nil)
	if err != nil {
		t.Fatalf("pruneEmpty: %v", err)
	}
	removedRows := h.pruneVanished(context.Background(), nil)

	if len(removedDirs) == 0 {
		t.Error("the emptied album directory should have been removed")
	}
	if !equalStrings(removedRows, []string{"Artist/Album/track.ogg"}) {
		t.Errorf("removedRows = %v, want [Artist/Album/track.ogg]", removedRows)
	}
	if rowExists(t, gdb, trackRow) {
		t.Error("the row for the removed track survived")
	}
}
