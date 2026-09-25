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
// Covers (`cover-<album>.<ext>`) are keyed by ALBUM, not by file name, so
// they only need to travel when the directory changes — a pure base-name
// rename leaves them correctly in place.
func MoveSidecars(oldPath, newPath string) []SidecarMove {
	var moves []SidecarMove

	oldBase := strings.TrimSuffix(oldPath, filepath.Ext(oldPath))
	newBase := strings.TrimSuffix(newPath, filepath.Ext(newPath))
	if m := moveSidecarFile(oldBase+".lrc", newBase+".lrc"); m != nil {
		moves = append(moves, *m)
	}

	// Covers are named after the album, not the track, so they only
	// travel when the directory changes. A base-name rename leaves them
	// exactly where they belong.
	oldDir, newDir := filepath.Dir(oldPath), filepath.Dir(newPath)
	if oldDir != newDir {
		for _, m := range moveCovers(oldDir, newDir) {
			moves = append(moves, m)
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
