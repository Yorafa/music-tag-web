// The dry run for 整理目录: what the tidy WOULD do, per file, without
// moving anything.
//
// It exists because tidy is the most destructive thing in the toolbar.
// 解析文件名 writes tags and 从标签改名 renames one file at a time, both
// with a per-file outcome the dialog can show. A tidy moves the file AND
// the album cover AND the .lrc, rebuilds the directory tree, and is
// applied to the whole list at once — "确认整理" on a 500-row batch used
// to mean trusting a form with three text boxes and no output.
//
// The rule that makes this trustworthy rather than decorative: it calls
// the same tidyDestPath the worker calls. A preview computed any other way
// is a second thing that can disagree with the first, and the whole point
// of asking someone to read a plan is that the plan is the thing that will
// happen.
//
// Read-only, so the gateway answers it inline rather than round-tripping
// through the worker queue the way the mutation does.
//
// # Who calls this now
//
// The app does not. 整理目录 renders its plan in the browser, from the
// rows' cached tags (frontend/src/components/workstation/localPreview.ts),
// because the preview was a request on a rule the dialog invites you to
// change and the answer was only ever ten rows of it.
//
// That makes the code below the REFERENCE the local plan mirrors, and its
// tests the thing that keeps the two honest. The mirror cannot check
// whether a destination is occupied — that needs a stat of the target
// path — so it is the one verdict it does not report, and the one the
// operator is left to check. The rules below (sanitising, containment,
// what counts as a collision) are the ones a change here has to keep
// agreeing with.

package tasks

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"go-music-tag/internal/tag"
	"go-music-tag/internal/utils"
)

// TidyPlan statuses. The first two mean the file moves; the rest mean it
// does not, and each says why in the row.
const (
	// TidyPlanMove — this is where the file will land.
	TidyPlanMove = "move"
	// TidyPlanSame — the file is already at that path. Not an error, and
	// reported separately from TidyPlanMove precisely so "412 will move"
	// and "412 are already correct" never get added into one number.
	TidyPlanSame = "same"
	// TidyPlanTaken — something is already at the destination. The move
	// would fail, and silently overwriting a real file is not on the menu.
	TidyPlanTaken = "taken"
	// TidyPlanDuplicate — two files in this batch want the same path. Only
	// the first would win; the rest would fail at os.Rename with a much
	// less useful message.
	TidyPlanDuplicate = "duplicate"
	// TidyPlanBlocked — this file cannot be planned: unreadable, outside
	// the library, or a template that renders to nothing usable.
	TidyPlanBlocked = "blocked"
)

// TidyPlanRow is one file's verdict. OldPath/NewPath are absolute, because
// the operator is checking them against a filesystem and a relative path
// would make them compare against a different thing per row.
type TidyPlanRow struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
	Status  string `json:"status"`
	// Reason is set for taken/duplicate/blocked, and carries the field
	// names (not a prose sentence) for a row with gaps, so the dialog can
	// render it however it likes.
	Reason string `json:"reason,omitempty"`
	// Missing lists the tags this file did not have that the templates
	// asked for. The file still moves — the directory just has a hole in
	// it — so this is a warning, not a block.
	Missing []string `json:"missing,omitempty"`
}

// PreviewTidy answers "where would 整理目录 put these?" without moving
// anything. It is what the confirmation dialog renders.
//
// `rootPath` is resolved through the same TidyRoot the worker uses, so a
// root outside the library is refused here with the worker's own message
// rather than being quietly accepted by a preview and refused by the move.
func (h *TidyFolderHandler) PreviewTidy(
	ctx context.Context, musicPaths []string, rootPath string, segments []string,
) ([]TidyPlanRow, error) {
	root, err := h.tidyRoot(rootPath)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("没有指定任何目录层级")
	}
	rows := make([]TidyPlanRow, 0, len(musicPaths))
	// claimed counts destinations already spoken for by an EARLIER row in
	// this batch, so the second file to want a path is marked duplicate
	// rather than both being reported as fine. Reset per call: a preview
	// describes this batch, not the last one.
	claimed := map[string]int{}

	for _, p := range musicPaths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		src, err := h.tidySource(p)
		if err != nil {
			rows = append(rows, TidyPlanRow{OldPath: p, Status: TidyPlanBlocked, Reason: err.Error()})
			continue
		}
		rows = append(rows, h.planOne(src, root, segments, claimed))
	}
	return rows, nil
}

// planOne is the per-file half. Split out of PreviewTidy so the collision
// bookkeeping lives with the derivation rather than in the loop that feeds
// it.
func (h *TidyFolderHandler) planOne(src, root string, segments []string, claimed map[string]int) TidyPlanRow {
	row := TidyPlanRow{OldPath: src}
	info, err := tag.Read(src)
	if err != nil {
		row.Status = TidyPlanBlocked
		row.Reason = fmt.Sprintf("读不出标签：%v", err)
		return row
	}
	dst, missing, err := tidyDestPath(root, segments, info, filepath.Base(src))
	if err != nil {
		row.Status = TidyPlanBlocked
		row.Reason = err.Error()
		return row
	}
	row.NewPath = dst
	row.Missing = missing

	// Order matters. `same` is checked first because it is the only status
	// that is not a problem, and a file already in place that happens to
	// collide with a LATER file's destination is still `same` — it is
	// where it already is, and it is not going anywhere.
	if dst == src {
		row.Status = TidyPlanSame
		claimed[dst]++
		return row
	}
	if claimed[dst] > 0 {
		row.Status = TidyPlanDuplicate
		row.Reason = "本批次里已有别的文件要放到这里"
		claimed[dst]++
		return row
	}
	claimed[dst]++

	// The destination existing is the collision that actually loses data,
	// and it is the one the operator cannot see by reading paths: two rows
	// that look unrelated in the old tree can both resolve onto one name.
	//
	// A directory there is not a collision — that is the normal case, the
	// tidy is building the tree. Only a non-directory is.
	if fi, err := os.Lstat(dst); err == nil && !fi.IsDir() {
		row.Status = TidyPlanTaken
		row.Reason = "目标位置已有同名文件"
		return row
	} else if err != nil && !os.IsNotExist(err) {
		// A permission problem or a stalled mount is "I could not tell",
		// not "it is free". Reported rather than treated as a clean
		// destination, for the same reason pruneVanished keeps its rows.
		row.Status = TidyPlanBlocked
		row.Reason = fmt.Sprintf("无法检查目标位置：%v", err)
		return row
	}
	row.Status = TidyPlanMove
	return row
}

// TidyTally is the summary line's input. Counted here rather than in the
// dialog so the numbers that go in the notice and the numbers that gate the
// apply button are the same ones.
type TidyTally struct {
	Total     int
	Move      int
	Same      int
	Taken     int
	Duplicate int
	Blocked   int
	// WithGaps counts rows that WILL move but whose templates asked for a
	// tag the file does not have. Separate from Move because those files
	// did get reorganised, into a directory with a hole in its name, and
	// "412 整理完成" hides that.
	WithGaps int
}

// TallyTidy folds a plan into counts.
func TallyTidy(rows []TidyPlanRow) TidyTally {
	var t TidyTally
	t.Total = len(rows)
	for _, r := range rows {
		switch r.Status {
		case TidyPlanMove:
			t.Move++
			if len(r.Missing) > 0 {
				t.WithGaps++
			}
		case TidyPlanSame:
			t.Same++
		case TidyPlanTaken:
			t.Taken++
		case TidyPlanDuplicate:
			t.Duplicate++
		case TidyPlanBlocked:
			t.Blocked++
		}
	}
	return t
}

// MovablePaths returns just the files the tidy can actually act on.
//
// The dialog sends only these when it applies. A preview row that says
// `taken` and a move that then fails at os.Rename are the same event
// arriving twice — once as a row the operator read and accepted, once as a
// failure they now have to diagnose from a log. Sending only what the plan
// approved keeps the audit log and the plan in agreement.
//
// Paths come back absolute because that is the form the worker's tidySource
// resolves most directly, and the same strings the plan table displayed.
func MovablePaths(rows []TidyPlanRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Status == TidyPlanMove || r.Status == TidyPlanSame {
			out = append(out, r.OldPath)
		}
	}
	return out
}

// TidySegmentsProblem describes a level list the worker would refuse, in
// the words the dialog shows. Exported so the dialog's own validation and
// the worker's agree by construction rather than by review.
//
// It is deliberately weaker than the per-file rules: a level that renders
// empty for one file is a plan-row fact, not a request-level one.
func TidySegmentsProblem(segments []string) string {
	if len(segments) == 0 {
		return "至少需要一层目录"
	}
	for i, seg := range segments {
		if strings.TrimSpace(seg) == "" {
			return fmt.Sprintf("第 %d 层是空的", i+1)
		}
		if _, err := utils.ExpandFilenameTemplate(seg, map[string]string{}); err != nil {
			return fmt.Sprintf("第 %d 层：%v", i+1, err)
		}
	}
	return ""
}

// LogTidyPlan is the one-line trace a preview leaves in the gateway log.
// A preview is a user-initiated read that can be large, and a line
// appearing in the log is how an operator correlates a surprise in the
// plan with the request that produced it.
func LogTidyPlan(root string, segments []string, t TidyTally) {
	log.Printf("[tidy] plan for %s (%s): %d row(s), %d move, %d already in place, %d blocked, %d taken, %d duplicate",
		root, strings.Join(segments, " / "), t.Total, t.Move, t.Same, t.Blocked, t.Taken, t.Duplicate)
}
