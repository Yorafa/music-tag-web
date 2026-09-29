// Package audioext is the single source of truth for "which file extensions
// count as audio" across the gateway, the worker, and the download cache.
//
// Before this package existed the same concept was spelled out as four
// independent literals that had drifted into three different behaviours:
//
//	handler/file.go::allowedAudioExts     15 bare exts, case-SENSITIVE
//	handler/update.go::isAudioFile        14 bare exts, lowercased
//	tasks/scanner.go::audioExt            14 bare exts, lowercased
//	handler/stream.go::filterAudioMatches  8 dotted exts (no .flac)
//	tasks/yt_dl.go::findDownloadedFile     8 dotted exts (no .flac)
//	tasks/yt_dl.go::resolveLibraryDest     9 dotted exts (with .flac)
//
// 这份清单必须被两边共用：曲库扩展名、流式缓存过滤、文件浏览器的类型图标、
// 标签编辑器的可写判定。任何一处自己抄一份，`Track.MP3` 这种大写扩展名就会
// 在某个入口看得见、在另一个入口看不见。
//
// # Two distinct sets, deliberately
//
// These are NOT the same concept and must not be merged:
//
//   - Library: containers we accept inside MUSIC_DIR for tag read/write.
//     Driven by what go.senan.xyz/taglib and dhowden/tag can parse. Includes
//     lossless/exotic formats (ape, tta, wv, dsf, dff) that no download
//     source ever produces.
//
//   - Streamable: containers that may legitimately land in the per-source
//     download cache AND that http.ServeFile can Range-serve to an <audio>
//     element. Includes container formats (webm, mkv, mp4) that yt-dlp
//     yields when no --extract-audio transcode is requested, and which we
//     therefore must be able to serve, but which we would not expect a user
//     to keep as a library track.
//
// Every predicate here lowercases its input first, so callers never have to
// remember to do it themselves — that omission was exactly the P2-2 bug.
package audioext

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// libraryExts are the bare (dot-less) extensions accepted for tag read/write
// inside MUSIC_DIR.
//
// NOTE on "wmv": the legacy Python ALLOW_TYPE list (mirrored by
// handler/file.go) also contained "wmv", a Windows Media *Video* container.
// It is intentionally absent here. Keeping it would mean the tag-write path
// starts accepting video files, which taglib cannot meaningfully write;
// before this package, file.go listed .wmv in the browser while
// isAudioFile() silently no-op'd every write to it. Dropping it from the
// listing is the smaller, more honest behaviour change: the file browser no
// longer advertises a file the editor refuses to touch.
var libraryExts = map[string]bool{
	"flac": true,
	"mp3":  true,
	"ape":  true,
	"wav":  true,
	"aiff": true,
	"wv":   true,
	"tta":  true,
	"m4a":  true,
	"ogg":  true,
	"mpc":  true,
	"opus": true,
	"wma":  true,
	"dsf":  true,
	"dff":  true,
}

// streamableExts are the dotted extensions a download-cache entry may carry.
//
// `.flac` is present because a TagSource CDN can serve lossless audio and
// resolveLibraryDest already accepted it as a destination; omitting it from
// the stream filter (the pre-fix state) meant such a file downloaded
// successfully and then could never be served.
var streamableExts = map[string]bool{
	".mp3":  true,
	".m4a":  true,
	".ogg":  true,
	".opus": true,
	".wav":  true,
	".flac": true,
	".webm": true,
	".mkv":  true,
	".mp4":  true,
}

// IsLibraryExt reports whether a bare extension (no leading dot, any case)
// is an audio container we accept inside MUSIC_DIR.
func IsLibraryExt(bare string) bool {
	return libraryExts[strings.ToLower(strings.TrimPrefix(bare, "."))]
}

// IsLibraryPath reports whether path's extension is a library audio format.
// Accepts a full path or a bare filename; case-insensitive.
func IsLibraryPath(path string) bool {
	return IsLibraryExt(filepath.Ext(path))
}

// IsStreamableExt reports whether a dotted extension (any case) may appear
// in the download cache and be Range-served.
func IsStreamableExt(dotted string) bool {
	e := strings.ToLower(dotted)
	if e != "" && !strings.HasPrefix(e, ".") {
		e = "." + e
	}
	return streamableExts[e]
}

// LibraryExtsHint renders the accepted library extensions as a stable,
// comma-separated, dot-prefixed list, for user-facing messages.
//
// The set is derived from libraryExts rather than spelled out again so the
// two cannot drift, and sorted so the output is deterministic — an error
// message that reordered itself between runs reads as a different bug.
// Kept short on purpose: this goes in an API response the UI shows verbatim,
// so it names a few representatives and counts the rest.
func LibraryExtsHint() string {
	all := make([]string, 0, len(libraryExts))
	for ext := range libraryExts {
		all = append(all, "."+ext)
	}
	sort.Strings(all)
	const shown = 8
	if len(all) <= shown {
		return strings.Join(all, " ")
	}
	return strings.Join(all[:shown], " ") +
		fmt.Sprintf(" 等共 %d 种", len(all))
}

// IsStreamablePath reports whether path's extension is cache-servable.
func IsStreamablePath(path string) bool {
	return IsStreamableExt(filepath.Ext(path))
}

// FileTypeForRow is the music_folder.file_type value for a downloaded file.
//
// This column is read by the folder scanner ("music" / "image" / "folder")
// and was also written by the downloader, which put the *source* name there —
// "youtube", "netease", … — for the very same kind of file. One column, two
// vocabularies, and the consequence was silent: every query identifying
// library audio by `file_type = 'music'` excluded every track that arrived
// through 加入库. Such a track got no duration (the indexer filtered the same
// way), so it was never a candidate for anyone else's fingerprint comparison,
// so a re-encoded twin of it was never detected. Nothing errored — the row was
// simply absent from each query's point of view.
//
// So the value is derived from the path, using the sets above as the single
// source of truth. The download source is not lost: it lives in
// TaskRecord.source, in the audit log, and in the cache path itself.
//
// Whether a row is in the *library* as opposed to the per-source download
// cache is decided by root containment, not by this column — the cache lives
// in AUDIO_CACHE_DIR, which is not under MUSIC_DIR at all.
const (
	// FileTypeMusic marks a row the library treats as audio.
	FileTypeMusic = "music"
	// FileTypeStreamable marks a cache entry we can serve but would not
	// keep as a library track (webm/mkv/mp4).
	FileTypeStreamable = "streamable"
	// FileTypeOther marks anything else (a cover, a .lrc).
	FileTypeOther = "other"
)

// FileTypeForRow classifies a downloaded file for music_folder.file_type.
func FileTypeForRow(path string) string {
	if IsLibraryPath(path) {
		return FileTypeMusic
	}
	if IsStreamablePath(path) {
		return FileTypeStreamable
	}
	return FileTypeOther
}

// audioMIMEToExt maps an upstream Content-Type to the extension we store.
//
// This replaces mime.ExtensionsByType, whose first-result-wins behaviour
// produced extensions outside streamableExts and therefore silently broke
// playback of successfully-downloaded files. Measured
// stdlib output on this platform:
//
//	audio/ogg  -> [.oga .ogg .opus]  ⇒ picked .oga  ✗ not streamable
//	audio/mp4  -> [.f4a .m4a]        ⇒ picked .f4a  ✗ not streamable
//	audio/mpeg -> [.mp3 .mpga]       ⇒ picked .mp3  ✓ correct by luck
//
// Every value in this map MUST be a member of streamableExts; that
// invariant is asserted by TestAudioMIMEToExt_AllStreamable.
var audioMIMEToExt = map[string]string{
	"audio/mpeg":       ".mp3",
	"audio/mp3":        ".mp3",
	"audio/mp4":        ".m4a",
	"audio/m4a":        ".m4a",
	"audio/x-m4a":      ".m4a",
	"audio/aac":        ".m4a",
	"audio/ogg":        ".ogg",
	"application/ogg":  ".ogg",
	"audio/vorbis":     ".ogg",
	"audio/opus":       ".opus",
	"audio/flac":       ".flac",
	"audio/x-flac":     ".flac",
	"audio/wav":        ".wav",
	"audio/wave":       ".wav",
	"audio/x-wav":      ".wav",
	"audio/webm":       ".webm",
	"video/webm":       ".webm",
	"video/mp4":        ".mp4",
	"video/x-matroska": ".mkv",
}

// ExtForMIME returns the storage extension for an already-parsed media type
// (parameters such as "; charset=utf-8" must be stripped by the caller via
// mime.ParseMediaType). Returns "" when the type is unknown, so callers can
// fall back to a URL-path extension.
func ExtForMIME(mediaType string) string {
	return audioMIMEToExt[strings.ToLower(strings.TrimSpace(mediaType))]
}

// DefaultExt is the last-resort extension when neither Content-Type nor the
// URL path identifies the container. `.mp3` is chosen because it is the most
// widely decodable of the streamable set — a mislabelled file at least has a
// chance of playing rather than being filtered out entirely.
const DefaultExt = ".mp3"
