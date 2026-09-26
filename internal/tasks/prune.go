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
	// Resolved once and handed to both passes. Each pass used to take the
	// raw payload and resolve it for itself, so a refused scope was logged
	// twice — and this is a log line an operator reads when something was
	// skipped, so a doubled one is worse than a single one, not merely
	// untidy. PreviewPrune had the same shape and the same doubling.
	scopes := h.scopes(p.SubPaths)
	removed, err := h.pruneEmptyScopes(ctx, scopes, false)
	if err != nil {
		return err
	}
	// The same button also drops index rows whose files are gone. Same
	// intent — the library stopped matching its own bookkeeping — and the
	// two are reported separately so the audit log says which happened
	// rather than lumping them into one number.
	vanished := h.pruneVanishedScopes(ctx, scopes, false)
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
	return h.pruneEmptyScopes(ctx, h.scopes(subPaths), false)
}

// pruneEmptyScopes is the walk itself, over already-resolved scopes, with
// the deletion switchable so the same code can answer "what would you
// remove?" without touching the disk. The preview is what the confirmation
// dialog renders; running a second, slightly different walk to produce it
// would let the two disagree.
//
// Taking scopes rather than the payload is what lets ProcessTask and
// PreviewPrune resolve once and share, instead of each pass resolving for
// itself and logging every refusal twice.
func (h *PruneEmptyFoldersHandler) pruneEmptyScopes(ctx context.Context, scopes []string, dryRun bool) ([]string, error) {
	// No empty-root guard. musicRoot() falls back to MUSIC_DIR and then to
	// /app/media, so it cannot return "", which made the guard this replaced
	// unreachable -- and a test asserting on it passed only because its
	// throwaway database happened to hold no rows. A branch that cannot run
	// is not a safety net; it is a comment that has stopped being true.
	root := h.musicRoot()
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

	if len(scopes) == 0 {
		return nil, nil
	}
	for _, scope := range scopes {
		// A walk that stops early cannot answer "what would you remove?",
		// and a partial list in a confirmation dialog is worse than no
		// answer: the user approves a count that was never the real one.
		// So the error propagates.
		//
		// Per-entry problems (an unreadable directory) do NOT reach here --
		// walk logs those and carries on, because one unreadable directory
		// should not abort the sweep of the rest. This is only ctx.Err()
		// and a failure to read a scope root itself.
		if err := walk(scope); err != nil {
			return nil, fmt.Errorf("prune: walk %s: %w", scope, err)
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
	return sortedUniq(removed), nil
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

// scopes resolves a task payload's sub_paths into the directories this task
// is allowed to act on.
//
// It is scanStack with the parent uids dropped, and it is no longer its own
// implementation. This used to be the second copy of the containment rule,
// and the two passes disagreed: pruneVanished skipped an out-of-root scope
// while pruneEmpty walked it, and pruneEmpty then os.Remove'd empty
// directories wherever the payload pointed. The limit was "only empty
// ones", which is a property of os.Remove, not access control. Now the rule
// lives in scanstack.go and the scanners share it.
func (h *PruneEmptyFoldersHandler) scopes(subPaths [][2]string) []string {
	if h.musicRoot() == "" {
		return nil
	}
	stack := scanStack(h.musicRoot(), subPaths)
	out := make([]string, 0, len(stack))
	for _, sp := range stack {
		out = append(out, sp[1])
	}
	return out
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
	return h.pruneVanishedScopes(ctx, h.scopes(subPaths), false)
}

func (h *PruneEmptyFoldersHandler) pruneVanishedScopes(ctx context.Context, scopes []string, dryRun bool) []string {
	// No empty-root guard, for the reason given in pruneEmptyScopes.
	root := h.musicRoot()
	if len(scopes) == 0 {
		return nil
	}

	// No per-scope `seen` set, because none is needed to avoid deleting
	// twice: music_folder has a UNIQUE index on path, so a query returns a
	// given path at most once, and a row deleted under one scope is not
	// returned by the next. A second pass over an overlapping scope
	// re-stats a row it chose to keep, which costs a syscall and changes
	// nothing. An earlier version carried such a set; it could never have
	// fired, and a map that implies a guarantee it cannot deliver is worse
	// than none.
	//
	// That reasoning is about deletion, and it is FALSE for the dry run:
	// nothing is deleted, so an overlapping scope returns the same row
	// again and the preview listed it twice. The dialog's whole job is to
	// be a list the user can check, and "16 items" over 15 distinct paths
	// is the kind of small lie that makes the next number untrustworthy.
	// The dry run reports nothing, so deduping its result is the whole fix
	// — see sortedUniq below.
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
	return sortedUniq(removed)
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
// sortedUniq sorts and removes duplicates from a list of paths to report.
//
// Both passes build their answer by appending per scope, so two scopes that
// overlap produce the same path twice. Deletion is idempotent, so only the
// reported list is wrong — and the reported list is what the confirmation
// dialog renders and what the audit log records, so "16 items" over 15
// distinct paths is a small lie in the one place the user is being asked to
// trust a count.
func sortedUniq(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

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
	scopes := h.scopes(subPaths)
	dirs, err := h.pruneEmptyScopes(ctx, scopes, true)
	if err != nil {
		return nil, nil, err
	}
	return dirs, h.pruneVanishedScopes(ctx, scopes, true), nil
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
