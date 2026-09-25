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
	"gorm.io/gorm/clause"

	"go-music-tag/internal/audioext"
	"go-music-tag/internal/db"
)

// coverExt 与 applications/task/constants.go 对齐。
//
// The former `audioExt` literal here was the third of four copies of the
// audio-extension whitelist; it now lives in internal/audioext so the
// scanner can never diverge from the file browser / tag writer again
// (REVIEW.md P2-1). Use audioext.IsLibraryExt for audio checks.
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
	// REVIEW.md P2-5: a second full scan used to INSERT a brand-new uid for
	// every path, doubling the table each time. Reuse the uid already on
	// disk so the folder tree keeps its identity, and let flushBatch's
	// upsert refresh the row in place.
	uidByPath := loadFolderUIDs(h.DB)
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
			myUID := uidForPath(uidByPath, dir)
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
					filePath := filepath.Join(dir, e.Name())
					fileUID := uidForPath(uidByPath, filePath)
					ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))
					if audioext.IsLibraryExt(ext) {
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
			myUID := uidForPath(uidByPath, dir)
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(dir), "."))
			if audioext.IsLibraryExt(ext) {
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

// loadFolderUIDs returns the existing path -> uid mapping, or an empty map
// if the table cannot be read. A failure here is not fatal: the scan then
// just mints fresh uids, which is what it always did — the upsert in
// flushBatch keeps the table from growing regardless.
func loadFolderUIDs(conn *gorm.DB) map[string]string {
	out := make(map[string]string)
	if conn == nil {
		return out
	}
	var rows []struct {
		Path string
		UID  string
	}
	if err := conn.Model(&db.Folder{}).Select("path", "uid").Find(&rows).Error; err != nil {
		log.Printf("[scan] preload folder uids: %v", err)
		return out
	}
	for _, r := range rows {
		if r.UID != "" {
			out[r.Path] = r.UID
		}
	}
	return out
}

// uidForPath returns a stable uid for path, minting and remembering one on
// first sight. Remembering intra-scan matters too: a caller-supplied
// subPaths list that names the same directory twice would otherwise produce
// two rows fighting over the unique index.
func uidForPath(m map[string]string, path string) string {
	if uid, ok := m[path]; ok && uid != "" {
		return uid
	}
	uid := uuid.New().String()
	m[path] = uid
	return uid
}

// flushBatch writes a batch of folder rows, refreshing any path that is
// already on disk rather than inserting beside it (REVIEW.md P2-5).
//
// `uid` is deliberately absent from the update list: the uid is the row's
// identity, children point at it, and the caller has just re-read it from
// disk. `state` is absent for the same reason — it is owned by
// updateScan ("updated"), and a full scan stamping "scanning" over it would
// discard the more meaningful value.
func flushBatch(conn *gorm.DB, rows []db.Folder) {
	if len(rows) == 0 || conn == nil {
		return
	}
	if err := conn.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "path"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "size", "file_type", "parent_id", "updated_at", "last_scan_time"}),
	}).CreateInBatches(rows, 500).Error; err != nil {
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
		// Type-check BEFORE ReadDir (REVIEW.md P2-4). ReadDir on a file
		// always fails with ENOTDIR, and the old ordering did
		// `entries, err := os.ReadDir(dir); if err != nil { continue }`
		// first — so a file path never reached the !isDir branch below and
		// the whole "file entry already popped from its parent" case was
		// dead code. Incremental scans therefore only ever recorded
		// directory rows.
		//
		// The Stat error is also handled honestly: an unstattable path
		// skips, rather than falling through to the file branch the way
		// the old `if fi, _ := os.Stat(dir); fi != nil` did.
		fi, statErr := os.Stat(dir)
		if statErr != nil {
			continue
		}
		if !fi.IsDir() {
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(dir), "."))
			if !audioext.IsLibraryExt(ext) && !coverExt[ext] {
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

		// Reached only when dir really is a directory.
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
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
