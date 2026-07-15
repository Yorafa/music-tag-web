// Package tasks 批量刮削 handler。Ported from
// applications/task/tasks.py `batch_auto_tag_task` 但做 P1 简化：
//
//   1. lock: 批量把 wait → processing（防多 worker 抢同一批）
//   2. 对每条 record：gRPC plugin FetchID3ByTitle → match_score → tag.Write
//   3. update state。P1 不再 fan-out 到子任务（避免引入 asynq.Client + 重复
//      inspect 状态），改为单 thread 串行处理；P2 再讨论并发子任务。
//
//   P1 已知边界：cover art 写入跳过（避免 worker 内部 HTTP fetch）；用户可走
//   /api/update_id3/ 单独写 cover。Lyrics 写入同样跳过（P1.5 再补）。
package tasks

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"go-music-tag/internal/db"
	"go-music-tag/internal/plugin"
	"go-music-tag/internal/tag"
)

// BatchAutoTagHandler 实施一次批量自动刮削。
type BatchAutoTagHandler struct {
	DB *gorm.DB
}

func (h *BatchAutoTagHandler) ProcessTask(ctx context.Context, t Task) error {
	if h.DB == nil {
		return fmt.Errorf("db not initialised")
	}
	var p BatchAutoTagPayload
	if raw, ok := t.Payload.(*BatchAutoTagPayload); ok && raw != nil {
		p = *raw
	}
	if len(p.SourceList) == 0 {
		return fmt.Errorf("batch_tag: source_list empty")
	}
	if p.Batch == "" {
		return fmt.Errorf("batch_tag: batch id empty")
	}

	// 1) lock
	if err := h.DB.Model(&db.TaskRecord{}).
		Where("batch = ? AND state = ?", p.Batch, "wait").
		Update("state", "processing").Error; err != nil {
		return fmt.Errorf("batch_tag: lock: %w", err)
	}
	// 2) fetch locked rows
	var rows []db.TaskRecord
	if err := h.DB.Where("batch = ? AND state = ?", p.Batch, "processing").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("batch_tag: fetch: %w", err)
	}

	// 3) pre-fetch one client per source; cache for the lifetime of this task
	sources := map[string]plugin.TagSource{}
	for _, src := range p.SourceList {
		client, err := plugin.GetTagSource(src)
		if err != nil {
			log.Printf("[batch_tag] source %s unavailable: %v", src, err)
			continue
		}
		sources[src] = client
	}
	if len(sources) == 0 {
		return fmt.Errorf("batch_tag: no source available")
	}
	log.Printf("[batch_tag] processing batch=%s records=%d sources=%v",
		p.Batch, len(rows), p.SourceList)

	// 4) per-record scoring + write
	processed := 0
	for _, r := range rows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := h.tagOne(ctx, p, r, sources); err != nil {
			log.Printf("[batch_tag] %s: %v", r.FullPath, err)
			h.DB.Model(&db.TaskRecord{}).
				Where("id = ?", r.ID).
				Update("state", "failed")
			continue
		}
		processed++
	}
	log.Printf("[batch_tag] batch=%s done: %d/%d", p.Batch, processed, len(rows))
	return nil
}

// tagOne 单文件完整流程：tag.Read → gRPC plugin FetchID3ByTitle → match → tag.Write。
func (h *BatchAutoTagHandler) tagOne(
	ctx context.Context, p BatchAutoTagPayload,
	r db.TaskRecord, sources map[string]plugin.TagSource,
) error {
	if r.FullPath == "" {
		return fmt.Errorf("empty full_path")
	}
	info, err := tag.Read(r.FullPath)
	if err != nil {
		return fmt.Errorf("tag.Read: %w", err)
	}
	fileTitle := strings.TrimSpace(info.Title)
	if fileTitle == "" {
		fileTitle = baseNameNoExt(r.FullPath)
	}
	fileArtist := strings.TrimSpace(info.Artist)
	fileAlbum := strings.TrimSpace(info.Album)

	// 5) 选 candidate
	var selected map[string]interface{}
	for _, client := range sources {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		songs, err := client.FetchID3ByTitle(ctx, fileTitle)
		if err != nil || len(songs) == 0 {
			continue
		}
		for _, s := range songs {
			m := songToMap(s)
			t := matchScore(fileTitle, asString(m["name"]))
			a := matchArtist(fileArtist, asString(m["artist"]))
			alb := matchScore(fileAlbum, asString(m["album"]))
			if fileArtist != "" && a == 0 {
				a = -2
			}
			if fileArtist == "" && a >= 1 && t >= 1 {
				t = 2
			}
			sum := float64(t + a + alb)
			if p.SelectMode == "simple" {
				if t == 2 {
					selected = m
					break
				}
				continue
			}
			if sum >= 3 {
				selected = m
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("no matching song across sources")
	}

	// 6) 写标签 (P1: cover/lyrics 跳过)
	selected["filename"] = filepath.Base(r.FullPath)
	selected["file_full_path"] = r.FullPath
	if err := writeSongTags(r.FullPath, selected); err != nil {
		return fmt.Errorf("tag.Write: %w", err)
	}

	// 7) 同步进 Task (Django `task_task`) + TaskRecord state
	parent := parentDir(r.FullPath)
	h.DB.Where("full_path = ?", r.FullPath).
		Attrs(db.Task{
			FullPath:   r.FullPath,
			State:      "success",
			ParentPath: parent,
			Filename:   filepath.Base(r.FullPath),
			SongName:   asString(selected["name"]),
			ArtistName: asString(selected["artist"]),
		}).
		FirstOrCreate(&db.Task{})

	return h.DB.Model(&db.TaskRecord{}).
		Where("id = ?", r.ID).
		Updates(map[string]interface{}{
			"state":       "success",
			"song_name":   asString(selected["name"]),
			"artist_name": asString(selected["artist"]),
			"tag_source":  asString(selected["source"]),
			"updated_at":  time.Now(),
		}).Error
}

// songToMap 把 plugin.Song 序列化成 map 供后续 select/write。
func songToMap(s plugin.Song) map[string]interface{} {
	return map[string]interface{}{
		"id":        s.ID,
		"name":      s.Name,
		"artist":    s.Artist,
		"artist_id": s.ArtistID,
		"album":     s.Album,
		"album_id":  s.AlbumID,
		"album_img": s.AlbumImg,
		"year":      s.Year,
		"source":    s.Source,
		"genre":     s.Genre,
		"cover":     s.Cover,
	}
}

// writeSongTags 把 candidate 写入 audio 文件 (P1: 仅写基础字段，不下 cover)。
func writeSongTags(fullPath string, m map[string]interface{}) error {
	upd := &tag.TagUpdate{}
	if v, ok := stringValue(m, "name"); ok {
		upd.Title = &v
	}
	if v, ok := stringValue(m, "artist"); ok {
		upd.Artist = []string{v}
	}
	if v, ok := stringValue(m, "album"); ok {
		upd.Album = &v
	}
	if v, ok := stringValue(m, "genre"); ok {
		upd.Genre = &v
	}
	if v, ok := stringValue(m, "year"); ok {
		upd.Year = &v
	}
	return tag.Write(fullPath, upd)
}

// stringValue 兼容 string / json.Number / 其他类型，统一返回 string。
func stringValue(m map[string]interface{}, k string) (string, bool) {
	v, ok := m[k]
	if !ok || v == nil {
		return "", false
	}
	switch s := v.(type) {
	case string:
		return s, true
	default:
		return fmt.Sprintf("%v", v), true
	}
}
