package handler

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/audioext"
	"go-music-tag/internal/utils"
)

// RecursiveFileListRequest is the POST body for /api/file_list_recursive/.
type RecursiveFileListRequest struct {
	// One or more directories to expand, relative to MUSIC_DIR. Empty means
	// the library root — the same "operate on root" sentinel FileList
	// accepts, so a client can send [] to mean "everything".
	//
	// A bare audio FILE is accepted here too, mirroring what the picker can
	// select: the caller gets that one file back and no directory walk
	// happens for it. Rejecting it would make this endpoint a special case
	// the caller has to pre-filter, and the caller already knows which of
	// its entries are files.
	Paths []string `json:"paths"`
	// Stop after this many audio files. 0 = unlimited.
	//
	// This exists because "expand the whole library" is a request a client
	// can make by accident (paths: [""]), and without a ceiling the response
	// is every track in the library — on a 50k-track NAS that is a JSON
	// array large enough to be a denial of service against the operator's
	// own gateway. `truncated` tells the client it did not get everything,
	// so a capped response is never mistaken for a complete one.
	Limit int `json:"limit"`
}

// RecursiveFileItem is one audio file found under a requested path.
//
// Deliberately NOT handler.FileItem: that carries the directory-listing
// chrome (id, icon, state, title, update_time, children) that only means
// something for a single level. This is a flat result set, and carrying
// fields a client ignores is how two shapes of the same concept drift.
type RecursiveFileItem struct {
	// Path relative to MUSIC_DIR, including the filename — the same string
	// SafeJoin(MUSIC_DIR, relPath) resolves to, so it can be fed straight
	// back into /api/music_id3/, /api/album_cover/, /api/delete_files/ etc.
	Path string `json:"path"`
	// Basename, which is what the table shows until tags arrive.
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Which of the request's `paths` this file came from. The worklist
	// dedupes per source directory, and without this the server would have
	// to invent a grouping the client cannot verify.
	Source string `json:"source"`
}

// RecursiveFileListResponse is the /api/file_list_recursive/ reply.
type RecursiveFileListResponse struct {
	Files []RecursiveFileItem `json:"files"`
	// True when `limit` cut the walk short. The client must surface this —
	// silently returning a partial list reads as "that was all of them".
	Truncated bool `json:"truncated"`
	// Directories entered, so a caller can see what the traversal cost even
	// when it returned few files.
	DirsVisited int `json:"dirs_visited"`
}

// MaxRecursiveDirs bounds how deep/wide one request will walk.
//
// A library is user data, not a shape the server gets to assume: the
// operator's tree may be 歌手/专辑/碟片/ and a symlink loop should not turn
// one request into an unbounded walk. 20000 directories is far beyond any
// sane music library (a 50k-track library has fewer than that directories)
// while still bounding a pathological tree.
const MaxRecursiveDirs = 20000

// MaxRecursiveFiles is the hard response ceiling when the client sends no
// limit of its own. Same reasoning as MaxRecursiveDirs: the response is
// built in memory and serialized to JSON, so this is what protects the
// gateway from a "paths: [\"\"]" mistake.
const MaxRecursiveFiles = 100000

// FileListRecursive handles POST /api/file_list_recursive/ — walks whole
// subtrees server-side and returns one flat list of audio files.
//
// WHY THIS EXISTS. The client used to expand a selected directory by asking
// /api/file_list/ once per directory, breadth-first: one request per
// directory, each level waiting on the one before it. That has two costs
// that no amount of client-side concurrency tuning removes:
//
//  1. Request count scales with the DIRECTORY COUNT, not the track count.
//     A 500-track library laid out as 歌手/专辑/碟片/ is ~2500 directories,
//     so ~2500 requests.
//  2. Latency scales with DEPTH, because BFS cannot start level N+1 until
//     level N has come back. A 4-deep tree is 4 serial round trips no
//     matter how wide each level is.
//
// Both come from the same root cause: the client was reconstructing a tree
// walk that the server can do in one pass. Walking here makes the cost one
// round trip regardless of how the operator's library is organised — which
// is the point, because that shape is data and this code cannot know it.
//
// Path containment is the same guarantee FileList gives: every requested
// path goes through utils.SafeJoin under MUSIC_DIR, and the walk stays
// inside the resolved root. Symlinked directories are NOT followed
// (filepath.WalkDir does not descend into symlinks), which is also what
// keeps a link pointing at / from turning this into a filesystem-wide scan.
func FileListRecursive(c *gin.Context) {
	var req RecursiveFileListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}

	root := utils.MusicRoot()
	paths := req.Paths
	if len(paths) == 0 {
		paths = []string{""}
	}
	limit := req.Limit
	if limit <= 0 || limit > MaxRecursiveFiles {
		limit = MaxRecursiveFiles
	}

	files := make([]RecursiveFileItem, 0, 64)
	dirsVisited := 0
	truncated := false

	for _, p := range paths {
		resolved, err := utils.SafeJoin(root, p)
		if err != nil {
			// One bad path must not abort the batch: the picker can hold
			// several directories and losing all of them because one was
			// deleted mid-selection is worse than skipping it. FileList
			// reports this as a failure for a single path, but here the
			// batch is the point.
			continue
		}

		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}

		// A selected FILE, not a directory. Answer it directly; walking it
		// would yield nothing and cost a syscall to discover that.
		if !info.IsDir() {
			if audioext.IsLibraryPath(resolved) {
				files = append(files, RecursiveFileItem{
					Path:   relOrBase(p, resolved),
					Name:   filepath.Base(resolved),
					Size:   info.Size(),
					Source: p,
				})
			}
			continue
		}

		walkErr := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// An unreadable directory is skipped, not fatal: one
				// permission-denied folder in a 2500-directory library
				// should not cost the user the other 2499.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				dirsVisited++
				if dirsVisited > MaxRecursiveDirs {
					truncated = true
					return fs.SkipAll
				}
				return nil
			}
			// Extensions are lowercased by IsLibraryPath, so `Track.FLAC`
			// counts — the same rule FileList applies.
			if !audioext.IsLibraryPath(path) {
				return nil
			}
			fi, statErr := d.Info()
			var size int64
			if statErr == nil {
				size = fi.Size()
			}
			files = append(files, RecursiveFileItem{
				Path:   relOrBase(p, path),
				Name:   d.Name(),
				Size:   size,
				Source: p,
			})
			if len(files) >= limit {
				truncated = true
				return fs.SkipAll
			}
			return nil
		})
		if walkErr != nil {
			// fs.SkipAll is how the walk says "stop, we hit a ceiling" —
			// that is `truncated`, already recorded, not a failure. A real
			// walk error (permissions on the root itself) just ends this
			// path's contribution.
			continue
		}
	}

	c.JSON(200, gin.H{
		"result":  true,
		"code":    "200",
		"message": "success",
		"data": RecursiveFileListResponse{
			Files:       files,
			Truncated:   truncated,
			DirsVisited: dirsVisited,
		},
	})
}

// AlbumCover handles GET /api/album_cover/?file_path=…&file_name=… —
// returns just the embedded cover image as binary.
//
// WHY THIS EXISTS. /api/music_id3/ returns the cover inline as a base64
// data URI, because the tag editor wants the whole record at once. That is
// the wrong shape for the worklist: a 300×300 JPEG is ~50 KB raw, ~67 KB
// once base64-encoded, and a reported-real library returns 3–15 MB per
// response because the embedded art is a full-resolution scan. Fetching that
// for 500 rows to paint 32-pixel thumbnails moved over a gigabyte to draw
// thumbnails — and stripHeavyFromRows then threw every byte of it away
// before persisting.
//
// So the batch path asks for tags WITHOUT artwork, and the table fetches
// the image only for rows it has actually scrolled into view. With
// virtualization that is ~13 rows on screen instead of 500.
//
// Caching is the browser's job, via Cache-Control: the cover bytes are
// immutable for a given file until something rewrites its tags, and the
// mtime-based ETag means a rewritten tag invalidates it naturally without
// this handler tracking anything.
//
// ERROR SHAPE — unlike every other handler here, this one does NOT use
// Failure(). Failure answers HTTP 200 with a JSON envelope carrying
// result:false, which is right for a fetch() caller that reads the body. An
// <img> tag cannot: it only sees a status code and renders a broken image.
// So this handler uses real status codes throughout, which is also what lets
// the client distinguish "no cover, use your gradient" (404) from "the
// server is broken" (5xx) without parsing anything.
func AlbumCover(c *gin.Context) {
	filePath := c.Query("file_path")
	fileName := c.Query("file_name")
	if fileName == "" {
		FailureStatus(c, http.StatusBadRequest, "file_name is required")
		return
	}

	root := utils.MusicRoot()
	dir, err := utils.SafeJoin(root, filePath)
	if err != nil {
		FailureStatus(c, http.StatusBadRequest, "路径不安全: "+err.Error())
		return
	}
	full, err := utils.SafeJoin(dir, fileName)
	if err != nil {
		FailureStatus(c, http.StatusBadRequest, "路径不安全: "+err.Error())
		return
	}
	if !audioext.IsLibraryPath(full) {
		// 400 not 404: the caller asked for something that is not a track,
		// which is a bug in the caller rather than a missing resource.
		FailureStatus(c, http.StatusBadRequest, "不是音频文件")
		return
	}

	// Reuse the same reader the tag path uses so the two cannot disagree
	// about which bytes count as "the cover" for a given container.
	info, err := ReadMusicTags(full)
	if err != nil {
		// A file that exists but will not parse is a server-side read
		// failure, and 500 says "this may work later" where 404 would say
		// "there is nothing here" and make the client cache the absence.
		FailureStatus(c, http.StatusInternalServerError, err.Error())
		return
	}
	if info == nil || info["artwork"] == "" {
		// No cover is a normal state, not an error: a lossless rip of a
		// single has none. 404 is what tells the caller to use its gradient
		// placeholder instead of treating this as a failure.
		FailureStatus(c, http.StatusNotFound, "没有内嵌封面")
		return
	}

	dataURI, _ := info["artwork"].(string)
	mime, raw, err := decodeDataURI(dataURI)
	if err != nil {
		FailureStatus(c, http.StatusInternalServerError, "封面解码失败: "+err.Error())
		return
	}

	etag := coverETag(full, info)
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, max-age=86400, must-revalidate")
	if match := c.GetHeader("If-None-Match"); match == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, mime, raw)
}

// coverETag identifies a cover by the file's identity and mtime.
//
// It has to include mtime: an edit that changes the embedded art must
// invalidate the cached bytes, and the handler has no other signal for
// that. Size alone would not notice a same-size replacement.
func coverETag(path string, info map[string]interface{}) string {
	var stamp string
	if fi, err := os.Stat(path); err == nil {
		stamp = strconv.FormatInt(fi.ModTime().UnixNano(), 36) + "-" +
			strconv.FormatInt(fi.Size(), 36)
	}
	// Artwork size in MB is coarse but changes when the art does; combined
	// with mtime it covers the rewrite case without hashing megabytes on
	// every request.
	if s, ok := info["artwork_size"].(int); ok {
		stamp += "-a" + strconv.Itoa(s)
	}
	return `"` + stamp + `"`
}

// decodeDataURI splits "data:<mime>;base64,<payload>" into its parts.
func decodeDataURI(s string) (mime string, raw []byte, err error) {
	const prefix = "data:"
	if !strings.HasPrefix(s, prefix) {
		return "", nil, os.ErrInvalid
	}
	rest := s[len(prefix):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", nil, os.ErrInvalid
	}
	meta := rest[:comma]
	payload := rest[comma+1:]
	if !strings.HasSuffix(meta, ";base64") {
		return "", nil, os.ErrInvalid
	}
	mime = strings.TrimSuffix(meta, ";base64")
	raw, err = base64Decode(payload)
	if err != nil {
		return "", nil, err
	}
	return mime, raw, nil
}

// relOrBase renders an absolute walked path as a MUSIC_DIR-relative one,
// falling back to the base name when the path is somehow outside the root
// (which SafeJoin should have prevented, but a fallback keeps the response
// well-formed instead of emitting an absolute server path to the client).
func relOrBase(source, abs string) string {
	if root := utils.MusicRoot(); strings.HasPrefix(abs, root) {
		rel := strings.TrimPrefix(abs, root)
		rel = strings.TrimPrefix(rel, "/")
		if rel != "" {
			return rel
		}
	}
	return filepath.Base(abs)
}
