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
	for _, musicPath := range p.MusicPaths {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := h.tidyOne(ctx, musicPath, p); err != nil {
			log.Printf("[tidy] %s: %v", musicPath, err)
		}
	}
	return nil
}

// tidyOne 重组单个文件并在 os.Rename 成功后同步更新 db.Folder / db.Track
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

// ─── Cross-package helpers ─────────────────────────────────────────────────

func baseName(p string) string { return filepath.Base(p) }

func baseNameNoExt(p string) string {
	b := filepath.Base(p)
	if i := strings.LastIndex(b, "."); i > 0 {
		return b[:i]
	}
	return b
}

func parentDir(p string) string {
	d := filepath.Dir(p)
	if d == "." {
		return ""
	}
	return d
}

func asString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
