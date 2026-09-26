package tasks

import (
	"log"
	"path/filepath"

	"go-music-tag/internal/utils"
)

// Turning a task payload's sub_paths into something safe to walk.
//
// Three handlers take a [][2]string of (parent_uid, path) pairs from a
// request body and use it as the root of a filesystem walk: FullScanHandler,
// UpdateScanHandler and PruneEmptyFoldersHandler. All three once used it
// verbatim, so a caller could name any directory the worker could reach. The
// prunes' half was fixed (it deletes), the scanners' half was recorded as a
// known gap, and the fix left a third copy of the rule in prune.go where
// nothing else could find it.
//
// This is the one place the rule lives now. It is deliberately not a
// convenience: a per-handler "walk whatever I was handed" is what produced
// the divergence, and a shared helper is the only thing that makes the next
// handler use it by default.
//
// The check is utils.SafeAbs, not SafeJoin. A scope is an absolute path —
// that is what music_folder stores and what every caller already passes —
// so an absolute path outside the root is a scope the caller has no business
// naming, and it should be refused outright. SafeJoin would fold "/etc/ssl"
// into "<root>/etc/ssl", which is a different directory that usually does not
// exist, so the escape would fail by accident rather than by decision.
//
// Two things this deliberately does NOT do:
//
//   - It does not resolve symlinks. The scanner already skips symlinked
//     entries (REVIEW.md P1-5 H5), so a link inside the tree cannot lead the
//     walk out; resolving here would instead reject a legitimate
//     MUSIC_DIR that IS a symlink, which is a supported deployment.
//   - It does not validate parent_uid. That is untrusted too, but with the
//     path contained it can only mis-parent rows inside the library — a
//     cosmetic tree defect rather than an escape.
func scanStack(musicFolder string, subPaths [][2]string) [][2]string {
	root := filepath.Clean(musicFolder)
	// An absent payload means the whole library, which is what the UI sends
	// and the only thing a caller with no paths can legitimately ask for.
	if len(subPaths) == 0 {
		return [][2]string{{"", root}}
	}
	out := make([][2]string, 0, len(subPaths))
	for _, sp := range subPaths {
		if sp[1] == "" {
			out = append(out, [2]string{sp[0], root})
			continue
		}
		abs, err := utils.SafeAbs(root, sp[1])
		if err != nil {
			// Logged rather than silently dropped: a payload that keeps
			// getting refused is a client bug or an attempt, and either
			// way the operator should be able to see it.
			log.Printf("[scan] skip out-of-root scope %q: %v", sp[1], err)
			continue
		}
		out = append(out, [2]string{sp[0], abs})
	}
	return out
}
