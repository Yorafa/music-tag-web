// Package tasks TidyFolder handler。Ported from applications/task/tasks.py tidy_folder_task。
package tasks

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/db"
	"go-music-tag/internal/events"
	"go-music-tag/internal/tag"
	"go-music-tag/internal/utils"
)

// TidyFolderHandler 按 root_path/{first_dir}/{second_dir?} 重组文件。
//
// Bus is optional. When non-nil, every successful rename publishes a
// FileMovedEvent so the gateway-side PathCache can invalidate the old
// entry. We don't fail the task if the publish fails — the file is on
// disk and the DB is already updated; cache staleness is recoverable on
// the next miss.
type TidyFolderHandler struct {
	DB        *gorm.DB
	MusicRoot string
	Bus       events.Bus // optional; nil = no publish
}

// asynqRetryCount reports how many times the running task has already been
// retried (0 on the first attempt).
//
// Indirected through a var purely so tests can drive the retry path: asynq
// stores this in a context value whose key is an unexported type in an
// internal/ package, so a test cannot construct a "this is attempt N" context
// on its own. Production always goes through asynq.GetRetryCount.
var asynqRetryCount = func(ctx context.Context) int {
	n, _ := asynq.GetRetryCount(ctx)
	return n
}

func (h *TidyFolderHandler) ProcessTask(ctx context.Context, t Task) error {
	var p TidyFolderPayload
	if raw, ok := t.Payload.(*TidyFolderPayload); ok && raw != nil {
		p = *raw
	}
	// RootPath is deliberately NOT required here. An empty one is the
	// library root (TidyRoot resolves it), and ProcessTask normalises it
	// into an absolute path a few lines below — the check that used to
	// reject it ran BEFORE that resolution and so threw away the one
	// value the dialog sends by default.
	if len(p.MusicPaths) == 0 || len(p.Segments) == 0 {
		return fmt.Errorf("invalid tidy payload")
	}
	// The templates are validated up front, before anything moves. An
	// unknown key is a typo in the dialog, and finding it out one file at
	// a time means 500 identical failures in the log and a batch that
	// half-ran before the first real refusal.
	//
	// An EMPTY var map is enough to do it: the expander decides
	// unknown-key purely from the allow-list, not from what resolved. Every
	// real field comes back in Expansion.Empty here, which this call site
	// ignores on purpose — that list is about this file's tags, which are
	// not read yet.
	//
	// Note what is NOT checked: that a level contains a placeholder.
	// utils.TemplateFieldError requires one, but its reason is that a
	// filename template with none renames every file to the same literal
	// name. A directory level has no such problem — "Live" or
	// "Various Artists" is a legitimate fixed grouping, and refusing it
	// would forbid the case this feature exists for.
	for i, seg := range p.Segments {
		if strings.TrimSpace(seg) == "" {
			return fmt.Errorf("invalid tidy payload: 第 %d 层是空的", i+1)
		}
		if _, err := utils.ExpandFilenameTemplate(seg, map[string]string{}); err != nil {
			return fmt.Errorf("invalid tidy payload: 第 %d 层：%w", i+1, err)
		}
	}
	// Contain the destination before anything moves.
	//
	// p.RootPath used to go straight into utils.SafeJoin as its TRUSTED
	// root, which is the one thing SafeJoin assumes it can trust. It came
	// from the request body, so a caller could name any directory and have
	// the worker create the tree under it and then os.Rename library files
	// into it. sanitizeTidySeg defends the tag-derived path segments
	// (first_dir / second_dir) against exactly this, which is what makes
	// the gap easy to miss: the segments were checked, the root was not.
	//
	// The dialog's own placeholder is "/app/media/", so this is reachable
	// by typing the wrong thing as well as by crafting a request.
	root, err := h.tidyRoot(p.RootPath)
	if err != nil {
		// Logged here as well as returned. The gateway has already told
		// the user "已提交目录整理异步任务" by the time this runs, and
		// asynq's own failure line does not carry the payload — so a
		// refused root would otherwise be a tidy that silently did
		// nothing. That is the shape of bug the toolbar comment about
		// MusicPaths arriving empty already describes once.
		log.Printf("[tidy] refusing the whole batch: %v", err)
		// And audited, not just logged: the audit log is the one place
		// the operator looks to answer "what happened to my files", and
		// this row is the only trace a refused batch leaves there. Before
		// this, a tidy that moved nothing produced NO audit row at all —
		// which is how the path-form bug below went unnoticed for so
		// long: every file was refused, the user saw a success toast, and
		// the operation history stayed empty.
		//
		// First attempt only. The refusal is permanent (a path outside the
		// library stays outside it), so asynq will retry this task up to
		// MaxRetry(5) times and every retry would otherwise append another
		// identical row — one user action, six log entries. The task still
		// returns err so the task record ends up failed rather than
		// completed; only the duplicate logging is suppressed.
		if asynqRetryCount(ctx) == 0 {
			audit.Log(ctx, audit.ActionTidyFolder, p.RootPath, "worker", audit.StatusFailed,
				len(p.MusicPaths), map[string]interface{}{
					"root_path": p.RootPath,
					"requested": len(p.MusicPaths),
					"refused":   "root_path is outside the library",
					"segments":  p.Segments,
				}, err)
		}
		return err
	}
	p.RootPath = root

	// Per-file outcomes. Successes audit themselves inside tidyOne (one row
	// per moved file, which is the granularity the operation log is for);
	// failures are collected here and summarised into ONE row, because a
	// 500-track batch where every file failed would otherwise bury the log
	// under 500 near-identical entries.
	var (
		failed    int
		firstErrs []string
	)
	for _, musicPath := range p.MusicPaths {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		// The source is contained too, and per-file rather than per-task:
		// one bad row in a 500-track batch should cost that row, not the
		// other 499. tidyOne moves a file, so "it is not in the library"
		// has to be a refusal rather than a best-effort attempt.
		src, sErr := h.tidySource(musicPath)
		if sErr != nil {
			log.Printf("[tidy] %s: %v", musicPath, sErr)
			failed++
			if len(firstErrs) < 5 {
				firstErrs = append(firstErrs, fmt.Sprintf("%s: %v", filepath.Base(musicPath), sErr))
			}
			continue
		}
		if err := h.tidyOne(ctx, src, p); err != nil {
			log.Printf("[tidy] %s: %v", musicPath, err)
			failed++
			if len(firstErrs) < 5 {
				firstErrs = append(firstErrs, fmt.Sprintf("%s: %v", filepath.Base(musicPath), err))
			}
		}
	}
	if failed > 0 {
		status := audit.StatusPartial
		if failed == len(p.MusicPaths) {
			status = audit.StatusFailed
		}
		audit.Log(ctx, audit.ActionTidyFolder, p.RootPath, "worker", status, failed,
			map[string]interface{}{
				"root_path": p.RootPath,
				"requested": len(p.MusicPaths),
				"failed":    failed,
				"segments":  p.Segments,
				"examples":  firstErrs,
			}, nil)
	}
	return nil
}

// TidyRoot resolves a tidy task's root_path against the configured music root,
// refusing anything outside it.
//
// Exported because the gateway runs the SAME check before enqueueing (see
// handler.TidyFolder): the rejection has to be synchronous, or a typo reads as
// a success that quietly moved nothing. Two copies of the rule would be free
// to drift, so there is one.
//
// Strictly absolute for anything the caller DOES supply, unlike the
// per-file paths. Those are widened to accept the root-relative form
// because the frontend genuinely cannot send the absolute one.
//
// An EMPTY root_path means the library root itself, and that is the case
// the dialog leads with. It used to be refused: the argument was that a
// stray word must not resolve to <musicRoot>/<word> and quietly build a
// nested tree instead of reporting the typo. That argument is sound for a
// value the user typed, and it does not apply to no value at all — but
// refusing empty left the dialog asking for a server-side absolute path
// the client has no way to learn (nothing in the API reports MUSIC_DIR),
// so 预览方案 sat disabled until the user guessed something like
// /app/media. "Tidy the library, in place" is the common case and the one
// that needs the least thought; requiring a guessed absolute path to
// express it was the whole reason the button did nothing.
//
// The strictness still holds for a non-empty value, which is where the
// typo risk actually lives.
//
// An unconfigured library root refuses everything rather than admitting
// everything: this handler MOVES files, and without a root there is no way to
// know what is in bounds. utils.SafeAbs refuses an empty root and its
// message ("root is empty") names the actual fault. The gateway handler
// keeps an empty-root check of its own, because it is answering an
// operator rather than a log.
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

func TidyRoot(musicRoot, rootPath string) (string, error) {
	if strings.TrimSpace(rootPath) == "" {
		if strings.TrimSpace(musicRoot) == "" {
			return "", fmt.Errorf("tidy: 未配置曲库根目录，无法确定整理目标")
		}
		abs, err := filepath.Abs(musicRoot)
		if err != nil {
			return "", fmt.Errorf("tidy: 曲库根目录 %q 无法解析: %w", musicRoot, err)
		}
		return abs, nil
	}
	abs, err := utils.SafeAbs(musicRoot, rootPath)
	if err != nil {
		return "", fmt.Errorf("tidy: root_path %q is outside the library: %w", rootPath, err)
	}
	return abs, nil
}

func (h *TidyFolderHandler) tidyRoot(rootPath string) (string, error) {
	return TidyRoot(h.MusicRoot, rootPath)
}

// tidySource resolves one payload path against the music root.
//
// Both forms are accepted, because both are what this codebase actually
// produces for "a library file": music_folder rows are stored absolute, and
// the worklist rows the frontend sends are root-relative (see
// utils.ResolveUnderRoot for why the client cannot send the absolute form).
// An absolute path outside the library is still refused outright, and a
// relative one that climbs out is still refused by SafeJoin's containment
// check — this only widens WHICH inputs are understood, not what is
// reachable.
//
// No explicit empty-root check, because there is no case left for it to
// handle: tidyRoot runs first and fails the whole task when MusicRoot is
// unset, and ResolveUnderRoot refuses an empty root anyway. An earlier
// version had the branch, and mutation testing showed it was unreachable —
// tidyRoot had already returned, so the branch could only ever be dead code
// pretending to be a decision.
func (h *TidyFolderHandler) tidySource(musicPath string) (string, error) {
	abs, err := utils.ResolveUnderRoot(h.MusicRoot, musicPath)
	if err != nil {
		return "", fmt.Errorf("not in the library: %w", err)
	}
	return abs, nil
}

// tidyOne 重组单个文件并在 os.Rename 成功后同步更新 db.Folder / db.Track
//
// p is expected to be ALREADY VALIDATED: ProcessTask resolves RootPath and
// MusicPaths against the music root before calling this. The check is not
// repeated here on purpose — it belongs at the payload boundary, once, where
// it can reject the whole task rather than one file — but that also means
// tidyOne is unsafe to call with a raw payload, and its test does exactly
// that. See TestTidyOne_RequiresAValidatedPayload.
// 中的 path 字段（带 WithContext(ctx) 让 asynq 取消可以中断 GORM），
// 然后发布 FileMoved 事件让 webhook handler 失效缓存。
func (h *TidyFolderHandler) tidyOne(ctx context.Context, musicPath string, p TidyFolderPayload) error {
	info, err := tag.Read(musicPath)
	if err != nil {
		return fmt.Errorf("read meta: %w", err)
	}
	dst, _, err := tidyDestPath(p.RootPath, p.Segments, info, filepath.Base(musicPath))
	if err != nil {
		return err
	}
	// Refuse to overwrite something that is already there. os.Rename
	// replaces silently on Linux, so without this a tidy whose rule
	// collapses two files onto one name destroys one of them with no
	// error anywhere — and the dialog's plan is computed in the browser
	// now, where it cannot stat the destination at all.
	//
	// A DIRECTORY there is not a conflict: that is the tree this whole
	// operation exists to build. Only a non-directory is.
	if fi, statErr := os.Lstat(dst); statErr == nil && !fi.IsDir() {
		return fmt.Errorf("目标位置已有同名文件，未移动：%s", dst)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		// A permission problem or a stalled mount is "I could not tell",
		// not "it is free" — refuse rather than gamble.
		return fmt.Errorf("无法检查目标位置 %s：%w", dst, statErr)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(musicPath, dst); err != nil {
		return err
	}
	// The .lrc and the album cover are filed next to the audio, not
	// inside it, so the rename above strands them in the old folder and
	// splits the album across two directories. Best-effort for the same
	// reason as the rename itself: the file has already moved, so report
	// the leftovers rather than failing a job that mostly succeeded.
	for _, m := range tag.MoveSidecars(musicPath, dst) {
		if m.Err != nil {
			log.Printf("[tidy] sidecar %s -> %s did not follow: %v",
				filepath.Base(m.From), filepath.Base(m.To), m.Err)
		}
	}
	if h.DB != nil {
		// Update Folder row (tracker's primary key). Track rows track audio
		// files by path; mirror the rename so refresh-by-path keeps working.
		// WithContext lets asynq cancellation interrupt the UPDATE.
		res := h.DB.WithContext(ctx).Model(&db.Folder{}).
			Where("path = ?", musicPath).
			Updates(map[string]interface{}{"path": dst, "name": filepath.Base(dst)})
		if res.Error != nil {
			log.Printf("[tidy] db.Folder update %s: %v", musicPath, res.Error)
		}
		res = h.DB.WithContext(ctx).Model(&db.Track{}).
			Where("path = ?", musicPath).
			Updates(map[string]interface{}{"path": dst, "name": filepath.Base(dst)})
		if res.Error != nil {
			log.Printf("[tidy] db.Track update %s: %v", musicPath, res.Error)
		}
	}
	// Publish FileMoved after the rename + DB writes. Best-effort: if the
	// bus is down or the publish times out, log and continue — the file is
	// already on disk and the DB row has the new path.
	if h.Bus != nil {
		if err := h.Bus.Publish(ctx, events.TopicFileMoved, events.FileMovedEvent{
			Action:  "renamed",
			OldPath: musicPath,
			NewPath: dst,
		}); err != nil {
			log.Printf("[tidy] publish FileMoved %s: %v", musicPath, err)
		}
	}
	audit.Log(ctx, audit.ActionTidyFolder, filepath.Base(dst), "worker", audit.StatusSuccess, 1, map[string]interface{}{
		"old_path":  musicPath,
		"new_path":  dst,
		"root_path": p.RootPath,
	}, nil)
	fmt.Printf("[tidy] %s -> %s\n", musicPath, dst)
	return nil
}

// tidyVars 把 TagInfo 展开成模板变量表。
//
// 键集就是 utils.RenameTemplateFields——与「从标签改名」和「解析文件名」
// 用的是同一套词汇。这里以前是一份独立的 switch，只认 album/artist/
// genre/year/albumartist 五个键，而两个改名对话框用的是另外八个；同一份
// 标签在两个界面里能填的字段不一样，本身就是个 bug。现在三者共用一份
// 列表（utils.RenameTemplateFields），字段芯片不会和服务端拒绝的列表
// 漂移。
//
// 原来没有 tracknumber/discnumber/title，是能力缺失而不是有意排除：
// 「音轨 - 艺术家 - 专辑」是用户明确要的整理结构。
func tidyVars(info *tag.TagInfo) map[string]string {
	year := ""
	if info.Year != 0 {
		year = strconv.Itoa(info.Year)
	}
	return map[string]string{
		"title":       info.Title,
		"artist":      info.Artist,
		"album":       info.Album,
		"albumartist": info.AlbumArtist,
		"genre":       info.Genre,
		"year":        year,
		"tracknumber": info.TrackNumber,
		"discnumber":  info.DiscNumber,
	}
}

// tidyDestPath derives where one file goes, and is the ONLY place that
// answer is computed. The preview and the move both call it, so a plan the
// operator read cannot disagree with what runs — which was the whole reason
// to add a preview at all. Two implementations would be free to drift on
// exactly the rules that matter (sanitising, containment, the empty-segment
// fallback) and would do so silently, in the direction that shows a tidy
// plan and then moves files somewhere else.
//
// `missing` lists the tags that resolved to nothing across the whole
// template, deduplicated, in level order. The caller surfaces it rather
// than acting on it: `${year} - ${album}` on a track with no year still
// produces a usable directory (" - 叶惠美"), and only the operator can say
// whether that is what they meant. A level that renders to nothing AT ALL
// is a different matter and is an error — see below.
func tidyDestPath(root string, segments []string, info *tag.TagInfo, filename string) (string, []string, error) {
	if len(segments) == 0 {
		return "", nil, fmt.Errorf("tidy: 没有指定任何目录层级")
	}
	vars := tidyVars(info)
	var (
		levels  []string
		missing []string
		seen    = map[string]bool{}
	)
	for i, tmpl := range segments {
		if strings.TrimSpace(tmpl) == "" {
			return "", nil, fmt.Errorf("tidy: 第 %d 层是空的", i+1)
		}
		// The strict expander, not RenderTemplate: an unknown key must
		// be an error the dialog can show, never a literal "${typo}"
		// becoming a directory name.
		exp, err := utils.ExpandFilenameTemplate(tmpl, vars)
		if err != nil {
			return "", nil, fmt.Errorf("tidy: 第 %d 层：%w", i+1, err)
		}
		for _, k := range exp.Empty {
			if !seen[k] {
				seen[k] = true
				missing = append(missing, k)
			}
		}
		raw := exp.Name
		if strings.TrimSpace(raw) == "" {
			// Every placeholder in this level was empty AND the level had
			// no fixed text to fall back on. The old two-level version
			// had an unconditional "未知" for this; keeping it means an
			// untagged track still lands somewhere instead of failing
			// the whole batch on a level the operator never saw.
			raw = unknownDir
		}
		seg, err := sanitizeTidySeg(fmt.Sprintf("第 %d 层", i+1), raw)
		if err != nil {
			return "", nil, err
		}
		levels = append(levels, seg)
	}
	// A fresh slice rather than `append(levels, filename)`: that would
	// reuse levels' backing array and leave a stray filename sitting in
	// it, which is harmless right now and a genuine bug the moment
	// anything reads `levels` again below this line.
	dst, err := utils.SafeJoin(root, filepath.Join(append(append([]string{}, levels...), filename)...))
	if err != nil {
		return "", nil, fmt.Errorf("tidy: %w", err)
	}
	return dst, missing, nil
}

// unknownDir is the directory name a level falls back to when the
// template produced nothing at all. Spelled rather than symbolised because
// it ends up in the operator's filesystem.
const unknownDir = "未知"

// sanitizeTidySeg validates a tag-derived directory segment used by
// TidyFolder for renaming. pickAttr returns the verbatim album / artist /
// genre / year tag value, which is attacker-controlled via UpdateID3 and
// may contain "..", path separators, or control bytes that would let
// os.Rename move the file outside the configured music root.
//
// Defence in depth: strip bad chars first (mirrors tag.sanitizeFileName),
// then keep only the trailing path segment via filepath.Base, then refuse
// "."/".." / leading-dot segments. TidyFolder is a low-frequency batch
// operation, so the cost of rejecting borderline album names is
// acceptable.
func sanitizeTidySeg(label, raw string) (string, error) {
	bad := []rune{'/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t'}
	s := raw
	for _, r := range bad {
		s = strings.ReplaceAll(s, string(r), "_")
	}
	s = strings.TrimSpace(s)
	seg := filepath.Base(s)
	if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
		return "", fmt.Errorf("tidy: unsafe %s segment: %q", label, raw)
	}
	return seg, nil
}
