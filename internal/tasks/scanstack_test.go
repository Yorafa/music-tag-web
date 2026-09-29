package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"go-music-tag/internal/db"
)

// sub_paths arrives in a request body and can become the walk root
// verbatim, in all three handlers that take it. The prunes' half is fixed
// because it deletes; these two only write index rows, so the gap is
// recorded here rather than fixed. The harm is real either way:
// a scanned directory becomes a db.Folder row, and rows are what the
// duplicate-detection candidate queries and the stream handler read.
//
// These tests go through the handlers rather than through scanStack, because
// the bug lived in the handlers' use of the payload and a test of the helper
// alone would not have seen it. That distinction already cost one round of
// mutation testing on the index enqueue path.

// everyRowIn is a blunt instrument on purpose: the claim is "the outside
// directory left no trace at all", so counting everything is stronger than
// checking for one expected row.
func everyRowIn(t *testing.T, gdb *gorm.DB, prefix string) []string {
	t.Helper()
	var paths []string
	if err := gdb.Model(&db.Folder{}).Where("path LIKE ?", prefix+"%").Pluck("path", &paths).Error; err != nil {
		t.Fatalf("read rows under %s: %v", prefix, err)
	}
	return paths
}

func scanFixture(t *testing.T) (music string, outside string) {
	t.Helper()
	base := t.TempDir()
	music = filepath.Join(base, "music")
	outside = filepath.Join(base, "secrets")
	mustMkdir(t, filepath.Join(music, "Artist"))
	mustMkdir(t, filepath.Join(outside, "private"))
	mustWrite(t, filepath.Join(music, "Artist", "track.mp3"), "x")
	mustWrite(t, filepath.Join(outside, "private", "leak.mp3"), "x")
	mustWrite(t, filepath.Join(outside, "loose.txt"), "x")
	return music, outside
}

// The feature: a full scan pointed at a directory outside the library writes
// nothing.
func TestFullScan_IgnoresOutOfRootScope(t *testing.T) {
	music, outside := scanFixture(t)
	gdb := newScanDB(t)

	h := &FullScanHandler{DB: gdb, MusicRoot: music}
	if err := h.fullScan(context.Background(), [][2]string{{"", outside}}); err != nil {
		t.Fatalf("fullScan: %v", err)
	}
	if rows := everyRowIn(t, gdb, outside); len(rows) != 0 {
		t.Errorf("ESCAPED: a full scan of %s wrote %v", outside, rows)
	}
	var total int64
	gdb.Model(&db.Folder{}).Count(&total)
	if total != 0 {
		t.Errorf("%d rows written for a refused scope, want 0", total)
	}
}

// Same for the incremental scan, which is the one the UI actually calls.
func TestUpdateScan_IgnoresOutOfRootScope(t *testing.T) {
	music, outside := scanFixture(t)
	gdb := newScanDB(t)

	h := &UpdateScanHandler{DB: gdb, MusicRoot: music}
	if err := h.updateScan(context.Background(), [][2]string{{"", outside}}); err != nil {
		t.Fatalf("updateScan: %v", err)
	}
	if rows := everyRowIn(t, gdb, outside); len(rows) != 0 {
		t.Errorf("ESCAPED: an update scan of %s wrote %v", outside, rows)
	}
	var total int64
	gdb.Model(&db.Folder{}).Count(&total)
	if total != 0 {
		t.Errorf("%d rows written for a refused scope, want 0", total)
	}
}

// Containment filters scopes; it does not discard the request. If a payload
// mixes a legitimate subdirectory with an escape, the legitimate half must
// still be scanned — otherwise the fix would be "ignore sub_paths", which
// would break the tidy flow that passes real subdirectories.
func TestScan_MixedPayloadStillScansTheLegitimateHalf(t *testing.T) {
	music, outside := scanFixture(t)
	inScope := filepath.Join(music, "Artist")
	gdb := newScanDB(t)

	h := &FullScanHandler{DB: gdb, MusicRoot: music}
	err := h.fullScan(context.Background(), [][2]string{
		{"", outside},
		{"", inScope},
	})
	if err != nil {
		t.Fatalf("fullScan: %v", err)
	}
	if rows := everyRowIn(t, gdb, outside); len(rows) != 0 {
		t.Errorf("ESCAPED: wrote %v", rows)
	}
	rows := everyRowIn(t, gdb, inScope)
	if len(rows) != 2 { // the directory and its one track
		t.Errorf("in-scope rows = %v, want the directory and track.mp3", rows)
	}
}

// An absent payload still means the whole library, which is what the UI
// sends. Scoping the check must not turn "no sub_paths" into "nothing".
func TestScan_NoPayloadStillScansTheLibrary(t *testing.T) {
	music, _ := scanFixture(t)
	gdb := newScanDB(t)

	if err := (&FullScanHandler{DB: gdb, MusicRoot: music}).fullScan(context.Background(), nil); err != nil {
		t.Fatalf("fullScan: %v", err)
	}
	rows := everyRowIn(t, gdb, music)
	if len(rows) != 3 { // Artist, track.mp3, and the library root itself
		t.Errorf("rows = %v, want the whole library", rows)
	}
}

// A path that shares a prefix with the root is a different directory:
// /music-backup is not under /music. SafeAbs resolves and compares, so this
// holds; the test is here because the naive check is a string prefix test and
// the naive check is what a future edit would reach for.
func TestScanStack_RootPrefixAloneIsNotContainment(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	sibling := filepath.Join(base, "music-backup")
	mustMkdir(t, sibling)
	if _, err := os.Stat(sibling); err != nil {
		t.Fatal(err)
	}

	if stack := scanStack(music, [][2]string{{"", sibling}}); len(stack) != 0 {
		t.Errorf("scanStack accepted %s under root %s: %v", sibling, music, stack)
	}
	// And the same directory genuinely is accepted under the right root, so
	// the test above is not passing because everything is refused.
	if stack := scanStack(base, [][2]string{{"", sibling}}); len(stack) != 1 {
		t.Errorf("scanStack refused a genuinely in-root path: %v", stack)
	}
}

// The parent uid travels with the path and is not validated. With the path
// contained it can only mis-parent rows inside the library, so the uid is
// passed through unchanged rather than being second-guessed here.
func TestScanStack_KeepsTheParentUID(t *testing.T) {
	music := t.TempDir()
	stack := scanStack(music, [][2]string{{"parent-uid-1", filepath.Join(music, "sub")}})
	if len(stack) != 1 {
		t.Fatalf("stack = %v, want one entry", stack)
	}
	if stack[0][0] != "parent-uid-1" {
		t.Errorf("parent uid = %q, want it passed through unchanged", stack[0][0])
	}
}
