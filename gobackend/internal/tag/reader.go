package tag

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	dhtag "github.com/dhowden/tag"
)

// ErrUnsupportedFormat 出现在 Read 找不到文件/格式无法解析时。
var ErrUnsupportedFormat = errors.New("tag: unsupported audio format")

// Read 从指定音频文件读取全部标签字段 + 元数据（duration/bitrate/size/codec）。
// 底层调 dhowden/tag.ReadFrom，由 Identify 自动检测 mp3/flac/ogg/mp4。
func Read(path string) (*TagInfo, error) {
	cfg, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	format := ProbeFile(path)
	codec := CodecName(format)

	info := &TagInfo{
		Filename: filepath.Base(path),
		Size:     int(cfg.Size()),
		Codec:    codec,
	}

	f, err := os.Open(path)
	if err != nil {
		return info, nil
	}
	defer f.Close()

	md, err := dhtag.ReadFrom(f)
	if err != nil {
		// 文件存在但解析失败：仍返回元数据，与 Django MusicIDS 行为一致
		info.Title = strings.TrimSuffix(info.Filename, filepath.Ext(info.Filename))
		return info, nil
	}

	info.Title = md.Title()
	info.Artist = md.Artist()
	info.Album = md.Album()
	info.Genre = strings.TrimSpace(md.Genre())
	info.Comment = md.Comment()
	info.Lyrics = md.Lyrics()
	info.TrackNumber = formatNT(md.Track())
	info.DiscNumber = formatNT(md.Disc())

	if y := md.Year(); y > 0 {
		info.Year = y
	}

	if pic := md.Picture(); pic != nil {
		info.Artwork = fmt.Sprintf("data:%s;base64,%s",
			normalizeMime(pic.MIMEType),
			base64.StdEncoding.EncodeToString(pic.Data))
		// dhowden/tag.Picture 没有 Width/Height，直接从原始 bytes sniff JPEG SOF
		w, h := sniffJPEGWH(pic.Data)
		if w > 0 {
			info.ArtworkW = w
		}
		if h > 0 {
			info.ArtworkH = h
		}
		info.ArtworkSize = int(SizeMB(int64(len(pic.Data))))
	}

	info.Duration = estimateDuration(path, format, cfg.Size())
	info.BitRate = estimateBitrate(path, format, cfg.Size())

	return info, nil
}

// formatNT 把 (num, total) 格式化成 "3" 或 "3/12"。两个值均为 0/-1 时返回空串。
func formatNT(num, total int) string {
	if num <= 0 && total <= 0 {
		return ""
	}
	if num <= 0 && total > 0 {
		return "0/" + strconv.Itoa(total)
	}
	if total > 0 {
		return strconv.Itoa(num) + "/" + strconv.Itoa(total)
	}
	return strconv.Itoa(num)
}

func normalizeMime(t string) string {
	if t == "" {
		return "image/jpeg"
	}
	return t
}

// sniffJPEGWH 通过解析 JPEG SOF0/SOF2 帧直接获取宽高。
func sniffJPEGWH(data []byte) (int, int) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, 0
	}
	for i := 2; i+9 < len(data); {
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			i += 2
			continue
		}
		if i+4 >= len(data) {
			return 0, 0
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if marker == 0xC0 || marker == 0xC2 {
			if i+9 >= len(data) {
				return 0, 0
			}
			h := int(data[i+5])<<8 | int(data[i+6])
			w := int(data[i+7])<<8 | int(data[i+8])
			return w, h
		}
		i += 2 + segLen
	}
	return 0, 0
}

func estimateDuration(path string, format Format, size int64) int {
	if size <= 0 {
		return 0
	}
	br := estimateBitrate(path, format, size)
	if br <= 0 {
		return 0
	}
	return int(float64(size) * 8 / float64(br) / 1000)
}

func estimateBitrate(path string, format Format, _ int64) int {
	switch format {
	case FormatMP3:
		return 192
	case FormatFLAC:
		return 1000
	case FormatOGG:
		return 160
	case FormatMP4:
		return 256
	}
	return 0
}

// DecodePictureBase64 与前端约定：返回带 "data:..." 前缀的字符串时的还原。
func DecodePictureBase64(s string) ([]byte, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, "", nil
	}
	mimeType := "image/jpeg"
	if strings.HasPrefix(s, "data:") {
		end := strings.Index(s, ";base64,")
		if end < 0 {
			return nil, "", errors.New("malformed data URI")
		}
		mimeType = s[5:end]
		s = s[end+8:]
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, "", fmt.Errorf("base64 decode: %w", err)
	}
	return data, mimeType, nil
}
