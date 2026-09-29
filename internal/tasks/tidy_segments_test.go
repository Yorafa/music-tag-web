// The tests for 整理目录's variable-depth, free-text directory levels.
//
// What is being pinned here is the thing the old two-field payload could
// not express at all: a level is a TEMPLATE, the depth is however many
// levels the operator asked for, and the two compose. The rest of the file
// is about the preview agreeing with the move, which is the only reason the
// preview is worth having.

package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
)

// taggedTrack writes a real MP3 carrying the given tags and returns its
// path. Tests that only exercise the path derivation could build a
// TagInfo by hand, but the point of several of these is that the derivation
// reads the same tags the move will read, so they go through the real
// reader.
func taggedTrack(t *testing.T, dir, name string, up tag.TagUpdate) string {
	t.Helper()
	p := testaudio.SeedMP3(t, dir, name)
	if err := tag.Write(p, &up); err != nil {
		t.Fatalf("seed tags on %s: %v", name, err)
	}
	return p
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	return fi
}

// A level may mix tag values with fixed text. "${year} - ${album}" is ONE
// directory, which is the case the old first_dir/second_dir pair had no
// way to express: it could make a level called `album` or a level called
// `year`, never a level called "2003 - 叶惠美".
func TestTidyDestPath_LevelMixesTagsAndFixedText(t *testing.T) {
	info := &tag.TagInfo{Year: 2003, Album: "叶惠美", Artist: "周杰伦", Title: "晴天"}

	dst, missing, err := tidyDestPath("/lib", []string{"${year} - ${album}"}, info, "01.mp3")
	if err != nil {
		t.Fatalf("tidyDestPath: %v", err)
	}
	if want := filepath.Join("/lib", "2003 - 叶惠美", "01.mp3"); dst != want {
		t.Errorf("dst = %q, want %q", dst, want)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none for a fully-tagged track", missing)
	}
}

// The depth is the length of the list, in both directions. Two was the
// hard maximum before; three was unrepresentable, and so was one.
func TestTidyDestPath_DepthIsTheNumberOfLevels(t *testing.T) {
	info := &tag.TagInfo{
		Artist: "周杰伦", Album: "叶惠美", Genre: "流行",
		Year: 2003, DiscNumber: "1",
	}

	for _, tc := range []struct {
		name     string
		segments []string
		want     string
	}{
		{
			name:     "one level flattens everything into one directory",
			segments: []string{"${artist}"},
			want:     filepath.Join("/lib", "周杰伦", "01.mp3"),
		},
		{
			name:     "the classic two",
			segments: []string{"${artist}", "${album}"},
			want:     filepath.Join("/lib", "周杰伦", "叶惠美", "01.mp3"),
		},
		{
			name:     "three levels, the shape the old payload could not express",
			segments: []string{"${artist}", "${album}", "${discnumber}"},
			want:     filepath.Join("/lib", "周杰伦", "叶惠美", "1", "01.mp3"),
		},
		{
			name:     "four, for the disc-flattened-by-genre libraries",
			segments: []string{"${genre}", "${artist}", "${year} - ${album}", "${discnumber}"},
			want:     filepath.Join("/lib", "流行", "周杰伦", "2003 - 叶惠美", "1", "01.mp3"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := tidyDestPath("/lib", tc.segments, info, "01.mp3")
			if err != nil {
				t.Fatalf("tidyDestPath: %v", err)
			}
			if got != tc.want {
				t.Errorf("dst = %q, want %q", got, tc.want)
			}
		})
	}
}

// A level with no placeholder at all is a fixed grouping — "Live",
// "Various Artists", "未整理". utils.TemplateFieldError forbids this for
// filenames (every file would get the same literal name) and it is
// tempting to reuse it here, but that rule is about a collision between
// FILES, and a directory level has no such collision. The two dialogs this
// one is meant to sit beside both allow arbitrary fixed text, and a user
// who cannot write a literal level name cannot build the tree they have in
// their head.
func TestTidyDestPath_AllowsAFixedLevel(t *testing.T) {
	info := &tag.TagInfo{Artist: "Boards of Canada", Album: "Geogaddi"}

	got, _, err := tidyDestPath("/lib", []string{"电子", "${artist}", "${album}"}, info, "a.mp3")
	if err != nil {
		t.Fatalf("a literal level was refused: %v", err)
	}
	if want := filepath.Join("/lib", "电子", "Boards of Canada", "Geogaddi", "a.mp3"); got != want {
		t.Errorf("dst = %q, want %q", got, want)
	}
}

// An empty level list is not "flat into the root", it is a malformed
// request. Left unguarded, a zero-length list would put every file
// directly in root_path and quietly flatten the library.
func TestTidyDestPath_RefusesNoLevels(t *testing.T) {
	if _, _, err := tidyDestPath("/lib", nil, &tag.TagInfo{}, "a.mp3"); err == nil {
		t.Fatal("no levels was accepted; every file would land in the root")
	}
}

func TestTidyDestPath_RefusesAnEmptyLevel(t *testing.T) {
	_, _, err := tidyDestPath("/lib", []string{"${artist}", "  "}, &tag.TagInfo{Artist: "A"}, "a.mp3")
	if err == nil {
		t.Fatal("an empty level was accepted")
	}
}

// The field vocabulary is the one the other two template dialogs use, and
// it is a superset of what tidy used to support. tracknumber/discnumber/
// title were missing, and "音轨 - 艺术家 - 专辑" is a structure people
// explicitly ask for.
func TestTidyVars_CoversTheRenameFieldSet(t *testing.T) {
	info := &tag.TagInfo{
		Title: "T", Artist: "A", Album: "B", AlbumArtist: "AA",
		Genre: "G", Year: 1999, TrackNumber: "3/12", DiscNumber: "1/2",
	}
	v := tidyVars(info)
	for _, key := range []string{
		"title", "artist", "album", "albumartist",
		"genre", "year", "tracknumber", "discnumber",
	} {
		if v[key] == "" {
			t.Errorf("field %q resolved to nothing for a fully-tagged file", key)
		}
	}
	if v["year"] != "1999" {
		t.Errorf("year = %q, want the integer rendered as a string", v["year"])
	}
}

// A year of 0 means "no year tag", and must render as empty rather than
// "0" — a directory called "0 - 叶惠美" is worse than one that says the
// year is missing.
func TestTidyVars_AbsentYearIsEmptyNotZero(t *testing.T) {
	if got := tidyVars(&tag.TagInfo{Album: "X"})["year"]; got != "" {
		t.Errorf("year = %q, want empty for a file with no year tag", got)
	}
}

// A missing tag is NOT an error. The file still moves; the directory just
// has a gap, and `missing` is what lets the dialog say so out loud. Only
// a level that renders to nothing AT ALL falls back to 未知.
func TestTidyDestPath_MissingTagIsReportedButDoesNotBlock(t *testing.T) {
	info := &tag.TagInfo{Artist: "A", Album: "B"} // no year

	dst, missing, err := tidyDestPath("/lib", []string{"${year} - ${album}"}, info, "a.mp3")
	if err != nil {
		t.Fatalf("a missing year refused the move: %v", err)
	}
	if len(missing) != 1 || missing[0] != "year" {
		t.Errorf("missing = %v, want [year]", missing)
	}
	// The leading " - " survives as "- B": the segment is trimmed, but the
	// separator the operator typed between a missing year and the album
	// stays. That gap is the whole reason `missing` is reported — a
	// directory called "- B" is a thing only the operator can have meant.
	if want := filepath.Join("/lib", "- B", "a.mp3"); dst != want {
		t.Errorf("dst = %q, want %q", dst, want)
	}
}

func TestTidyDestPath_LevelThatRendersToNothingFallsBackToUnknown(t *testing.T) {
	info := &tag.TagInfo{Album: "B"} // no year, and the level is ONLY ${year}

	dst, _, err := tidyDestPath("/lib", []string{"${year}", "${album}"}, info, "a.mp3")
	if err != nil {
		t.Fatalf("tidyDestPath: %v", err)
	}
	if want := filepath.Join("/lib", unknownDir, "B", "a.mp3"); dst != want {
		t.Errorf("dst = %q, want the %q fallback at %q", dst, unknownDir, want)
	}
}

// unknown is reported once even when the same field is asked for twice, and
// the dialog should not have to dedupe it.
func TestTidyDestPath_ReportsEachMissingFieldOnce(t *testing.T) {
	info := &tag.TagInfo{Album: "B"}
	_, missing, err := tidyDestPath("/lib", []string{"${year}", "${year} - ${album}"}, info, "a.mp3")
	if err != nil {
		t.Fatalf("tidyDestPath: %v", err)
	}
	if len(missing) != 1 || missing[0] != "year" {
		t.Errorf("missing = %v, want [year] exactly once", missing)
	}
}

// The safety property the old code earned by hand and this rewrite has to
// keep: a tag value is attacker-controlled (UpdateID3 writes whatever it
// is given) and a segment carrying a separator or a `..` must not become
// a path that walks out of the root.
func TestTidyDestPath_KeepsATagValueInsideTheRoot(t *testing.T) {
	info := &tag.TagInfo{Album: "../../etc", Artist: "A"}

	if _, _, err := tidyDestPath("/lib", []string{"${album}"}, info, "a.mp3"); err == nil {
		t.Fatal("a traversing album tag was accepted as a directory name")
	}
}

// An unknown key is refused rather than rendered. The permissive renderer
// (utils.RenderTemplate) would have produced a directory literally named
// "${albmu}".
func TestTidyDestPath_RefusesAnUnknownField(t *testing.T) {
	if _, _, err := tidyDestPath("/lib", []string{"${albmu}"}, &tag.TagInfo{}, "a.mp3"); err == nil {
		t.Fatal("a misspelled field was accepted")
	}
}

// --- The preview ---

// The core promise: the plan the operator reads is computed by the same
// function the move uses. If these two ever disagree, the preview is worse
// than no preview — it is a confident lie.
func TestPreviewTidy_PlansWhatTidyOneActuallyDoes(t *testing.T) {
	root := t.TempDir()
	src := taggedTrack(t, root, "song.mp3", tag.TagUpdate{
		Title:  strptr("Song"),
		Album:  strptr("MyAlbum"),
		Artist: []string{"MyArtist"},
	})
	segments := []string{"${artist}", "${year} - ${album}"}

	h := &TidyFolderHandler{MusicRoot: root}
	rows, err := h.PreviewTidy(context.Background(), []string{src}, root, segments)
	if err != nil {
		t.Fatalf("PreviewTidy: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Status != TidyPlanMove {
		t.Fatalf("status = %q (%s), want %q", rows[0].Status, rows[0].Reason, TidyPlanMove)
	}
	planned := rows[0].NewPath
	// The fixture has no year, so the plan must name the gap rather than
	// quietly inventing one.
	if len(rows[0].Missing) != 1 || rows[0].Missing[0] != "year" {
		t.Errorf("missing = %v, want [year]", rows[0].Missing)
	}

	// Now actually run it. The assertion is that the file is AT the
	// planned path — not against a path spelled out a second time here,
	// which would only test that the test agrees with itself.
	if err := h.tidyOne(context.Background(), src, TidyFolderPayload{
		RootPath: root, Segments: segments,
	}); err != nil {
		t.Fatalf("tidyOne: %v", err)
	}
	mustStat(t, planned)
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("the file is still at its old path %s; the plan and the move disagree", src)
	}
}

// The collision the operator cannot see by reading paths: the same FILENAME
// sitting in two different source directories, both carrying tags that
// resolve to one destination. Only the second is a duplicate.
//
// The same-filename part is the point. Two tracks in one album are NOT a
// collision — tidy keeps the basename, so a.mp3 and b.mp3 go to different
// files in the same directory and both are fine. Flagging those would put
// a false "目标冲突" on every album with more than one track.
func TestPreviewTidy_MarksTheSecondFileForOneDestination(t *testing.T) {
	root := t.TempDir()
	tags := tag.TagUpdate{Album: strptr("X"), Artist: []string{"A"}, Title: strptr("T")}
	a := taggedTrack(t, filepath.Join(root, "One"), "song.mp3", tags)
	b := taggedTrack(t, filepath.Join(root, "Two"), "song.mp3", tags)

	h := &TidyFolderHandler{MusicRoot: root}
	rows, err := h.PreviewTidy(context.Background(), []string{a, b}, root, []string{"${artist}", "${album}"})
	if err != nil {
		t.Fatalf("PreviewTidy: %v", err)
	}
	if rows[0].NewPath != rows[1].NewPath {
		t.Fatalf("fixture is wrong: the two rows want different paths (%q, %q)", rows[0].NewPath, rows[1].NewPath)
	}
	tally := TallyTidy(rows)
	if tally.Duplicate != 1 {
		t.Errorf("duplicate = %d, want 1", tally.Duplicate)
	}
	if tally.Move != 1 {
		t.Errorf("move = %d, want 1", tally.Move)
	}
	if got := MovablePaths(rows); len(got) != 1 {
		t.Errorf("MovablePaths = %v, want only the winner", got)
	}
}

// ...and the converse: two tracks in one album are the normal case, and
// flagging them would put a false conflict on every multi-track release.
func TestPreviewTidy_TwoTracksInOneAlbumAreNotACollision(t *testing.T) {
	root := t.TempDir()
	tags := tag.TagUpdate{Album: strptr("X"), Artist: []string{"A"}}
	a := taggedTrack(t, root, "a.mp3", tags)
	b := taggedTrack(t, root, "b.mp3", tags)

	h := &TidyFolderHandler{MusicRoot: root}
	rows, err := h.PreviewTidy(context.Background(), []string{a, b}, root, []string{"${artist}", "${album}"})
	if err != nil {
		t.Fatalf("PreviewTidy: %v", err)
	}
	tally := TallyTidy(rows)
	if tally.Duplicate != 0 || tally.Move != 2 {
		t.Errorf("tally = %+v, want 2 moves and no conflicts", tally)
	}
}

// A file already in place is `same`, not `move` and not an error. It is
// reported in its own bucket so "412 will be reorganised" and "412 are
// already correct" never get added into one number.
func TestPreviewTidy_AFreshTreeIsAlreadyInPlace(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "A", "B")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := taggedTrack(t, dir, "a.mp3", tag.TagUpdate{Album: strptr("B"), Artist: []string{"A"}})

	h := &TidyFolderHandler{MusicRoot: root}
	rows, err := h.PreviewTidy(context.Background(), []string{src}, root, []string{"${artist}", "${album}"})
	if err != nil {
		t.Fatalf("PreviewTidy: %v", err)
	}
	if rows[0].Status != TidyPlanSame {
		t.Errorf("status = %q (%s), want %q", rows[0].Status, rows[0].Reason, TidyPlanSame)
	}
}

// The collision that loses data. Reported rather than discovered at
// os.Rename, because by then the operator is reading a failure log for
// something the plan could have told them before they clicked.
func TestPreviewTidy_MarksATakenDestination(t *testing.T) {
	root := t.TempDir()
	// The destination is already occupied by an unrelated file.
	if err := os.MkdirAll(filepath.Join(root, "A", "B"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "A", "B", "a.mp3"), []byte("someone else"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := taggedTrack(t, filepath.Join(root, "Loose"), "a.mp3", tag.TagUpdate{
		Album: strptr("B"), Artist: []string{"A"},
	})

	h := &TidyFolderHandler{MusicRoot: root}
	rows, err := h.PreviewTidy(context.Background(), []string{src}, root, []string{"${artist}", "${album}"})
	if err != nil {
		t.Fatalf("PreviewTidy: %v", err)
	}
	if rows[0].Status != TidyPlanTaken {
		t.Errorf("status = %q, want %q — os.Rename would have failed on this", rows[0].Status, TidyPlanTaken)
	}
}

// A directory already at the destination is the NORMAL case — the tidy is
// building the tree — and must not be reported as a collision. This is the
// case that separates `taken` from "any os.Lstat succeeded".
func TestPreviewTidy_AnExistingDirectoryIsNotACollision(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "A", "B"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := taggedTrack(t, filepath.Join(root, "Loose"), "a.mp3", tag.TagUpdate{
		Album: strptr("B"), Artist: []string{"A"},
	})

	h := &TidyFolderHandler{MusicRoot: root}
	rows, err := h.PreviewTidy(context.Background(), []string{src}, root, []string{"${artist}", "${album}"})
	if err != nil {
		t.Fatalf("PreviewTidy: %v", err)
	}
	if rows[0].Status != TidyPlanMove {
		t.Errorf("status = %q (%s), want %q", rows[0].Status, rows[0].Reason, TidyPlanMove)
	}
}

// A file outside the library is blocked per-file, not per-batch. One bad
// row in a 500-track list should cost that row.
func TestPreviewTidy_BlocksAnOutOfLibraryPathWithoutLosingTheRest(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	good := taggedTrack(t, root, "a.mp3", tag.TagUpdate{Album: strptr("B"), Artist: []string{"A"}})
	bad := filepath.Join(outside, "stranger.mp3")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &TidyFolderHandler{MusicRoot: root}
	rows, err := h.PreviewTidy(context.Background(), []string{good, bad}, root, []string{"${artist}"})
	if err != nil {
		t.Fatalf("PreviewTidy: %v", err)
	}
	tally := TallyTidy(rows)
	if tally.Blocked != 1 || tally.Move != 1 {
		t.Errorf("tally = %+v, want 1 blocked and 1 move", tally)
	}
}

// A root outside the library is refused by the preview too, not just by
// the worker. The dialog is where the user finds out.
func TestPreviewTidy_RefusesAnOutOfLibraryRoot(t *testing.T) {
	root := t.TempDir()
	src := taggedTrack(t, root, "a.mp3", tag.TagUpdate{Album: strptr("B")})

	h := &TidyFolderHandler{MusicRoot: root}
	if _, err := h.PreviewTidy(context.Background(), []string{src}, t.TempDir(), []string{"${album}"}); err == nil {
		t.Fatal("a preview accepted a root outside the library")
	}
}

// MovablePaths is what the gateway enqueues, so it has to be exactly the
// set the plan approved — including `same`, which the tidy will run and
// find to be a no-op (that is how the worker learns to skip the move).
func TestMovablePaths_ExcludesEverythingThePlanRefused(t *testing.T) {
	rows := []TidyPlanRow{
		{OldPath: "/a", Status: TidyPlanMove},
		{OldPath: "/b", Status: TidyPlanSame},
		{OldPath: "/c", Status: TidyPlanTaken},
		{OldPath: "/d", Status: TidyPlanDuplicate},
		{OldPath: "/e", Status: TidyPlanBlocked},
	}
	got := MovablePaths(rows)
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Errorf("MovablePaths = %v, want [/a /b]", got)
	}
}

// TallyTally is what both the notice and the apply gate read, so a row
// with a missing tag has to be counted as a move AND as a gap. Folding
// gaps into a separate "skipped" bucket would understate what moved.
func TestTallyTidy_CountsGapsSeparatelyFromMoves(t *testing.T) {
	tally := TallyTidy([]TidyPlanRow{
		{Status: TidyPlanMove},
		{Status: TidyPlanMove, Missing: []string{"year"}},
		{Status: TidyPlanSame},
		{Status: TidyPlanTaken},
	})
	if tally.Move != 2 {
		t.Errorf("Move = %d, want 2", tally.Move)
	}
	if tally.WithGaps != 1 {
		t.Errorf("WithGaps = %d, want 1", tally.WithGaps)
	}
	if tally.Total != 4 {
		t.Errorf("Total = %d, want 4", tally.Total)
	}
}

// The request-level validation the gateway runs before enqueueing. It is
// the same list the worker enforces, shared rather than reimplemented, so
// the dialog and the worker cannot disagree about what a level list is.
func TestTidySegmentsProblem(t *testing.T) {
	for _, tc := range []struct {
		name     string
		segments []string
		wantBad  bool
	}{
		{"the classic two", []string{"${artist}", "${album}"}, false},
		{"a literal level is fine", []string{"Live", "${artist}"}, false},
		{"three deep is fine", []string{"${genre}", "${artist}", "${album}"}, false},
		{"no levels at all", nil, true},
		{"an empty level", []string{"${artist}", " "}, true},
		{"a misspelled field", []string{"${albmu}"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := TidySegmentsProblem(tc.segments)
			if (got != "") != tc.wantBad {
				t.Errorf("TidySegmentsProblem(%v) = %q, wantBad %v", tc.segments, got, tc.wantBad)
			}
		})
	}
}

// A batch with an unknown field is refused whole, before anything moves.
// The old per-file path would have reported the same typo 500 times and
// half-run the batch before the first real refusal.
func TestTidy_RejectsABadTemplateBeforeMovingAnything(t *testing.T) {
	music, track := tidyFixture(t)

	err := tidyTask(t, music, TidyFolderPayload{
		MusicPaths: []string{track},
		RootPath:   music,
		Segments:   []string{"${artist}", "${albmu}"},
	})
	if err == nil {
		t.Fatal("ProcessTask accepted a misspelled field")
	}
	if _, sErr := os.Stat(track); sErr != nil {
		t.Errorf("the track moved despite the refused payload: %v", sErr)
	}
}
