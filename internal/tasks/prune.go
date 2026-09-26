// Package tasks — 空目录清理 handler。
//
// TidyFolder moves files into a new structure and leaves the directories it
// emptied behind; renaming and deleting tracks leaves more. This removes the
// ones that ended up with nothing in them, and drops the index rows for files
// that are no longer on disk at all (pruneVanished).
//
// The safety property that matters here is that it uses os.Remove, never
// os.RemoveAll. Remove fails on a non-empty directory, so "empty" is decided
// by the kernel at the moment of deletion rather than by a check this code
// might get wrong. A race — a file landing in the directory between the scan
// and the delete — therefore leaves the directory in place instead of
// deleting a track that arrived a millisecond ago. pruneVanished is built on
// the same principle: it asks the kernel whether each file exists, one row at
// a time, rather than inferring absence from what a walk happened to see.
package tasks

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/db"
)

// PruneEmptyFoldersPayload 与 FullScanPayload 同 schema：worker 入口可选传
// sub_paths；为空表示整库。
type PruneEmptyFoldersPayload = FullScanPayload

// PruneEmptyFoldersHandler 删除库内空目录。
type PruneEmptyFoldersHandler struct {
	DB        *gorm.DB
	MusicRoot string
}

func (h *PruneEmptyFoldersHandler) ProcessTask(ctx context.Context, t Task) error {
	var p PruneEmptyFoldersPayload
	if raw, ok := t.Payload.(*PruneEmptyFoldersPayload); ok && raw != nil {
		p = *raw
	}
	removed, err := h.pruneEmpty(ctx, p.SubPaths)
	if err != nil {
		return err
	}
	// The same button also drops index rows whose files are gone. Same
	// intent — the library stopped matching its own bookkeeping — and the
	// two are reported separately so the audit log says which happened
	// rather than lumping them into one number.
	vanished := h.pruneVanished(ctx, p.SubPaths)
	// The count is the whole point of the operation being visible, so it goes
	// to the audit log: 操作审计 then answers "did that clean anything?"
	// without the button having to guess.
	audit.Log(ctx, audit.ActionPruneEmptyFolders, "library", "worker", audit.StatusSuccess,
		len(removed), map[string]interface{}{
			"removed":        removed,
			"vanished_rows":  vanished,
			"vanished_count": len(vanished),
		}, nil)
	log.Printf("[prune] removed %d empty director%s, %d vanished index row(s)",
		len(removed), plural(len(removed)), len(vanished))
	return nil
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// pruneEmpty walks depth-first and deletes every directory that is empty by
// the time we reach it.
//
// Bottom-up is what makes nesting work: a directory holding nothing but empty
// subdirectories becomes empty itself once those go, and gets removed on the
// same pass. So `Artist/Album` emptied by a tidy, and then the `Artist` that
// held only that album, both disappear without anyone having to say so twice.
func (h *PruneEmptyFoldersHandler) pruneEmpty(ctx context.Context, subPaths [][2]string) ([]string, error) {
	return h.pruneEmptyMode(ctx, subPaths, false)
}

// pruneEmptyMode is pruneEmpty with the deletion switchable, so the same
// walk can answer "what would you remove?" without touching the disk. The
// preview is what the confirmation dialog renders; running a second,
// slightly different walk to produce it would let the two disagree.
func (h *PruneEmptyFoldersHandler) pruneEmptyMode(ctx context.Context, subPaths [][2]string, dryRun bool) ([]string, error) {
	root := h.musicRoot()
	if root == "" {
		return nil, fmt.Errorf("prune: music root not configured")
	}
	// data/ holds the app's own state (db, covers cache, bootstrap creds). It
	// is under the music root and will often contain empty subdirectories, so
	// it has to be excluded by path, not by hoping it is never empty.
	protected := filepath.Join(root, "data")

	var dirs []string
	walk := func(start string) error {
		return filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				// A directory we cannot read is not one we should delete.
				log.Printf("[prune] skip %s: %v", path, err)
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if !d.IsDir() {
				return nil
			}
			clean := filepath.Clean(path)
			if clean == filepath.Clean(root) {
				// The root is the walk's own starting point in the
				// whole-library case, and returning SkipDir here would
				// abandon the entire walk on its first callback. Descend
				// into it; just never collect it for deletion.
				return nil
			}
			if clean == protected || strings.HasPrefix(clean, protected+string(os.PathSeparator)) {
				return filepath.SkipDir
			}
			dirs = append(dirs, clean)
			return nil
		})
	}

	if len(subPaths) == 0 {
		if err := walk(root); err != nil {
			return nil, fmt.Errorf("prune: walk root: %w", err)
		}
	} else {
		for _, sp := range subPaths {
			// SafeJoin semantics matter here: a payload path is untrusted
			// input and must not be able to walk out of the library.
			dir := sp[1]
			if dir == "" {
				dir = root
			}
			if err := walk(dir); err != nil {
				log.Printf("[prune] walk %s: %v", dir, err)
			}
		}
	}

	// Deepest first. Comparing by separator count is enough and avoids
	// stat-ing every entry; equal-depth order does not matter because a
	// directory can only become empty once its children are gone.
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], string(os.PathSeparator)) >
			strings.Count(dirs[j], string(os.PathSeparator))
	})

	var removed []string
	// A dry run has to answer the same question the real pass answers one
	// level at a time: "would the kernel accept this?" — which for the
	// real pass means os.Remove, and for the preview means "is this
	// directory empty, or empty only once the children already claimed
	// below it are gone?". Skipping that second part made the preview
	// stop one level short of the cascade, so the dialog would promise
	// two directories where the task removes three.
	willGo := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		select {
		case <-ctx.Done():
			return removed, ctx.Err()
		default:
		}
		// os.Remove, deliberately not RemoveAll: a non-empty directory is
		// left exactly as it is, and that decision is the kernel's.
		if !dryRun {
			if err := os.Remove(dir); err != nil {
				continue
			}
		} else if !directoryWouldEmpty(dir, willGo) {
			continue
		}
		willGo[dir] = true
		rel, relErr := filepath.Rel(root, dir)
		if relErr != nil {
			rel = dir
		}
		removed = append(removed, filepath.ToSlash(rel))
	}
	sort.Strings(removed)
	return removed, nil
}

// directoryWouldEmpty reports whether dir holds nothing that would survive
// the pass: either nothing at all, or only subdirectories already accounted
// for as going away.
//
// This mirrors what os.Remove will decide, minus the removal. An entry that
// is a file, a symlink, or a directory the walk skipped (data/, anything
// reached through a symlink) keeps its parent alive — which is the same
// answer the real pass arrives at when the kernel refuses the parent.
func directoryWouldEmpty(dir string, willGo map[string]bool) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			return false
		}
		if !willGo[filepath.Join(dir, e.Name())] {
			return false
		}
	}
	return true
}

// pruneVanished drops index rows for library files that are no longer on
// disk.
//
// A row outlives its file whenever the file leaves without going through the
// app: a file manager, a bind-mounted volume edited from the host, an rsync.
// Neither the scanner nor tidy removes those rows, so they accumulate and
// every dedup check pays for them — each one is a candidate row that has to
// be stat-ed and rejected before the check can move on. The library browser
// reads the disk rather than the table, so the user never sees them; they are
// pure cost.
//
// What this deliberately does NOT do is the set difference the scanner refuses
// to do ("WHERE path NOT IN", scanner.go: 不执行 WHERE path NOT IN 删除).
// That was banned because a partial or failed sub-scan would delete the rows
// for every file it never visited — a whole library, gone, because one
// directory was unreadable. Here each row is judged on its own by asking the
// kernel whether its file exists, so a scan that only covered one subtree
// still cannot delete a row outside it. A stat error that is not "not exist"
// — a permission problem, an I/O error, a network mount briefly stalling —
// keeps the row, because "I could not tell" is not "it is gone".
//
// Folder rows are left alone for a different reason: their children's
// parent_id points at them, so removing one can strand the rest of the tree.
// See TestPruneVanished_KeepsFolderRows.
func (h *PruneEmptyFoldersHandler) pruneVanished(ctx context.Context, subPaths [][2]string) []string {
	return h.pruneVanishedMode(ctx, subPaths, false)
}

func (h *PruneEmptyFoldersHandler) pruneVanishedMode(ctx context.Context, subPaths [][2]string, dryRun bool) []string {
	root := h.musicRoot()
	if root == "" {
		return nil
	}

	scopes := []string{filepath.Clean(root)}
	if len(subPaths) > 0 {
		// A payload path is untrusted input and must not be able to walk
		// out of the library; same reasoning as pruneEmpty above.
		scopes = scopes[:0]
		for _, sp := range subPaths {
			dir := sp[1]
			if dir == "" {
				dir = root
			}
			clean := filepath.Clean(dir)
			if clean != root && !strings.HasPrefix(clean, root+string(os.PathSeparator)) {
				log.Printf("[prune] skip out-of-root scope %s", clean)
				continue
			}
			scopes = append(scopes, clean)
		}
	}

	// No de-duplication across scopes, because none is needed: music_folder
	// has a UNIQUE index on path, so a query returns each path at most
	// once, and a row deleted under one scope is simply not returned by the
	// next. A second pass over an overlapping scope re-stats a row it
	// chose to keep, which costs a syscall and changes nothing. An earlier
	// version carried a `seen` set for this; it could never have fired, and
	// a map that implies a guarantee it cannot deliver is worse than none.
	var removed []string
	for _, scope := range scopes {
		var rows []db.Folder
		// file_type 'folder' excluded, not by enumerating the audio and
		// image values: that set has already proven it drifts between
		// writers. Everything that is not a folder row is a file row.
		q := h.DB.Where("file_type <> 'folder'").
			Where(`path = ? OR path LIKE ? ESCAPE '!'`,
				scope, likeEscape(scope)+string(os.PathSeparator)+"%")
		if err := q.Find(&rows).Error; err != nil {
			log.Printf("[prune] read rows under %s: %v", scope, err)
			continue
		}
		for _, row := range rows {
			select {
			case <-ctx.Done():
				return removed
			default:
			}
			if _, err := os.Stat(row.Path); err == nil {
				continue // still there
			} else if !os.IsNotExist(err) {
				log.Printf("[prune] keep %s: %v", row.Path, err)
				continue
			}
			if !dryRun {
				if err := h.DB.Where("path = ?", row.Path).Delete(&db.Folder{}).Error; err != nil {
					log.Printf("[prune] delete %s: %v", row.Path, err)
					continue
				}
			}
			rel, relErr := filepath.Rel(root, row.Path)
			if relErr != nil {
				rel = row.Path
			}
			removed = append(removed, filepath.ToSlash(rel))
		}
	}
	sort.Strings(removed)
	return removed
}

// likeEscape neutralises the LIKE wildcards in a literal path prefix.
//
// The scope comes from configuration or from a task payload, and a music
// directory called "100% Hits" is not exotic. Passed unescaped, the '%' would
// match any run of characters and the scan would consider rows outside the
// scope — which, given that deletion follows, is not a cosmetic problem.
//
// The escape character is '!' and the caller must pair this with ESCAPE '!'.
// Backslash would read more naturally but does not work: LIKE has no default
// escape character, so '\_' is a literal backslash followed by any character
// and matches nothing. The bug is invisible for a path without '_' in it,
// which is why it survived a first pass — every test path that failed to
// match had an underscore in the name, and the wildcard test that was meant
// to catch this could not fail for the right reason. The escape character
// itself is escaped first, so a directory literally named "a!b" is still
// matched exactly.
func likeEscape(s string) string {
	r := strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)
	return r.Replace(s)
}

// PreviewPrune answers "what would 清理残留 remove?" without removing
// anything, and is what the confirmation dialog renders.
//
// It runs the same two passes the real task runs, in dry-run mode, rather
// than a parallel implementation: a preview computed any other way is a
// second thing that can disagree with the first, and the whole point of
// asking the user to confirm is that the list they approve is the list
// that gets acted on.
//
// Read-only, so the gateway can answer it inline instead of round-tripping
// through the worker queue the way the mutation does.
func (h *PruneEmptyFoldersHandler) PreviewPrune(ctx context.Context, subPaths [][2]string) ([]string, []string, error) {
	dirs, err := h.pruneEmptyMode(ctx, subPaths, true)
	if err != nil {
		return nil, nil, err
	}
	return dirs, h.pruneVanishedMode(ctx, subPaths, true), nil
}

func (h *PruneEmptyFoldersHandler) musicRoot() string {
	if h.MusicRoot != "" {
		return h.MusicRoot
	}
	if v := os.Getenv("MUSIC_DIR"); v != "" {
		return v
	}
	return "/app/media"
}
