package dedup

import (
	"path/filepath"
	"strings"

	"go-music-tag/internal/audioext"
	"go-music-tag/internal/utils"
)

// Deciding which music_folder rows are library audio.
//
// The obvious query is `file_type = 'music'`, and it was the query for
// months — but `file_type` is written by three producers that do not agree
// on what it means:
//
//	scanner.go    "music" / "image" / "folder"   (by extension)
//	yt_dl.go      payload.Source — "youtube", "netease", … (by download source)
//
// A track fetched with 加入库 lands in MUSIC_DIR with file_type='youtube',
// so every query filtering on 'music' silently excluded it: it never got a
// duration, so it was never a candidate for anyone else's fingerprint
// comparison, so a re-encoded twin of it was never found. Nothing logged
// anything, because from each query's point of view the row simply was not
// there.
//
// So classification is done from the path, using audioext — the one package
// that is already the single source of truth for "is this audio" — instead
// of from a column that three writers spell differently. file_type is
// retained as a cheap pre-filter for the two values that are definitely not
// audio; a NULL or unknown value is not excluded, because "we did not
// recognise the writer" is not evidence of "not audio".

// audioRowScope is the shared WHERE fragment every library-audio query
// uses: exclude the two file_types that are unambiguously not audio, and
// exclude directories by size (a folder row carries the directory's own
// size, which is > 0, so file_type alone is not enough).
const audioRowScope = `file_type NOT IN ('folder', 'image') AND size > 0`

// libraryAudioCond returns the SQL condition and args identifying library
// audio rows in music_folder.
//
// It is deliberately NOT an extension filter in SQL. SQLite's LIKE would
// have to spell out all fourteen extensions as a chain of ORs, which is a
// fourth copy of the audioext list and would drift from it the moment
// someone adds a format. Instead the query is widened to "not a folder, not
// an image, has a size" and the extension check is applied to the handful of
// rows that survive, where audioext.IsLibraryPath is the authority.
func libraryAudioCond() (string, []interface{}) {
	return audioRowScope, nil
}

// isLibraryAudioRow applies the extension half of the classification to a
// path that SQL already narrowed down.
func isLibraryAudioRow(path string) bool {
	if path == "" {
		return false
	}
	// A directory row is excluded by size in SQL, but a directory that
	// somehow carries a library extension ("weird.flac/") must not be
	// offered as a duplicate candidate either.
	if strings.HasSuffix(path, string(filepath.Separator)) {
		return false
	}
	return audioext.IsLibraryPath(path)
}

// underMusicRoot reports whether an indexed path is inside the library.
//
// This is the constraint that keeps the download cache out of duplicate
// detection, and it is a stronger statement than any file_type value: the
// per-source cache lives in AUDIO_CACHE_DIR (/tmp/audio_cache), which is not
// under MUSIC_DIR at all, and the file there is byte-identical to the track
// the user just downloaded on purpose. Treating it as a library duplicate
// would report "already in your library" about a file that is not in the
// library, and the next sweep would remove the cache copy the library entry
// was pointing at.
//
// It is a path check rather than a file_type check because the cache's
// file_type is whatever the downloader wrote ('youtube', 'netease', …) —
// the same drift that hid real library tracks from this query in the first
// place. Root containment does not drift.
//
// The test itself is utils.UnderRoot, shared with the fingerprint indexer
// and the pruner. It was once spelled out here, and the three copies
// disagreed: the indexer kept filtering on file_type and so indexed the
// cache, spending 0.4s a file on rows nothing would ever query.
func underMusicRoot(root, path string) bool {
	if path == "" {
		return false
	}
	if root == "" {
		// No configured root: there is nothing to be inside of, and the
		// disk-walk stages have the same limitation. Refusing every row
		// would silently disable the index entirely.
		return true
	}
	return utils.UnderRoot(root, path)
}

// libraryAudioRows runs `cond` against music_folder restricted to rows that
// are plausibly library audio, then filters the survivors by extension and
// by root containment.
func (c *Checker) libraryAudioRows(limit int, cond string, args ...interface{}) []string {
	if c.db == nil {
		return nil
	}
	if limit <= 0 {
		limit = indexCandidateLimit
	}
	scope, _ := libraryAudioCond()
	var paths []string
	err := c.db.Table("music_folder").
		Where(scope).
		Where(cond, args...).
		Limit(limit).
		Pluck("path", &paths).Error
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !isLibraryAudioRow(p) {
			continue
		}
		if abs := resolveUnderRoot(c.musicRoot, p); abs != "" && underMusicRoot(c.musicRoot, abs) {
			out = append(out, abs)
		}
	}
	return out
}
