// Package audiocache owns the on-disk download cache under AUDIO_CACHE_DIR.
//
// The cache is the staging area that yt-dlp / the migu-kugou-kuwo fetchers
// write into and that /api/stream/ serves from directly. It was the one
// piece of backend state with no owner: written by the worker, read by the
// gateway, mounted as a docker named volume, and never pruned by anybody.
//
// Nothing here deletes anything on its own. The package is split into
// Inspect (what is on disk), Select (which files a given policy would
// delete) and Remove (delete an explicit list). The caller decides whether
// the policy is the automatic size cap or a human clicking a button, and
// only one process — the worker — runs the automatic one, so two pruners
// can never race on the same directory.
package audiocache

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	envRoot     = "AUDIO_CACHE_DIR"
	defaultRoot = "/tmp/audio_cache"
	// dotDepth is how far below Root a cacheable file lives:
	// Root/<source>/<id>.<ext>. Anything else (a nested dir, a stray file
	// dropped at the root) is counted in the usage report but never
	// selected for deletion, so a future layout change cannot turn the
	// pruner into a recursive rm.
	dotDepth = 2
)

// Root returns the cache root. Read from env on every call so an operator
// can set AUDIO_CACHE_DIR without a rebuild; the test seams that os.Setenv
// rely on work the same way.
//
// The default is duplicated in internal/plugin/youtube/server.go::tmpDir,
// which cannot import this package (the plugin deliberately stays free of
// internal deps so its image is pure Go). That mirror predates this
// package; it is the last remaining copy of the rule.
func Root() string {
	if r := strings.TrimSpace(os.Getenv(envRoot)); r != "" {
		return r
	}
	return defaultRoot
}

// Dir returns the per-source staging directory.
//
// Per-source subdirectories are load-bearing, not cosmetic: numeric ids
// from migu/soundcloud would otherwise collide with youtube's 11-char ids
// and one source's file would be served for another's request.
func Dir(source string) string {
	return filepath.Join(Root(), source)
}

// File is one cacheable file on disk.
type File struct {
	Path    string
	Source  string
	Size    int64
	ModTime time.Time
}

// SourceUsage is the per-source breakdown of Inspect's total.
type SourceUsage struct {
	Bytes int64
	Files int
}

// Stats is a point-in-time accounting of the cache directory.
type Stats struct {
	Root     string
	Exists   bool
	Bytes    int64
	Files    int
	BySource map[string]SourceUsage
	// OtherBytes / OtherFiles counts regular files that live somewhere
	// other than Root/<source>/<file>. They are reported so the number
	// the user sees matches `du`, but Select never touches them.
	OtherBytes int64
	OtherFiles int
}

// Inspect walks the cache root and accounts for every regular file in it.
//
// Missing root is not an error: a fresh deployment has no cache yet, and
// the settings page should say "0 B" rather than fail.
func Inspect() Stats {
	st := Stats{Root: Root(), BySource: map[string]SourceUsage{}}
	entries, err := os.ReadDir(st.Root)
	if err != nil {
		return st
	}
	st.Exists = true
	for _, e := range entries {
		if !e.IsDir() {
			info, err := e.Info()
			if err == nil && info.Mode().IsRegular() {
				st.OtherBytes += info.Size()
				st.OtherFiles++
			}
			continue
		}
		src := e.Name()
		inner, err := os.ReadDir(filepath.Join(st.Root, src))
		if err != nil {
			continue
		}
		for _, f := range inner {
			if f.IsDir() {
				continue
			}
			info, err := f.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			u := st.BySource[src]
			u.Bytes += info.Size()
			u.Files++
			st.BySource[src] = u
			st.Bytes += info.Size()
			st.Files++
		}
	}
	return st
}

// Selection is the outcome of applying a policy to the cache: what would go,
// how much that frees, and what was deliberately left alone.
type Selection struct {
	Files      []File
	FreedBytes int64
	// TotalBytes is the cache size before the selection, so the caller can
	// report "1.4 GB → 1.1 GB" without a second walk.
	TotalBytes int64
	// RecentFiles / RecentBytes are the files the minAge guard excluded.
	// They are reported rather than silently dropped: when the cache sits
	// above the cap because everything in it was downloaded this hour, the
	// honest answer is "I left it all alone", not "cache is now under cap".
	RecentFiles int
	RecentBytes int64
}

// Select applies a policy to the cache and returns the files it would
// delete, oldest first. It deletes nothing.
//
//   - limitBytes <= 0 means "no cap": every eligible file is selected.
//     That is the manual button's shape, and it is why the automatic and
//     manual paths share one implementation.
//   - minAge protects files modified within that window from being
//     selected, whatever the cap. /api/stream ServeFiles straight out of
//     this directory and a download task reads it moments before copying
//     into the library, so a file is not safe to unlink just because it is
//     the oldest one. Order of the two rules matters: the cap is applied to
//     the eligible set, so protecting recent files can leave the cache
//     above the cap rather than deleting something in use.
//
// A missing cache directory selects nothing and is not an error.
func Select(limitBytes int64, minAge time.Duration) Selection {
	sel := Selection{}
	now := time.Now()
	st := Inspect()
	sel.TotalBytes = st.Bytes
	if !st.Exists {
		return sel
	}
	for src := range st.BySource {
		// Per-source walk again rather than reusing Inspect's aggregate:
		// Select needs each file's mtime, which Stats does not carry.
		inner, err := os.ReadDir(filepath.Join(st.Root, src))
		if err != nil {
			continue
		}
		for _, f := range inner {
			if f.IsDir() {
				continue
			}
			info, err := f.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if minAge > 0 && now.Sub(info.ModTime()) < minAge {
				sel.RecentFiles++
				sel.RecentBytes += info.Size()
				continue
			}
			sel.Files = append(sel.Files, File{
				Path:    filepath.Join(st.Root, src, f.Name()),
				Source:  src,
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
			sel.FreedBytes += info.Size()
		}
	}
	// Oldest first. Ties broken by path so two runs over an unchanged
	// directory produce an identical plan — an eviction order that shifts
	// between runs makes "which files did it keep" unexplainable.
	sort.Slice(sel.Files, func(i, j int) bool {
		if !sel.Files[i].ModTime.Equal(sel.Files[j].ModTime) {
			return sel.Files[i].ModTime.Before(sel.Files[j].ModTime)
		}
		return sel.Files[i].Path < sel.Files[j].Path
	})
	if limitBytes > 0 && sel.TotalBytes <= limitBytes {
		// Under the cap already; selecting everything would be a
		// surprising response to "enforce the cap".
		return Selection{TotalBytes: sel.TotalBytes, RecentFiles: sel.RecentFiles, RecentBytes: sel.RecentBytes}
	}
	if limitBytes > 0 {
		// sel.Files is already oldest-first, so taking a prefix of it is
		// the eviction order. Stop as soon as the target is met: deleting
		// one file more than the cap requires is how a size cap becomes a
		// size cliff.
		need := sel.TotalBytes - limitBytes
		// The walk accumulated every eligible file's size; from here
		// FreedBytes is re-derived as "how much this selection frees".
		sel.FreedBytes = 0
		deleted := sel.Files[:0:0]
		for _, f := range sel.Files {
			if sel.FreedBytes >= need {
				break
			}
			deleted = append(deleted, f)
			sel.FreedBytes += f.Size
		}
		sel.Files = deleted
	}
	return sel
}

// Result reports what Remove actually did.
type Result struct {
	Removed    int
	FreedBytes int64
	// Failed maps path → reason for every file that could not be deleted.
	// Reported instead of counted so the caller can say "3 of 5 removed,
	// 2 failed" rather than silently under-reporting.
	Failed map[string]string
}

// Remove deletes the given files and reports what happened.
//
// Every path is re-checked for containment under the cache root before the
// unlink. Remove is reached from an HTTP handler, and the handler's own
// list came from Select — but a delete entry point that trusts its caller's
// path arithmetic is one refactor away from being an rm -rf, and this
// check costs one filepath.Rel per file.
//
// Only regular files are unlinked. A symlink is reported as a failure
// rather than followed: nothing in the download path creates one, so
// seeing one means something else wrote here.
func Remove(paths []string) Result {
	res := Result{Failed: map[string]string{}}
	root := Root()
	for _, p := range paths {
		if !within(root, p) {
			res.Failed[p] = "outside cache root"
			continue
		}
		info, err := os.Lstat(p)
		if err != nil {
			res.Failed[p] = "stat failed: " + err.Error()
			continue
		}
		if !info.Mode().IsRegular() {
			res.Failed[p] = "not a regular file"
			continue
		}
		if err := os.Remove(p); err != nil {
			res.Failed[p] = err.Error()
			continue
		}
		res.Removed++
		res.FreedBytes += info.Size()
	}
	return res
}

// within reports whether p is a cacheable file: exactly
// <root>/<source>/<name>, with no traversal and no symlinked parent that
// could redirect the unlink out of the tree.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != dotDepth {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// FormatBytes renders a byte count the way the settings page shows it.
// Lives here so the gateway response and anything logging the same numbers
// agree on the units.
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
