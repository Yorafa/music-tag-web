// Package tasks 扫描相关 handler 实现。Ported from applications/task/tasks.py + scan_utils.py。
//
// P1 已知边界：update_scan 不再执行 WHERE path NOT IN 删除（避免子扫描失败
// 误删整库）。删除操作只能由显式的 clear_music 触发。
package tasks

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"go-music-tag/internal/db"
)

// audioExt / coverExt 与 applications/task/constants.go 对齐。
var audioExt = map[string]bool{
	"flac": true, "mp3": true, "ape": true, "wav": true, "aiff": true,
	"wv": true, "tta": true, "m4a": true, "ogg": true, "mpc": true,
	"opus": true, "wma": true, "dsf": true, "dff": true,
}
var coverExt = map[string]bool{"jpg": true, "jpeg": true, "png": true}

// UpdateScanPayload 与 FullScanPayload 同 schema（worker 入口可选传 sub_paths）。
type UpdateScanPayload = FullScanPayload

// ─── FullScanHandler ────────────────────────────────────────────────────────

type FullScanHandler struct {
	DB        *gorm.DB
	MusicRoot string
}

func (h *FullScanHandler) ProcessTask(ctx context.Context, t Task) error {
	var p FullScanPayload
	if raw, ok := t.Payload.(*FullScanPayload); ok && raw != nil {
		p = *raw
	}
	return h.fullScan(ctx, p.SubPaths)
}

func (h *FullScanHandler) fullScan(ctx context.Context, subPaths [][2]string) error {
	if h.DB == nil {
		return fmt.Errorf("db not initialised")
	}
	musicFolder := h.musicRoot()
	ignoreData := filepath.Join(musicFolder, "data")
	var stack [][2]string
	if len(subPaths) == 0 {
		stack = append(stack, [2]string{"", musicFolder})
	} else {
		stack = append(stack, subPaths...)
	}
	const batchSize = 500
	batch := make([]db.Folder, 0, batchSize)
	for len(stack) > 0 {
		select {
		case <-ctx.Done():
			flushBatch(h.DB, batch)
			return ctx.Err()
		default:
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		parentUID, dir := top[0], top[1]
		fi, statErr := os.Stat(dir)
		if statErr != nil {
			continue
		}
		if dir == ignoreData {
			continue
		}
		if fi.IsDir() {
			entries, err := os.ReadDir(dir)
			if err != nil {
				log.Printf("[scan] read %s: %v", dir, err)
				continue
			}
			myUID := uuid.New().String()
			batch = append(batch, db.Folder{
				Name: filepath.Base(dir), Path: dir,
				FileType: "folder", UID: myUID, ParentID: parentUID,
				State: "scanning",
			})
			if len(batch) >= batchSize {
				flushBatch(h.DB, batch)
				batch = batch[:0]
			}
			for _, e := range entries {
				// SECURITY (P1.5 issue F — H5): skip symlinks unconditionally.
				// os.ReadDir returns DirEntry backed by Lstat on POSIX, so
				// e.Type() reports the symlink bit even when the target is
				// a directory. Without this, a hostile music dir containing
				// a symlink to /etc would let fullScan crawl outside the
				// music root and persist those paths into db.Folder /
				// db.Track — at which point Stream / GetCoverArt would
				// happily serve the off-tree files via http.ServeFile.
				if e.Type()&os.ModeSymlink != 0 {
					log.Printf("[scan] skip symlink %s/%s", dir, e.Name())
					continue
				}
				if e.IsDir() {
					stack = append(stack, [2]string{myUID, filepath.Join(dir, e.Name())})
				} else {
					fileUID := uuid.New().String()
					filePath := filepath.Join(dir, e.Name())
					ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))
					if audioExt[ext] {
						batch = append(batch, db.Folder{
							Name: filepath.Base(filePath), Path: filePath,
							FileType: "music", UID: fileUID, ParentID: myUID,
							State: "none",
						})
					} else if coverExt[ext] {
						batch = append(batch, db.Folder{
							Name: filepath.Base(filePath), Path: filePath,
							FileType: "image", UID: fileUID, ParentID: myUID,
							State: "none",
						})
					}
				}
			}
		} else {
			// file entry (already-popped from parent dir); record inline
			myUID := uuid.New().String()
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(dir), "."))
			if audioExt[ext] {
				batch = append(batch, db.Folder{
					Name: filepath.Base(dir), Path: dir,
					FileType: "music", UID: myUID, ParentID: parentUID,
					State: "none",
				})
			} else if coverExt[ext] {
				batch = append(batch, db.Folder{
					Name: filepath.Base(dir), Path: dir,
					FileType: "image", UID: myUID, ParentID: parentUID,
					State: "none",
				})
			}
		}
		if len(batch) >= batchSize {
			flushBatch(h.DB, batch)
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		flushBatch(h.DB, batch)
	}
	log.Printf("[scan] full_scan done (root=%s)", musicFolder)
	return nil
}

func flushBatch(conn *gorm.DB, rows []db.Folder) {
	if len(rows) == 0 || conn == nil {
		return
	}
	if err := conn.CreateInBatches(rows, 500).Error; err != nil {
		log.Printf("[scan] flush batch err: %v", err)
	}
}

func (h *FullScanHandler) musicRoot() string {
	if h.MusicRoot != "" {
		return h.MusicRoot
	}
	if v := os.Getenv("MUSIC_DIR"); v != "" {
		return v
	}
	return "/app/media"
}

// ─── UpdateScanHandler ──────────────────────────────────────────────────────

type UpdateScanHandler struct {
	DB        *gorm.DB
	MusicRoot string
}

func (h *UpdateScanHandler) ProcessTask(ctx context.Context, t Task) error {
	var p UpdateScanPayload
	if raw, ok := t.Payload.(*UpdateScanPayload); ok && raw != nil {
		p = *raw
	}
	return h.updateScan(ctx, p.SubPaths)
}

func (h *UpdateScanHandler) updateScan(ctx context.Context, subPaths [][2]string) error {
	if h.DB == nil {
		return fmt.Errorf("db not initialised")
	}
	musicFolder := h.musicRoot()
	ignoreData := filepath.Join(musicFolder, "data")
	var stack [][2]string
	if len(subPaths) == 0 {
		stack = append(stack, [2]string{"", musicFolder})
	} else {
		stack = append(stack, subPaths...)
	}
	now := time.Now()

	for len(stack) > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		top := stack[0]
		stack = stack[1:]
		parentUID, dir := top[0], top[1]
		if dir == ignoreData {
			continue
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			continue
		}
		isDir := false
		if fi, _ := os.Stat(dir); fi != nil {
			isDir = fi.IsDir()
		}
		if !isDir {
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(dir), "."))
			if !audioExt[ext] && !coverExt[ext] {
				continue
			}
			fileType := "music"
			if coverExt[ext] {
				fileType = "image"
			}
			existing := db.Folder{}
			err := h.DB.Where("path = ?", dir).First(&existing).Error
			if err == nil {
				h.DB.Model(&db.Folder{}).
					Where("path = ?", dir).
					Updates(map[string]interface{}{
						"name":           filepath.Base(dir),
						"file_type":      fileType,
						"uid":            existing.UID,
						"parent_id":      parentUID,
						"updated_at":     now,
						"state":          "updated",
						"last_scan_time": now,
					})
			} else {
				h.DB.Create(&db.Folder{
					Name: filepath.Base(dir), Path: dir, FileType: fileType,
					UID: uuid.New().String(), ParentID: parentUID,
					State: "updated", UpdatedAt: now, LastScanTime: now,
				})
			}
			continue
		}

		existing := db.Folder{}
		err := h.DB.Where("path = ?", dir).First(&existing).Error
		var existingUID string
		if err == nil {
			existingUID = existing.UID
			h.DB.Model(&db.Folder{}).
				Where("path = ?", dir).
				Updates(map[string]interface{}{
					"name":           filepath.Base(dir),
					"file_type":      "folder",
					"uid":            existing.UID,
					"parent_id":      parentUID,
					"updated_at":     now,
					"last_scan_time": now,
				})
		} else {
			existingUID = uuid.New().String()
			h.DB.Create(&db.Folder{
				Name: filepath.Base(dir), Path: dir, FileType: "folder",
				UID: existingUID, ParentID: parentUID,
				State: "updated", UpdatedAt: now, LastScanTime: now,
			})
		}
		var children [][2]string
		// SECURITY (P1.5 issue F — H5): same symlink skip as fullScan.
		// updateScan reuses the same set of paths into db.Folder, so a
		// hostile symlink could propagate a `/etc/...` path here too.
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				log.Printf("[scan] update_scan skip symlink %s/%s", dir, e.Name())
				continue
			}
			children = append(children, [2]string{existingUID, filepath.Join(dir, e.Name())})
		}
		stack = append(stack, children...)
	}
	// P1: 不执行 WHERE path NOT IN 删除。删除操作只能由 clear_music 触发。
	log.Printf("[scan] update_scan done (root=%s)", musicFolder)
	return nil
}

func (h *UpdateScanHandler) musicRoot() string {
	if h.MusicRoot != "" {
		return h.MusicRoot
	}
	if v := os.Getenv("MUSIC_DIR"); v != "" {
		return v
	}
	return "/app/media"
}
