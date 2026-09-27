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

	// Refuse to touch a file that is not audio.
	//
	// taglib.WriteTags does not validate the container: handed a text file
	// named bogus.mp3 it prepends a well-formed ID3v2 header and returns
	// nil, so the caller sees a successful tag save on a file whose audio
	// is now behind a fabricated frame. The old code only reached this
	// path by accident, and the write was silently destructive.
	if err := ensureAudioFile(path); err != nil {
		return err
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

// ensureAudioFile rejects paths that do not hold a decodable audio stream.
//
// It reuses Read's readability rule (a non-zero duration from taglib,
// because that is the one property ReadProperties cannot fake from a file
// extension) rather than inventing a second, weaker check. Sharing the
// predicate is the point: if the two ever disagree, a file the editor
// refuses to open is a file the editor can still overwrite.
func ensureAudioFile(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if st.Size() == 0 {
		return fmt.Errorf("%w: %s: file is empty",
			ErrUnsupportedFormat, filepath.Base(path))
	}
	props, err := taglib.ReadProperties(path)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrUnsupportedFormat, filepath.Base(path), err)
	}
	if props.Length <= 0 {
		return fmt.Errorf("%w: %s: no audio stream found",
			ErrUnsupportedFormat, filepath.Base(path))
	}
	return nil
}

func writeWithTagLib(path string, upd *TagUpdate) error {
	tags := make(map[string][]string)

	// Clear* 优先于同名写入：两者同时出现时以清除为准，因为「先写再删」
	// 与「删」的结果一样，而先删再写会把清除悄悄吃掉。
	//
	// 清除在 taglib 侧只能靠写空值实现（taglib 没有删除单个 tag 的 API），
	// 这与 ClearLyrics 当年的做法一致，也是唯一能让所有格式表现一致的
	// 方式：MP3 走下面的 id3v2 分支，DeleteFrames 是真的删 frame。
	if upd.ClearTitle {
		tags[taglib.Title] = []string{""}
	} else if upd.Title != nil {
		tags[taglib.Title] = []string{strings.TrimSpace(*upd.Title)}
	}
	if upd.ClearArtist {
		tags[taglib.Artist] = []string{""}
	} else if len(upd.Artist) > 0 {
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
	if upd.ClearAlbum {
		tags[taglib.Album] = []string{""}
	} else if upd.Album != nil {
		tags[taglib.Album] = []string{strings.TrimSpace(*upd.Album)}
	}
	if upd.ClearAlbumArtist {
		tags[taglib.AlbumArtist] = []string{""}
	} else if upd.AlbumArtist != nil {
		tags[taglib.AlbumArtist] = []string{strings.TrimSpace(*upd.AlbumArtist)}
	}
	if upd.ClearGenre {
		tags[taglib.Genre] = []string{""}
	} else if upd.Genre != nil {
		tags[taglib.Genre] = []string{strings.TrimSpace(*upd.Genre)}
	}
	if upd.ClearYear {
		tags[taglib.Date] = []string{""}
	} else if upd.Year != nil && strings.TrimSpace(*upd.Year) != "" {
		tags[taglib.Date] = []string{strings.TrimSpace(*upd.Year)}
	}
	if upd.ClearTrackNumber {
		tags[taglib.TrackNumber] = []string{""}
	} else if upd.TrackNumber != nil && strings.TrimSpace(*upd.TrackNumber) != "" {
		tags[taglib.TrackNumber] = []string{strings.TrimSpace(*upd.TrackNumber)}
	}
	if upd.ClearDiscNumber {
		tags[taglib.DiscNumber] = []string{""}
	} else if upd.DiscNumber != nil && strings.TrimSpace(*upd.DiscNumber) != "" {
		tags[taglib.DiscNumber] = []string{strings.TrimSpace(*upd.DiscNumber)}
	}
	if upd.ClearComment {
		tags[taglib.Comment] = []string{""}
	} else if upd.Comment != nil {
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

	// Embedded cover art.
	//
	// This used to swallow the error (`_ = err`) on the theory that a
	// failed image write should not block the text tags. That is the wrong
	// trade here: HandleSidecars reads the cover back out of the file to
	// write a sidecar, so a silently-failed cover means the next save
	// writes a *stale* sidecar from the old artwork while reporting
	// success. The tags are already committed at this point, so returning
	// an error costs the caller a warning, not the text-tag write.
	if len(upd.AlbumImg) > 0 {
		if err := taglib.WriteImage(path, upd.AlbumImg); err != nil {
			return fmt.Errorf("taglib write image: %w", err)
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

	// MP3 这条路径能真的删 frame，所以清除在这里是真删除而不是写空值。
	// 两个分支对同一个 Clear* 的行为不同（taglib 写空、id3v2 删 frame），
	// 但对「读完这个 tag 应该是空的」这个可观察结果一致。
	if upd.ClearTitle {
		tag.DeleteFrames("TIT2")
	} else if upd.Title != nil {
		tag.AddTextFrame("TIT2", id3v2.EncodingUTF8, strings.TrimSpace(*upd.Title))
	}
	if upd.ClearArtist {
		tag.DeleteFrames("TPE1")
	} else if upd.Artist != nil {
		tag.AddTextFrame("TPE1", id3v2.EncodingUTF8, strings.Join(upd.Artist, ", "))
	}
	if upd.ClearAlbum {
		tag.DeleteFrames("TALB")
	} else if upd.Album != nil {
		tag.AddTextFrame("TALB", id3v2.EncodingUTF8, strings.TrimSpace(*upd.Album))
	}
	if upd.ClearAlbumArtist {
		tag.DeleteFrames("TPE2")
	} else if upd.AlbumArtist != nil {
		tag.AddTextFrame("TPE2", id3v2.EncodingUTF8, strings.TrimSpace(*upd.AlbumArtist))
	}
	if upd.ClearGenre {
		tag.DeleteFrames("TCON")
	} else if upd.Genre != nil {
		tag.AddTextFrame("TCON", id3v2.EncodingUTF8, strings.TrimSpace(*upd.Genre))
	}
	if upd.ClearComment {
		tag.DeleteFrames("COMM")
	} else if upd.Comment != nil {
		tag.AddCommentFrame(id3v2.CommentFrame{
			Encoding:    id3v2.EncodingUTF8,
			Language:    "chi",
			Description: "",
			Text:        strings.TrimSpace(*upd.Comment),
		})
	}
	if upd.ClearYear {
		tag.DeleteFrames("TDRC")
	} else if upd.Year != nil {
		// ID3v2.4 用 TDRC, ID3v2.3 用 TYER。bogem/open 按 v2.3 解析，先写 TDRC 兼容性最好。
		tag.AddTextFrame("TDRC", id3v2.EncodingUTF8, strings.TrimSpace(*upd.Year))
	}
	if upd.ClearTrackNumber {
		tag.DeleteFrames("TRCK")
	} else if upd.TrackNumber != nil {
		n, total := parseTrack(*upd.TrackNumber)
		val := strconv.Itoa(n)
		if total > 0 {
			val = val + "/" + strconv.Itoa(total)
		}
		if n > 0 || total > 0 {
			tag.AddTextFrame("TRCK", id3v2.EncodingUTF8, val)
		}
	}
	if upd.ClearDiscNumber {
		tag.DeleteFrames("TPOS")
	} else if upd.DiscNumber != nil {
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
			lrcPath := LyricsSidecarName(base)
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
