// Package tasks — 空目录清理 handler。
//
// TidyFolder moves files into a new structure and leaves the directories it
// emptied behind; renaming and deleting tracks leaves more. This removes the
// ones that ended up with nothing in them.
//
// The safety property that matters here is that it uses os.Remove, never
// os.RemoveAll. Remove fails on a non-empty directory, so "empty" is decided
// by the kernel at the moment of deletion rather than by a check this code
// might get wrong. A race — a file landing in the directory between the scan
// and the delete — therefore leaves the directory in place instead of
// deleting a track that arrived a millisecond ago.
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
	// The count is the whole point of the operation being visible, so it goes
	// to the audit log: 操作审计 then answers "did that clean anything?"
	// without the button having to guess.
	audit.Log(ctx, audit.ActionPruneEmptyFolders, "library", "worker", audit.StatusSuccess,
		len(removed), map[string]interface{}{
			"removed": removed,
		}, nil)
	log.Printf("[prune] removed %d empty director%s", len(removed), plural(len(removed)))
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
	for _, dir := range dirs {
		select {
		case <-ctx.Done():
			return removed, ctx.Err()
		default:
		}
		// os.Remove, deliberately not RemoveAll: a non-empty directory is
		// left exactly as it is.
		if err := os.Remove(dir); err != nil {
			continue
		}
		rel, relErr := filepath.Rel(root, dir)
		if relErr != nil {
			rel = dir
		}
		removed = append(removed, filepath.ToSlash(rel))
	}
	sort.Strings(removed)
	return removed, nil
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
