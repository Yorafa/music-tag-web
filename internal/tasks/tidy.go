// Package tasks TidyFolder handler。Ported from applications/task/tasks.py tidy_folder_task。
package tasks

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

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

func (h *TidyFolderHandler) ProcessTask(ctx context.Context, t Task) error {
	var p TidyFolderPayload
	if raw, ok := t.Payload.(*TidyFolderPayload); ok && raw != nil {
		p = *raw
	}
	if len(p.MusicPaths) == 0 || p.RootPath == "" || p.FirstDir == "" {
		return fmt.Errorf("invalid tidy payload")
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
		return err
	}
	p.RootPath = root

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
			continue
		}
		if err := h.tidyOne(ctx, src, p); err != nil {
			log.Printf("[tidy] %s: %v", musicPath, err)
		}
	}
	return nil
}

// tidyRoot resolves a payload's root_path against the configured music root,
// refusing anything outside it.
//
// An unconfigured root refuses everything rather than admitting everything.
// The read-only consumers (dedup, the pruners) treat an empty root as "no
// scope configured" so they degrade to a narrower answer, but this one moves
// files, and without a root there is no way to know what is in bounds — so
// the answer has to be "no".
// An unconfigured root refuses everything rather than admitting everything.
// The read-only consumers (dedup, the pruners) treat an empty root as "no
// scope configured" so they degrade to a narrower answer, but this one moves
// files, and without a root there is no way to know what is in bounds — so
// the answer has to be "no". The explicit check exists for the message;
// SafeAbs would refuse the empty root too, less clearly.
func (h *TidyFolderHandler) tidyRoot(rootPath string) (string, error) {
	if h.MusicRoot == "" {
		return "", fmt.Errorf("tidy: no music root configured, refusing to move anything")
	}
	abs, err := utils.SafeAbs(h.MusicRoot, rootPath)
	if err != nil {
		return "", fmt.Errorf("tidy: root_path %q is outside the library: %w", rootPath, err)
	}
	return abs, nil
}

// tidySource resolves one payload path against the music root.
//
// SafeAbs rather than SafeJoin, for the same reason as everywhere else: a
// path in a payload is absolute, so an absolute path outside the library is
// something the caller has no business naming and is refused outright. A
// relative path is refused too, rather than joined onto the root — in a
// payload that moves files, "which directory did you mean" is not a question
// worth guessing at.
//
// No explicit empty-root check, because there is no case left for it to
// handle: tidyRoot runs first and fails the whole task when MusicRoot is
// unset, and SafeAbs refuses an empty root anyway. An earlier version had
// the branch, and mutation testing showed it was unreachable — tidyRoot had
// already returned, so the branch could only ever be dead code pretending to
// be a decision.
func (h *TidyFolderHandler) tidySource(musicPath string) (string, error) {
	abs, err := utils.SafeAbs(h.MusicRoot, musicPath)
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
	firstRaw := pickAttr(info, p.FirstDir)
	if firstRaw == "" {
		firstRaw = "未知"
	}
	first, err := sanitizeTidySeg("first_dir", firstRaw)
	if err != nil {
		return err
	}
	var dst string
	if p.SecondDir != "" {
		secondRaw := pickAttr(info, p.SecondDir)
		if secondRaw == "" {
			secondRaw = "未知"
		}
		second, sErr := sanitizeTidySeg("second_dir", secondRaw)
		if sErr != nil {
			return sErr
		}
		dst, err = utils.SafeJoin(p.RootPath, filepath.Join(first, second, filepath.Base(musicPath)))
	} else {
		dst, err = utils.SafeJoin(p.RootPath, filepath.Join(first, filepath.Base(musicPath)))
	}
	if err != nil {
		return fmt.Errorf("tidy: %w", err)
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

// pickAttr 把 first_dir/second_dir 字符串 (如 "album", "year", "genre") 映射到 TagInfo 字段。
func pickAttr(info *tag.TagInfo, key string) string {
	switch key {
	case "album":
		return info.Album
	case "artist":
		return info.Artist
	case "genre":
		return info.Genre
	case "year":
		if info.Year == 0 {
			return ""
		}
		return fmt.Sprintf("%d", info.Year)
	case "albumartist":
		return info.AlbumArtist
	default:
		return ""
	}
}

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
