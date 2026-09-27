package tag

import (
	"os"
	"path/filepath"
	"strings"
)

// coverPrefix is the fixed head of every cover sidecar name; the album
// name and the image extension follow it.
const coverPrefix = "cover-"

// SidecarMove records the fate of one sidecar during a rename. Err is nil
// when the sidecar reached To.
type SidecarMove struct {
	From string
	To   string
	Err  error
}

// MoveSidecars relocates the sidecar files that belong to an audio file
// which has just been renamed or moved, and returns one entry per sidecar
// it tried to move. A nil slice means there was nothing to carry.
//
// It is best-effort by design. The audio file has already moved by the
// time this runs, and refusing to report the rename would leave the
// client pointing a row at a path it cannot verify. A sidecar that fails
// to move is reported so the caller can warn; the audio stays renamed.
//
// Two classes travel, and the difference is what they are keyed by:
//
//   - Track-scoped (`<base>.lrc`) follows the file even on a pure base-name
//     rename, because it is named after the track.
//   - Album-scoped (covers, album.nfo, cue sheets) only travels when the
//     DIRECTORY changes, because the file names say nothing about which
//     track they belong to. A base-name rename leaves them correctly in
//     place.
//
// Before album-scoped files were carried at all, a tidy left `album.nfo`
// behind in the old directory while the audio moved out — and since the
// pruner refuses to remove a directory holding any file, that stranded .nfo
// is exactly what kept the emptied directory alive and undeletable.
func MoveSidecars(oldPath, newPath string) []SidecarMove {
	var moves []SidecarMove

	oldBase := strings.TrimSuffix(oldPath, filepath.Ext(oldPath))
	newBase := strings.TrimSuffix(newPath, filepath.Ext(newPath))
	if m := moveSidecarFile(oldBase+".lrc", newBase+".lrc"); m != nil {
		moves = append(moves, *m)
	}

	// Album-scoped files are named after the album, not the track, so they
	// only travel when the directory changes. A base-name rename leaves them
	// exactly where they belong.
	//
	// The check is a fast path, not the correctness guard: both helpers skip
	// a from==to move, so calling them with oldDir == newDir would be a no-op.
	// It is kept because a base-name rename is the common case (从标签改名) and
	// this way it costs no ReadDir of the album folder.
	oldDir, newDir := filepath.Dir(oldPath), filepath.Dir(newPath)
	if oldDir != newDir {
		moves = append(moves, moveCovers(oldDir, newDir)...)
		moves = append(moves, moveAlbumMetadata(oldDir, newDir)...)
	}

	return moves
}

// albumMetaNames are the album-scoped files that are part of the music rather
// than application state, matched case-insensitively and in full. `album.nfo`
// is the Jellyfin/Plex convention; without it a reorganise splits an album's
// metadata away from its audio.
var albumMetaNames = map[string]bool{
	"album.nfo": true,
}

// cueExt marks a cue sheet: one file describing a whole album's track list,
// conventionally named after the album or after track 1. It is album-scoped
// whatever it is called, so the extension is the test.
const cueExt = ".cue"

// bareCoverBases are the cover names that carry no album suffix. `cover.jpg`
// and `folder.jpg` are as standard as `cover-<album>.jpg`, and a tidy that
// carried the third while stranding the first two is not carrying the cover.
var bareCoverBases = map[string]bool{
	"cover":  true,
	"folder": true,
}

// isAlbumMetaSidecar reports whether name is an album-scoped file that belongs
// with the audio. Deliberately a short, explicit list rather than a pattern:
// everything here is moved out of the user's directory on a routine
// operation, so a name nobody has a reason to expect moving should not move.
func isAlbumMetaSidecar(name string) bool {
	if strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return false
	}
	lower := strings.ToLower(name)
	if albumMetaNames[lower] {
		return true
	}
	if strings.EqualFold(filepath.Ext(lower), cueExt) {
		return true
	}
	base := strings.TrimSuffix(lower, filepath.Ext(lower))
	return bareCoverBases[base] && coverExts[strings.TrimPrefix(filepath.Ext(lower), ".")]
}

// moveAlbumMetadata carries the album-scoped non-cover files (album.nfo, cue
// sheets) from oldDir to newDir.
//
// When several tracks from one directory are reorganised into DIFFERENT
// directories, these files can only follow one of them — the first move takes
// them and the rest find nothing. That is the same trade-off moveCovers has
// always made, and duplicating a metadata file per destination would be worse
// than picking one: two .nfo files claiming to describe the same album is a
// mess a user has to unpick by hand.
func moveAlbumMetadata(oldDir, newDir string) []SidecarMove {
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		// A folder we cannot list simply has nothing to carry. The caller
		// already moved the audio, so this is not worth a warning.
		return nil
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return nil
	}
	var moves []SidecarMove
	for _, e := range entries {
		if e.IsDir() || !isAlbumMetaSidecar(e.Name()) {
			continue
		}
		if m := moveSidecarFile(filepath.Join(oldDir, e.Name()), filepath.Join(newDir, e.Name())); m != nil {
			moves = append(moves, *m)
		}
	}
	return moves
}

// coverExts are the image types a cover sidecar can be written as. Kept
// explicit rather than sniffing content: the folder is full of unrelated
// files and the tidy job walks all of it.
var coverExts = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true,
	"webp": true, "bmp": true, "tiff": true, "tif": true,
}

// moveCovers carries every `cover-<album>.<imageext>` from oldDir to
// newDir. A cover is shared by every track in its folder, so this moves
// the album's images as a unit.
func moveCovers(oldDir, newDir string) []SidecarMove {
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		// A folder we cannot list simply has no covers to carry. The
		// caller already moved the audio, so this is not worth a warning.
		return nil
	}
	var moves []SidecarMove
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !isCoverSidecar(e.Name()) {
			continue
		}
		from := filepath.Join(oldDir, e.Name())
		to := filepath.Join(newDir, e.Name())
		if m := moveSidecarFile(from, to); m != nil {
			moves = append(moves, *m)
		}
	}
	return moves
}

// isCoverSidecar reports whether name is a `cover-<album>.<imageext>`
// file — the exact shape HandleSidecars writes. The album segment is
// held to the same rule that writer writes it under: non-empty, no
// separator, and not a dot-file, so `cover-.jpg` (where the "album" is
// just the extension) is not mistaken for a cover.
func isCoverSidecar(name string) bool {
	if !strings.HasPrefix(name, coverPrefix) {
		return false
	}
	album := strings.TrimPrefix(name, coverPrefix)
	if album == "" || strings.HasPrefix(album, ".") || strings.ContainsAny(album, `/\`) {
		return false
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	return coverExts[ext]
}

// moveSidecarFile renames from→to, returning nil when there is nothing to
// do. A missing source is not a failure: most tracks have no .lrc.
func moveSidecarFile(from, to string) *SidecarMove {
	m := &SidecarMove{From: from, To: to}
	if from == to {
		return nil
	}
	if _, err := os.Stat(from); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		m.Err = err
		return m
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		m.Err = err
		return m
	}
	// A stale sidecar left by a since-deleted file must not block the
	// move: the audio rename already claimed this name, so the sidecar
	// belongs to it. os.Rename replaces the destination.
	if err := os.Rename(from, to); err != nil {
		m.Err = err
		return m
	}
	return m
}
