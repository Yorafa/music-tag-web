package handler

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/dedup"
	"go-music-tag/internal/utils"
)

// Read-only duplicate detection, and the delete that follows it.
//
// Until now the four-stage dedup funnel had exactly one caller — the write
// path (applyFileUpdate), where a Duplicate verdict refuses the write. That
// makes it a guard rail, not a feature: the user has no way to ask "which of
// my files are duplicates?" without attempting a write, and a library that
// already contains duplicates (merged from a backup, a 320k→flac upgrade, a
// two-disk copy) never surfaces them at all.
//
// CheckDuplicate exposes the same funnel with no side effect. It shares
// dedupCheckFor so a verdict here and a verdict on the write path for the
// same file cannot disagree — one checker, one index, one rule.

// maxDuplicateCheckPaths bounds one request. Each path is a full four-stage
// check, and the fingerprint stage spawns fpcalc over a duration-filtered
// candidate set, so an unbounded batch is a request that can occupy a
// gateway worker for minutes. 500 is far above any realistic hand-selection.
const maxDuplicateCheckPaths = 500

// CheckDuplicate handles POST /api/check_duplicate/ — run the dedup funnel
// over an explicit list of files and report each verdict.
//
// Body: { file_full_paths: [...] } — relative to MUSIC_DIR, the same string
// the Worklist rows carry as fullPath.
//
// Response: { results: [...], summary: {...} } where each result is one
// input path. The response is always 200 + a summary: a file that cannot be
// checked is reported as a per-row error, not a request failure, because
// "one unreadable file" must not hide the verdict on the other forty.
//
// Deliberately sequential. dedup.Checker carries mutable state (SetMusicRoot
// is called per check) and an fpcalc handle, so fanning these out over
// goroutines would be a data race, not a speedup. The cap above is what
// keeps the worst case bounded instead.
func CheckDuplicate(c *gin.Context) {
	var req struct {
		FileFullPaths []string `json:"file_full_paths" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}
	if len(req.FileFullPaths) > maxDuplicateCheckPaths {
		Failure(c, fmt.Sprintf("too many paths: %d (max %d)",
			len(req.FileFullPaths), maxDuplicateCheckPaths))
		return
	}

	ctx := c.Request.Context()
	root := utils.MusicRoot()
	results := make([]duplicateRow, 0, len(req.FileFullPaths))
	summary := duplicateSummary{}

	for _, rel := range req.FileFullPaths {
		row := duplicateRow{FileFullPath: rel}

		abs, err := utils.SafeJoin(root, rel)
		if err != nil {
			row.Verdict = dedup.VerdictError
			row.Reason = "路径不在音乐库内"
			summary.Errors++
			results = append(results, row)
			continue
		}
		if _, err := os.Lstat(abs); err != nil {
			row.Verdict = dedup.VerdictSkipped
			row.Reason = "文件不存在"
			summary.Skipped++
			results = append(results, row)
			continue
		}

		r, err := dedupCheckFor(ctx, abs)
		if err != nil || r == nil {
			row.Verdict = dedup.VerdictError
			row.Reason = "查重服务不可用"
			summary.Errors++
			results = append(results, row)
			continue
		}
		row.Verdict = r.Verdict
		row.MatchField = r.MatchField
		row.DuplicatePath = r.DuplicatePath
		row.Reason = r.Reason
		row.Run = r.Run
		switch r.Verdict {
		case dedup.VerdictDuplicate:
			summary.Duplicate++
		case dedup.VerdictLikelyDuplicate:
			summary.Likely++
		case dedup.VerdictSkipped:
			summary.Skipped++
		case dedup.VerdictError:
			summary.Errors++
		default:
			summary.Unique++
		}
		results = append(results, row)
	}

	SuccessData(c, duplicateReport{Results: results, Summary: summary})
}

// duplicateRow is one file's verdict. Field names mirror dedup.Result so a
// caller already reading a write-path skip entry sees the same vocabulary.
type duplicateRow struct {
	FileFullPath  string   `json:"file_full_path"`
	Verdict       string   `json:"verdict"`
	MatchField    string   `json:"match_field,omitempty"`
	DuplicatePath string   `json:"duplicate_path,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	Run           []string `json:"run,omitempty"`
}

type duplicateSummary struct {
	Duplicate int `json:"duplicate"`
	Likely    int `json:"likely_duplicate"`
	Unique    int `json:"unique"`
	Skipped   int `json:"skipped"`
	Errors    int `json:"error"`
}

type duplicateReport struct {
	Results []duplicateRow   `json:"results"`
	Summary duplicateSummary `json:"summary"`
}

// DeleteFiles handles POST /api/delete_files/ — remove files the duplicate
// check flagged.
//
// This is a destructive endpoint and the design decision that matters is
// that it does NOT unlink. Files are moved under DATA_DIR/.trash/<ts>/,
// preserving their path relative to MUSIC_DIR, so the original is one `mv`
// away and the index cannot serve it afterwards (the trash is outside
// MUSIC_DIR, so neither the scanner nor http.Dir(MUSIC_DIR) can reach it).
// A hard delete would be irreversible from inside the app, and the one
// recovery path — restoring from the host's own backup — is not something a
// user should have to be told about at the moment they click.
//
// Body: { file_full_paths: [...] } relative to MUSIC_DIR.
//
// Safety, in order:
//   - SafeJoin under MUSIC_DIR (a path outside the root is refused, not
//     sanitised — the caller sent something wrong and hiding it is worse)
//   - regular files only; a directory is refused rather than recursed
//   - symlinks refused, matching the scanner's unconditional skip, so a
//     planted link cannot be used to move an arbitrary file out of the root
//   - the DB index row is dropped only after the file actually moved
func DeleteFiles(c *gin.Context) {
	var req struct {
		FileFullPaths []string `json:"file_full_paths" binding:"required"`
		// Why the caller is deleting. Recorded in the audit row so the log
		// distinguishes "this was a duplicate cleanup" from "the user picked
		// these rows and deleted them" — the same endpoint serves both. It
		// used to be a hardcoded "duplicate_cleanup" string, which would have
		// kept asserting a reason nobody gave once a second caller existed.
		// Bounded because it lands in the audit log verbatim.
		RequestedBy string `json:"requested_by"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request")
		return
	}
	if len(req.FileFullPaths) == 0 {
		Failure(c, "no paths given")
		return
	}
	if len(req.FileFullPaths) > maxDuplicateCheckPaths {
		Failure(c, fmt.Sprintf("too many paths: %d (max %d)",
			len(req.FileFullPaths), maxDuplicateCheckPaths))
		return
	}

	requestedBy := strings.TrimSpace(req.RequestedBy)
	if requestedBy == "" {
		// Older clients omit the field; the log still has to say something
		// true, and "unspecified" beats inheriting a reason nobody gave.
		requestedBy = "unspecified"
	}
	if len(requestedBy) > 64 {
		requestedBy = requestedBy[:64]
	}

	ctx := c.Request.Context()
	root := utils.MusicRoot()
	trashDir := filepath.Join(utils.DataDir(), ".trash", time.Now().Format("20060102-150405"))

	results := make([]deleteRow, 0, len(req.FileFullPaths))
	deleted, failed := 0, 0

	for _, rel := range req.FileFullPaths {
		row := deleteRow{FileFullPath: rel}

		abs, err := utils.SafeJoin(root, rel)
		if err != nil {
			row.Status = "refused"
			row.Reason = "路径不在音乐库内"
			failed++
			results = append(results, row)
			continue
		}
		fi, err := os.Lstat(abs)
		if err != nil {
			row.Status = "missing"
			row.Reason = "文件不存在"
			failed++
			results = append(results, row)
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			row.Status = "refused"
			row.Reason = "拒绝处理符号链接"
			failed++
			results = append(results, row)
			continue
		}
		if fi.IsDir() {
			row.Status = "refused"
			row.Reason = "只支持删除文件，不支持目录"
			failed++
			results = append(results, row)
			continue
		}

		dest := filepath.Join(trashDir, filepath.FromSlash(rel))
		if err := moveAside(abs, dest); err != nil {
			row.Status = "failed"
			row.Reason = err.Error()
			failed++
			results = append(results, row)
			continue
		}
		forgetIndexPath(ctx, abs)

		row.Status = "deleted"
		row.TrashPath = dest
		deleted++
		results = append(results, row)
	}

	status := audit.StatusSuccess
	if failed > 0 && deleted > 0 {
		status = audit.StatusPartial
	} else if deleted == 0 {
		status = audit.StatusFailed
	}
	audit.Log(ctx, audit.ActionDeleteFiles, "batch", "admin", status,
		deleted+failed, map[string]interface{}{
			"deleted":      deleted,
			"failed":       failed,
			"trash_dir":    trashDir,
			"file_paths":   req.FileFullPaths,
			"requested_by": requestedBy,
		}, nil)

	SuccessData(c, deleteReport{Results: results, Deleted: deleted, Failed: failed})
}

type deleteRow struct {
	FileFullPath string `json:"file_full_path"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	TrashPath    string `json:"trash_path,omitempty"`
}

type deleteReport struct {
	Results []deleteRow `json:"results"`
	Deleted int         `json:"deleted"`
	Failed  int         `json:"failed"`
}

// renameAside is os.Rename behind a variable so a test can make it fail the
// way a real deployment makes it fail.
//
// This is not a hypothetical: DATA_DIR and MUSIC_DIR are separate mounts in
// the default compose layout, so a rename between them returns EXDEV. A test
// with one temp dir is one device and can never produce that, which is how
// RestoreTrash shipped a plain os.Rename that failed with "invalid
// cross-device link" on every file in production while its Go test passed.
//
// The alternative — an integration test on two real mounts — is not runnable
// in `go test`, and the alternative before that, a per-call rename parameter,
// cannot reach a caller that goes through the HTTP handler.
var renameAside = os.Rename

// moveAside renames src into destDir, falling back to copy+remove when the
// two are on different filesystems (EXDEV) — which they are here whenever
// DATA_DIR and MUSIC_DIR are separate mounts, i.e. the default compose
// layout. Without the fallback, DeleteFiles would work in `go test` (one
// temp dir) and fail in every real deployment.
//
// The rename is also a parameter so a test can inject the EXDEV directly,
// without going through the package-level seam above.
func moveAside(src, dest string) error {
	return moveAsideWith(src, dest, renameAside)
}

func moveAsideWith(src, dest string, rename func(string, string) error) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("prepare trash dir: %w", err)
	}
	if err := rename(src, dest); err == nil {
		return nil
	}
	// The dest dir is created before the rename so a genuine permission
	// error surfaces as itself rather than being masked by a confusing
	// "no such file" from the fallback.
	if err := copyFileThenRemove(src, dest); err != nil {
		return err
	}
	return nil
}

func copyFileThenRemove(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create trash copy: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dest)
		return fmt.Errorf("copy to trash: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("close trash copy: %w", err)
	}
	if err := os.Remove(src); err != nil {
		// The copy exists, so the content is safe even though the
		// original path still does. Report it rather than silently
		// leaving two copies of the same file in the library.
		return fmt.Errorf("copy succeeded but removing original failed: %w", err)
	}
	return nil
}

// forgetIndexPath drops the music_folder row for a file that has left the
// library.
//
// Skipping this is not cosmetic: internal/dedup selects hash and
// fingerprint candidates by querying that index, and a row pointing at a
// file that no longer exists is a candidate that fails to open on every
// subsequent check. Worse, the duration that fed the index still matches,
// so the stale row keeps a deleted song in the candidate set forever.
func forgetIndexPath(ctx context.Context, abs string) {
	if dedupDB == nil {
		return
	}
	if err := dedupDB.WithContext(ctx).
		Exec("DELETE FROM music_folder WHERE path = ?", abs).Error; err != nil {
		// A stale index row degrades dedup, it does not corrupt
		// anything — and the next full scan reconciles it. Logged so
		// it is visible rather than silently accumulating.
		log.Printf("[delete_files] index cleanup for %s failed: %v", abs, err)
	}
}
