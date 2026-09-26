// Rename files from their own tags — the inverse of 解析文件名.
//
// # The split
//
// 解析文件名 reads a name and writes tags. This reads tags and writes
// names. Both are per-file operations driven by a template, and both go
// through a preview before anything is written; the two dialogs are
// deliberately the same shape so the second one needs no explanation.
//
// The engine underneath is not new — utils.RenderTemplate plus
// SanitizePath plus the collision check in update.go already do it, and
// the 批量编辑标签 dialog's rename toggle has been using it. What is new
// here is:
//
//   - the variables actually cover the tags people have (genre and year
//     did not, and an unfound key used to be left in the output as
//     literal `${year}` text — see renameTemplateVars);
//   - a dry run exists, so a 500-file rename can be read before it is
//     committed;
//   - collisions are detected across the BATCH, not just against what is
//     already on disk. Two files whose templates render to the same name
//     is the failure that loses data, and per-file checks cannot see it.
//
// # Why apply is synchronous
//
// batch_update_id3 renames files inside its handler rather than in a
// worker, and this follows suit: a rename is an os.Rename, the work is
// bounded by the selection the operator just made, and returning the
// per-row result immediately is worth more here than keeping the request
// short. The tag-writing path needs a worker because decoding and writing
// an audio file is slow; this does not.

package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/tag"
	"go-music-tag/internal/utils"
)

// RenameFromTags statuses. `ok` is the only one that means the file will
// move; the rest are outcomes the operator needs to see counted rather
// than buried in a per-row list they will not read.
const (
	// RenameOK — the name will change to NewName.
	RenameOK = "ok"
	// RenameNoChange — the template renders to the name the file already
	// has. Not an error: half the selection already being correct is the
	// normal case, and reporting it as a failure would make the count lie.
	RenameNoChange = "no_change"
	// RenameBlocked — the template cannot be used here: an unknown field,
	// no placeholders, or it rendered to nothing.
	RenameBlocked = "blocked"
	// RenameTaken — another file in this same batch renders to the same
	// name. The FIRST one wins and the rest are blocked; renaming them
	// all would silently overwrite (os.Rename replaces).
	RenameTaken = "taken"
	// RenameFailed — the rename itself errored (permissions, the file
	// vanished between preview and apply).
	RenameFailed = "failed"
)

// MaxRenameRows mirrors MaxPreviewRows: one bulk rename of a label's back
// catalogue is the largest sane request, and it stays under the 8 MiB
// body limit the /api middleware enforces.
const MaxRenameRows = 5000

// RenamePlanRow is one file's prospective rename.
//
// NewName carries the EXTENSION, because that is what the operator will
// see on disk and what the client writes back into the table.
type RenamePlanRow struct {
	Path    string `json:"path"`
	OldName string `json:"old_name"`
	NewName string `json:"new_name"`
	Status  string `json:"status"`
	// Missing lists template fields that were empty for this file, so
	// the operator can see WHY the name has a gap in it.
	Missing []string `json:"missing,omitempty"`
	// Detail is a human-readable reason for a non-ok status.
	Detail string `json:"detail,omitempty"`
}

// renameRequest is the shared body of preview and apply.
type renameRequest struct {
	Paths    []string `json:"paths" binding:"required"`
	Template string   `json:"template" binding:"required"`
}

// planRename computes one row without touching the filesystem. It is the
// single source of truth for what a rename would do, so preview and apply
// cannot disagree — apply calls this again rather than trusting anything
// the client echoes back.
// Every outcome is a status on the row rather than an error, because the
// caller keeps going: one file with a missing tag must not stop the other
// four hundred from being planned. Only a path that failed SafeJoin is
// reported as a Go error, and even that becomes a blocked row.
func planRename(root, abs, tmpl string) RenamePlanRow {
	row := RenamePlanRow{Path: abs, OldName: filepath.Base(abs)}

	exp, err := utils.ExpandFilenameTemplate(tmpl, readFileContext(abs))
	if err != nil {
		row.Status = RenameBlocked
		row.Detail = err.Error()
		return row
	}
	row.Missing = exp.Empty
	if exp.Name == "" {
		row.Status = RenameBlocked
		row.Detail = "模板展开后是空文件名"
		return row
	}

	// Keep the file's own extension: a rename must not convert formats,
	// and an operator who wants ".mp3" in the name is asking for a
	// different feature.
	ext := filepath.Ext(abs)
	if !strings.EqualFold(filepath.Ext(exp.Name), ext) {
		exp.Name += ext
	}
	row.NewName = exp.Name

	// The file must not try to rename itself. Compared on the sanitised
	// name rather than the path so a template that reproduces the
	// current name exactly is correctly a no-op.
	if exp.Name == row.OldName {
		row.Status = RenameNoChange
		return row
	}

	target := filepath.Join(filepath.Dir(abs), exp.Name)
	safe, err := utils.SafeAbs(root, target)
	if err != nil {
		row.Status = RenameBlocked
		row.Detail = "rename target unsafe: " + err.Error()
		return row
	}
	row.NewName = filepath.Base(safe)
	row.Status = RenameOK
	return row
}

// resolveRenames plans every requested path and applies the two rules
// that need the whole batch in view: a name already on disk, and a name
// another row in this same batch also wants.
//
// dryRun stops before any os.Rename; apply performs them. Both call this,
// so the counts a preview shows are the counts apply will report.
func resolveRenames(root string, req renameRequest, dryRun bool) []RenamePlanRow {
	rows := make([]RenamePlanRow, 0, len(req.Paths))
	// claimed is keyed by the FULL target path, lowercased. Keying on
	// the bare name instead would make two tracks in different folders
	// that legitimately end up with the same name look like a
	// collision — which is the normal case for any library with more
	// than one album.
	claimed := map[string]bool{}

	for _, rel := range req.Paths {
		rel = strings.TrimPrefix(rel, "/")
		abs, err := utils.SafeJoin(root, rel)
		if err != nil {
			rows = append(rows, RenamePlanRow{
				Path: rel, Status: RenameBlocked, Detail: err.Error(),
			})
			continue
		}
		row := planRename(root, abs, req.Template)
		if row.Status != RenameOK {
			rows = append(rows, row)
			continue
		}
		target := filepath.Join(filepath.Dir(abs), row.NewName)

		// Batch collision FIRST. Two rows rendering to the same target
		// is invisible to a per-file stat, and os.Rename replaces, so
		// the second would destroy the first. Whoever got there first
		// keeps the name.
		key := strings.ToLower(target)
		if claimed[key] {
			row.Status = RenameTaken
			row.Detail = "同一批次里有另一个文件也要用这个名字"
			rows = append(rows, row)
			continue
		}

		// Then the on-disk check, on the resolved target. A stat error
		// that is not "not exist" is not evidence the name is free.
		if _, statErr := os.Stat(target); statErr == nil {
			row.Status = RenameTaken
			row.Detail = "目标位置已存在同名文件"
			rows = append(rows, row)
			continue
		} else if !os.IsNotExist(statErr) {
			row.Status = RenameBlocked
			row.Detail = "无法检查目标位置: " + statErr.Error()
			rows = append(rows, row)
			continue
		}
		claimed[key] = true

		if !dryRun {
			if err := os.Rename(abs, target); err != nil {
				row.Status = RenameFailed
				row.Detail = err.Error()
			} else {
				// Sidecars (.lrc, .nfo, cover.jpg) are named after the
				// old basename and would be orphaned otherwise — the
				// library would stop listing the lyrics.
				tag.MoveSidecars(abs, target)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// tallyRenames counts statuses for the summary line. Kept next to the
// handlers because the preview's numbers and the apply's numbers must
// come from the same place or the dialog lies by omission.
func tallyRenames(rows []RenamePlanRow) map[string]int {
	t := map[string]int{}
	for _, r := range rows {
		t[r.Status]++
	}
	return t
}

// PreviewRenameFromTags handles POST /api/tag/preview_rename_from_tags/.
//
//	{
//	  "paths":    ["a.flac", "b.flac"],   // relative to MUSIC_DIR
//	  "template": "${artist} - ${title}"
//	}
//
//	→ 200 { rows: [...RenamePlanRow...], tally: {ok: n, ...} }
//	→ Failure(...) if the template is empty or has no placeholders.
//
// Nothing is written. The point is to let an operator read 500 old→new
// pairs before committing to them, which the batch editor's rename
// toggle could not offer.
func PreviewRenameFromTags(c *gin.Context) {
	var req renameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}
	if len(req.Paths) == 0 {
		Failure(c, "paths is empty")
		return
	}
	if len(req.Paths) > MaxRenameRows {
		Failure(c, fmt.Sprintf("too many paths (max %d)", MaxRenameRows))
		return
	}
	if err := utils.TemplateFieldError(req.Template); err != nil {
		Failure(c, err.Error())
		return
	}
	rows := resolveRenames(utils.MusicRoot(), req, true)
	SuccessData(c, gin.H{"rows": rows, "tally": tallyRenames(rows), "dry_run": true})
}

// ApplyRenameFromTags handles POST /api/tag/apply_rename_from_tags/.
//
// Same body as the preview. Re-plans every row rather than trusting a
// client-supplied plan: a file can have been renamed, or the template
// edited, between the preview and the click, and applying a stale plan is
// how a rename lands on the wrong file.
//
//	→ 200 { rows: [...], tally: {...} }
func ApplyRenameFromTags(c *gin.Context) {
	var req renameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}
	if len(req.Paths) == 0 {
		Failure(c, "paths is empty")
		return
	}
	if len(req.Paths) > MaxRenameRows {
		Failure(c, fmt.Sprintf("too many paths (max %d)", MaxRenameRows))
		return
	}
	if err := utils.TemplateFieldError(req.Template); err != nil {
		Failure(c, err.Error())
		return
	}
	rows := resolveRenames(utils.MusicRoot(), req, false)
	SuccessData(c, gin.H{"rows": rows, "tally": tallyRenames(rows), "dry_run": false})
}
