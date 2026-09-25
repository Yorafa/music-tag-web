package tag

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bogem/id3v2"
	"go.senan.xyz/taglib"

	"go-music-tag/internal/utils"
)

// WriteSidecar: 「is_save_lyrics_file」「is_save_album_cover」等开关行为。
type WriteSidecar struct {
	SaveLrcFile    bool
	SaveAlbumCover bool
	Album          string
}

// WriteResult 写完返回的信息（供前端/HTTP 显示状态）。
type WriteResult struct {
	Written        bool   `json:"written"`
	Renamed        bool   `json:"renamed"`
	NewPath        string `json:"new_path,omitempty"`
	SidecarLyrics  string `json:"sidecar_lyrics,omitempty"`
	SidecarCover   string `json:"sidecar_cover,omitempty"`
	CoverDownsized bool   `json:"cover_downsized,omitempty"`
}

// Write 按文件格式写入音频标签。
// 支持主流格式：MP3, FLAC, OGG, Opus, M4A/MP4, WAV, WMA, AIFF 等。
func Write(path string, upd *TagUpdate) error {
	if upd == nil {
		return nil
	}

	// 优先使用 taglib 统一写入各种主流格式（OGG, FLAC, M4A, MP3, WAV 等）
	err := writeWithTagLib(path, upd)
	if err == nil {
		return nil
	}

	// 如果是 MP3 且 taglib 失败，回退尝试 bogem/id3v2 原生写入
	format := ProbeFile(path)
	if format == FormatMP3 {
		if mp3Err := writeMP3(path, upd); mp3Err == nil {
			return nil
		}
	}

	return fmt.Errorf("write audio tags for %s: %w", filepath.Base(path), err)
}

func writeWithTagLib(path string, upd *TagUpdate) error {
	tags := make(map[string][]string)

	if upd.Title != nil {
		tags[taglib.Title] = []string{strings.TrimSpace(*upd.Title)}
	}
	if len(upd.Artist) > 0 {
		var validArtists []string
		for _, a := range upd.Artist {
			if trimmed := strings.TrimSpace(a); trimmed != "" {
				validArtists = append(validArtists, trimmed)
			}
		}
		if len(validArtists) > 0 {
			tags[taglib.Artist] = validArtists
		}
	}
	if upd.Album != nil {
		tags[taglib.Album] = []string{strings.TrimSpace(*upd.Album)}
	}
	if upd.AlbumArtist != nil {
		tags[taglib.AlbumArtist] = []string{strings.TrimSpace(*upd.AlbumArtist)}
	}
	if upd.Genre != nil {
		tags[taglib.Genre] = []string{strings.TrimSpace(*upd.Genre)}
	}
	if upd.Year != nil && strings.TrimSpace(*upd.Year) != "" {
		tags[taglib.Date] = []string{strings.TrimSpace(*upd.Year)}
	}
	if upd.TrackNumber != nil && strings.TrimSpace(*upd.TrackNumber) != "" {
		tags[taglib.TrackNumber] = []string{strings.TrimSpace(*upd.TrackNumber)}
	}
	if upd.DiscNumber != nil && strings.TrimSpace(*upd.DiscNumber) != "" {
		tags[taglib.DiscNumber] = []string{strings.TrimSpace(*upd.DiscNumber)}
	}
	if upd.Comment != nil {
		tags[taglib.Comment] = []string{strings.TrimSpace(*upd.Comment)}
	}
	if upd.ClearLyrics {
		tags[taglib.Lyrics] = []string{""}
	} else if upd.Lyrics != nil {
		tags[taglib.Lyrics] = []string{*upd.Lyrics}
	}

	if len(tags) > 0 {
		if err := taglib.WriteTags(path, tags, 0); err != nil {
			return fmt.Errorf("taglib write tags: %w", err)
		}
	}

	// 嵌入式封面写入
	if len(upd.AlbumImg) > 0 {
		if err := taglib.WriteImage(path, upd.AlbumImg); err != nil {
			// 图片写入若失败，不阻断文本标签保存
			_ = err
		}
	}

	return nil
}

func writeMP3(path string, upd *TagUpdate) error {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("open id3v2: %w", err)
	}
	defer tag.Close()

	if upd.Title != nil {
		tag.AddTextFrame("TIT2", id3v2.EncodingUTF8, strings.TrimSpace(*upd.Title))
	}
	if upd.Artist != nil {
		tag.AddTextFrame("TPE1", id3v2.EncodingUTF8, strings.Join(upd.Artist, ", "))
	}
	if upd.Album != nil {
		tag.AddTextFrame("TALB", id3v2.EncodingUTF8, strings.TrimSpace(*upd.Album))
	}
	if upd.AlbumArtist != nil {
		tag.AddTextFrame("TPE2", id3v2.EncodingUTF8, strings.TrimSpace(*upd.AlbumArtist))
	}
	if upd.Genre != nil {
		tag.AddTextFrame("TCON", id3v2.EncodingUTF8, strings.TrimSpace(*upd.Genre))
	}
	if upd.Comment != nil {
		tag.AddCommentFrame(id3v2.CommentFrame{
			Encoding:    id3v2.EncodingUTF8,
			Language:    "chi",
			Description: "",
			Text:        strings.TrimSpace(*upd.Comment),
		})
	}
	if upd.Year != nil {
		// ID3v2.4 用 TDRC, ID3v2.3 用 TYER。bogem/open 按 v2.3 解析，先写 TDRC 兼容性最好。
		tag.AddTextFrame("TDRC", id3v2.EncodingUTF8, strings.TrimSpace(*upd.Year))
	}
	if upd.TrackNumber != nil {
		n, total := parseTrack(*upd.TrackNumber)
		val := strconv.Itoa(n)
		if total > 0 {
			val = val + "/" + strconv.Itoa(total)
		}
		if n > 0 || total > 0 {
			tag.AddTextFrame("TRCK", id3v2.EncodingUTF8, val)
		}
	}
	if upd.DiscNumber != nil {
		n, total := parseTrack(*upd.DiscNumber)
		val := strconv.Itoa(n)
		if total > 0 {
			val = val + "/" + strconv.Itoa(total)
		}
		if n > 0 || total > 0 {
			tag.AddTextFrame("TPOS", id3v2.EncodingUTF8, val)
		}
	}
	// 歌词
	if upd.ClearLyrics {
		tag.DeleteFrames("USLT")
	} else if upd.Lyrics != nil {
		tag.AddUnsynchronisedLyricsFrame(id3v2.UnsynchronisedLyricsFrame{
			Encoding:          id3v2.EncodingUTF8,
			Language:          "chi",
			ContentDescriptor: "",
			Lyrics:            *upd.Lyrics,
		})
	}
	// 封面
	if len(upd.AlbumImg) > 0 {
		tag.DeleteFrames("APIC")
		mime := http.DetectContentType(upd.AlbumImg)
		tag.AddAttachedPicture(id3v2.PictureFrame{
			Encoding:    id3v2.EncodingISO,
			MimeType:    mime,
			PictureType: id3v2.PTFrontCover,
			Picture:     upd.AlbumImg,
		})
	}

	if err := tag.Save(); err != nil {
		return fmt.Errorf("id3v2 save: %w", err)
	}
	return nil
}

// parseTrack "1/12" → (1, 12) or "3" → (3, 0). 失败 → (0, 0)。
func parseTrack(s string) (int, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0
	}
	parts := strings.Split(s, "/")
	n, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	if len(parts) == 1 {
		return n, 0
	}
	t, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
	return n, t
}

// HandleSidecars 处理 is_save_lyrics_file / is_save_album_cover。
func HandleSidecars(path string, upd *TagUpdate, sc *WriteSidecar) (*WriteResult, error) {
	res := &WriteResult{}
	if sc == nil {
		return res, nil
	}
	base := strings.TrimSuffix(path, filepath.Ext(path))
	dir := filepath.Dir(path)

	if sc.SaveLrcFile {
		lyrics := ""
		if upd != nil && upd.Lyrics != nil {
			lyrics = *upd.Lyrics
		}
		if lyrics != "" {
			lrcPath := base + ".lrc"
			if err := os.WriteFile(lrcPath, []byte(lyrics), 0o644); err != nil {
				return res, fmt.Errorf("write .lrc sidecar: %w", err)
			}
			res.SidecarLyrics = lrcPath
		}
	}

	if sc.SaveAlbumCover {
		var raw []byte
		var mimeType string
		if upd != nil && len(upd.AlbumImg) > 0 {
			raw = upd.AlbumImg
			mimeType = http.DetectContentType(raw)
		} else {
			info, err := Read(path)
			if err != nil || info.Artwork == "" {
				return res, nil
			}
			r, m, err := DecodePictureBase64(info.Artwork)
			if err != nil || len(r) == 0 {
				return res, nil
			}
			raw = r
			mimeType = m
		}
		if len(raw) == 0 {
			return res, nil
		}
		albumName := sanitizeFileName(sc.Album)
		if albumName == "" {
			albumName = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		// SECURITY (P1.5 issue F — H1): defence-in-depth. sanitizeFileName
		// already replaces "/" and "\", so a cover filename cannot carry a
		// `..` traversal segment through filepath.Join — but this layer
		// tightens further: drop any segment that survives Base() as "." /
		// ".." / leading-dot, and let SafeJoin enforce containment so a
		// future regression in sanitizeFileName fails closed instead of
		// leaking a cover image outside `dir`.
		cleanAlbum := filepath.Base(albumName)
		if cleanAlbum == "" || cleanAlbum == "." || cleanAlbum == ".." || strings.HasPrefix(cleanAlbum, ".") {
			return res, fmt.Errorf("write cover sidecar: unsafe album name %q", sc.Album)
		}
		albumName = cleanAlbum
		coverPath, err := utils.SafeJoin(dir, "cover-"+albumName+"."+MimeToExt(mimeType))
		if err != nil {
			return res, fmt.Errorf("write cover sidecar: %w", err)
		}
		_ = os.Remove(coverPath)
		if err := os.WriteFile(coverPath, raw, 0o644); err != nil {
			return res, fmt.Errorf("write cover sidecar: %w", err)
		}
		res.SidecarCover = coverPath
	}
	return res, nil
}

// sanitizeFileName 去除文件系统敏感字符。
func sanitizeFileName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	bad := []rune{'/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t'}
	for _, r := range bad {
		s = strings.ReplaceAll(s, string(r), "_")
	}
	return s
}

// EncodePictureToDataURI 把 []byte 编码为 "data:image/...;base64,..."。
func EncodePictureToDataURI(data []byte, mimeType string) string {
	if len(data) == 0 {
		return ""
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))
}
