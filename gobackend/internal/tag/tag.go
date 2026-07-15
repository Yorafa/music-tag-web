// Package tag 统一封装音频标签读取与写入。
//
// 读：以 dhowden/tag 为底层，支持 ID3v1/v2、FLAC（含 Vorbis comment）、Ogg Vorbis、MP4。
// 写：同样基于 dhowden/tag 的 SetFileTag，覆盖 title/artist/album 等基础字段；P0
//     阶段不支持 RELEASETYPE / TXXX:LANGUAGE 等私有 frame，需要时再补底层。
package tag

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Format 枚举：与 dhowden/tag 内部的 format 对应，只保留本工程真正用到的。
type Format string

const (
	FormatUnknown Format = ""
	FormatMP3     Format = "mp3"
	FormatFLAC    Format = "flac"
	FormatOGG     Format = "ogg"
	FormatMP4     Format = "mp4"
)

// TagInfo 是读取标签后返回给调用方的统一结构，字段命名贴近 Django 端 MusicIDS，
// 让前端 TagEditor / ScrapeResults 可以无缝消费。
type TagInfo struct {
	Year         int    `json:"year"`
	Comment      string `json:"comment"`
	Lyrics       string `json:"lyrics"`
	Duration     int    `json:"duration"`     // 秒，向下取整
	Size         int    `json:"size"`         // 字节
	BitRate      int    `json:"bit_rate"`     // kbps
	TrackNumber  string `json:"tracknumber"`
	DiscNumber   string `json:"discnumber"`
	Artwork      string `json:"artwork"`      // data URI (jpeg/png) — 兼容 Django 前端约定
	ArtworkW     int    `json:"artwork_w"`    // 封面宽高，便于前端表格展示
	ArtworkH     int    `json:"artwork_h"`
	ArtworkSize  int    `json:"artwork_size"` // MB
	Title        string `json:"title"`
	Artist       string `json:"artist"`
	Album        string `json:"album"`
	AlbumType    string `json:"album_type"`
	Genre        string `json:"genre"`
	Filename     string `json:"filename"`
	AlbumArtist  string `json:"albumartist"`
	Language     string `json:"language"`
	Codec        string `json:"codec"`
}

// TagUpdate 是写入时的可空字段集合。字段为空表示「保持原值不写」。
// Artist/DiscNumber/TrackNumber 这种需要「半空」处理的字段另取出来，使用单独的 bool。
type TagUpdate struct {
	Title        *string  `json:"title,omitempty"`
	Artist       []string `json:"artist,omitempty"` // 多个艺术家用逗号分隔
	Album        *string  `json:"album,omitempty"`
	AlbumArtist  *string  `json:"albumartist,omitempty"`
	TrackNumber  *string  `json:"tracknumber,omitempty"` // "1/12"
	DiscNumber   *string  `json:"discnumber,omitempty"`  // "1/2"
	Genre        *string  `json:"genre,omitempty"`
	Year         *string  `json:"year,omitempty"`
	Lyrics       *string  `json:"lyrics,omitempty"`
	Comment      *string  `json:"comment,omitempty"`
	AlbumType    *string  `json:"album_type,omitempty"`
	Language     *string  `json:"language,omitempty"`
	ClearLyrics  bool     `json:"clear_lyrics,omitempty"`
	AlbumImg     []byte   `json:"-"` // 二进制封面图，URL/base64 由调用方预处理
}

// ProbeFile 用文件头嗅探音频格式（mp3/flac/ogg/mp4）。
// 嗅探失败时回退为扩展名。
func ProbeFile(path string) Format {
	f, err := os.Open(path)
	if err != nil {
		return formatFromExt(path)
	}
	defer f.Close()

	// 嗅探需要至少 12 字节：fLaC(4) + OggS(4) + ftyp(4)
	head := make([]byte, 16)
	n, _ := f.Read(head)
	head = head[:n]

	switch {
	case bytes.HasPrefix(head, []byte("fLaC")):
		return FormatFLAC
	case bytes.HasPrefix(head, []byte("OggS")):
		return FormatOGG
	case bytes.HasPrefix(head, []byte("ID3")):
		return FormatMP3
	case n >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		return FormatMP4
	case n >= 2 && head[0] == 0xFF && (head[1]&0xE0) == 0xE0:
		// MPEG frame sync：11 位全 1 + 2 位版本号（11/10/01/00）
		return FormatMP3
	}
	return formatFromExt(path)
}

func formatFromExt(path string) Format {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "mp3":
		return FormatMP3
	case "flac":
		return FormatFLAC
	case "ogg", "opus":
		return FormatOGG
	case "m4a", "mp4":
		return FormatMP4
	}
	return FormatUnknown
}

// 检测时若 fail，扩展名同样识别不到时给 Unknown，让上层放弃读写。
// Format map 用来给 codec string 在四种格式下分别赋值。
func CodecName(fmt Format) string {
	switch fmt {
	case FormatMP3:
		return "mp3"
	case FormatFLAC:
		return "flac"
	case FormatOGG:
		return "vorbis"
	case FormatMP4:
		return "mp4a"
	}
	return string(fmt)
}

// WithSizeMB 把字节数换算成 MB（保留 2 位小数）。
func SizeMB(byteSize int64) float64 {
	return float64(byteSize) / 1024 / 1024
}

// FileSizeMB 方便前端 to_dict 字段换算。
func FileSizeMB(path string) float64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return SizeMB(info.Size())
}

// MimeToExt 反向查常见 mime → 扩展名。
func MimeToExt(mimeType string) string {
	exts, _ := mime.ExtensionsByType(mimeType)
	if len(exts) > 0 {
		return strings.TrimPrefix(exts[0], ".")
	}
	switch mimeType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	}
	return ""
}

// ReadPicture 从 io.Reader 读取至字节数组，返回 data + mime 推断。
func ReadPicture(r io.Reader) ([]byte, string, error) {
	if r == nil {
		return nil, "", errors.New("nil reader")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("read picture: %w", err)
	}
	mimeType := http.DetectContentType(data)
	return data, mimeType, nil
}
