// Package trash owns the one thing every "this file is leaving the library"
// path has to agree on: how a file gets into DATA_DIR/.trash, and how a
// relative path maps to its slot inside a batch.
//
// It exists because that logic had two homes. The gateway's DeleteFiles and
// RestoreTrash both move files between MUSIC_DIR and DATA_DIR, and so does the
// pruner now — and a second implementation of "move it into the trash" is how
// the cross-device bug happened once already. One place, one owner.
//
// Nothing here talks to the database, HTTP, or the library index. It moves
// bytes between two paths and reports whether it managed to.
package trash

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// batchLayout is the on-disk shape of a batch: one timestamped directory per
// delete operation, holding the deleted files under their library-relative
// paths. Keeping the relative path is what lets RestoreTrash put a file back
// exactly where it came from, and what lets the listing show a path the user
// recognises rather than a flattened name.
const (
	dirName  = ".trash"
	batchFmt = "20060102-150405"
)

// Rename is os.Rename behind a variable so a test can make it fail the way a
// real deployment makes it fail.
//
// This is not a hypothetical: DATA_DIR and MUSIC_DIR are separate mounts in
// the default compose layout, so a rename between them returns EXDEV. A test
// with one temp dir is one device and can never produce that, which is how
// RestoreTrash shipped a plain os.Rename that failed with "invalid cross-device
// link" on every file in production while its Go test passed.
var Rename = os.Rename

// BatchDir returns the trash batch directory for a delete performed at the
// given moment. One timestamp per operation: a batch is one "the user pressed
// the button" event, which is the unit both the listing and the audit speak in.
//
// The second-resolution layout is inherited from the gateway's original
// implementation rather than invented here. It is a real (if unlikely)
// collision risk — two deletions inside the same second merge into one batch —
// and the trash is keyed by an opaque id, not a path, so a merge is harmless
// either way.
func BatchDir(dataDir string, at time.Time) string {
	return filepath.Join(dataDir, dirName, at.Format(batchFmt))
}

// MoveAside relocates src to dest, falling back to copy+remove when the two
// are on different filesystems (EXDEV) — which they are here whenever
// DATA_DIR and MUSIC_DIR are separate mounts, i.e. the default compose
// layout. Without the fallback this works in `go test` (one temp dir) and
// fails in every real deployment.
//
// It is used in both directions: the gateway moving a file in when it is
// deleted and out when it is restored, and the pruner moving album metadata
// in. A move that cannot complete reports why; callers decide what to do with
// that, and none of them can afford to treat it as success.
func MoveAside(src, dest string) error {
	return moveAsideWith(src, dest, Rename)
}

// moveAsideWith takes the rename as a parameter so a test can inject the EXDEV
// directly, without going through the package-level seam above.
func moveAsideWith(src, dest string, rename func(string, string) error) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("prepare trash dir: %w", err)
	}
	if err := rename(src, dest); err == nil {
		return nil
	}
	// The dest dir is created before the rename so a genuine permission
	// error surfaces as itself rather than being masked by a confusing
	// "no such file" from the fallback.
	return copyFileThenRemove(src, dest)
}

func copyFileThenRemove(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create trash copy: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dest)
		return fmt.Errorf("copy to trash: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("close trash copy: %w", err)
	}
	if err := os.Remove(src); err != nil {
		// The copy exists, so the content is safe even though the
		// original path still does. Report it rather than silently
		// leaving two copies of the same file in the library.
		return fmt.Errorf("copy succeeded but removing original failed: %w", err)
	}
	return nil
}
