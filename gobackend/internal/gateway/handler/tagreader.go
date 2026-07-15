// Package handler 标签读取入口。
//
// 主要入口 MusicID3 由 file.go 调用；MusicIDS 接口字段对齐 Django。
// 与 worker / scan_utils 后接入兼容：保留 ReadMusicTags 函数。
package handler

import (
	"github.com/gin-gonic/gin"

	"go-music-tag/internal/tag"
)

// ReadMusicTags 把 internal/tag.Read 输出转为 map[string]interface{}，
// 兼容老调用方习惯返回 map 的接口。
func ReadMusicTags(filePath string) (map[string]interface{}, error) {
	info, err := tag.Read(filePath)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"year":         info.Year,
		"comment":      info.Comment,
		"lyrics":       info.Lyrics,
		"duration":     info.Duration,
		"size":         info.Size,
		"bit_rate":     info.BitRate,
		"tracknumber":  info.TrackNumber,
		"discnumber":   info.DiscNumber,
		"artwork":      info.Artwork,
		"artwork_w":    info.ArtworkW,
		"artwork_h":    info.ArtworkH,
		"artwork_size": info.ArtworkSize,
		"title":        info.Title,
		"artist":       info.Artist,
		"album":        info.Album,
		"album_type":   info.AlbumType,
		"genre":        info.Genre,
		"filename":     info.Filename,
		"albumartist":  info.AlbumArtist,
		"language":     info.Language,
	}, nil
}

// 占位符：保持文件不空 (某些编译目标会抱怨无内容)。后续 svc 接入后删除。
var _ = gin.New
