package tag

import (
	"os"
	"path/filepath"
	"strings"
)

// lrcExt is the lyrics sidecar extension. The writer emits exactly this
// spelling (internal/tag/sidecar_write.go), and every reader here is
// case-insensitive, because a library that came off Windows or SMB can
// carry `Song.LRC` and treating it as an unknown file is the conservative
// answer but a confusing one.
const lrcExt = ".lrc"

// LyricsSidecarName returns the lyrics sidecar path for a track's audio path.
// The writer (HandleSidecars), the move path (MoveSidecars) and the delete
// path all have to agree on this spelling; a second ".lrc" literal in any of
// them is a reader that stops finding the file the app just wrote.
func LyricsSidecarName(audioPath string) string {
	return strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + lrcExt
}

// ReadLyricsSidecar returns the contents of the `<base>.lrc` sitting next to
// audioPath, or "" when there is none (the common case) or it cannot be read.
//
// The read path (tag.Read) only sees embedded lyrics; a lyric that lives only
// in a sidecar — written by an external tool, or by our own "保存到外部文件"
// option on a file where the embed did not stick — would otherwise surface as
// 暂无歌词 even though the file browser flags the track with a lyrics icon.
// This is the read-side counterpart to HandleSidecars' write.
func ReadLyricsSidecar(audioPath string) string {
	b, err := os.ReadFile(LyricsSidecarName(audioPath))
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\r\n")
}

// LyricsSidecarFor reports whether name is a lyrics sidecar and, if so, the
// track base it belongs to.
//
// The base is the name without the extension, because that is the contract:
// HandleSidecars writes `<audio base>.lrc` next to the audio, and
// MoveSidecars carries it under exactly that new base. A file that does not
// follow the convention has no owner this code can reason about.
func LyricsSidecarFor(name string) (string, bool) {
	if strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	if !strings.EqualFold(filepath.Ext(name), lrcExt) {
		return "", false
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if base == "" {
		return "", false
	}
	return base, true
}
