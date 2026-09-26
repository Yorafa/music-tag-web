// Package handler — parsed_filenames.go backs the C.2 Filename Parse
// preview-and-apply round-trip. The flow:
//
//  1. POST /api/tag/preview_parse_filenames/ (paths?) →
//     - SafeJoin each path under MUSIC_DIR.
//     - PortParseFilename → cache.ParsedResult.
//     - Save bundle → return token (and same results array).
//  2. POST /api/tag/apply_parsed_filenames/ (token, overrides?) →
//     - Load bundle, apply per-path overrides.
//     - Re-SafeJoin each row's path (defence-in-depth).
//     - Skip unparsable rows.
//     - Enqueue asynq TypeApplyParsedFilenames; return { task_id }.
//
// Security: every path is SafeJoined. Token TTL is enforced inside
// cache.DefaultPreviewCache.Load. A 401 envelope (not 404) on
// ErrTokenExpired tells the frontend "re-preview" rather than
// "missing resource" — matches the auth/refresh-token retry UX.
package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"go-music-tag/internal/cache"
	"go-music-tag/internal/taskclient"
	"go-music-tag/internal/tasks"
	"go-music-tag/internal/utils"
)

// MaxPreviewRows caps a single preview to avoid DoS by a hostile
// caller posting millions of paths in one request. 5000 maps to the
// largest sane library bulk-edit (a label's full back catalogue) and
// stays under the 8 MiB body limit enforced by the /api middleware.
const MaxPreviewRows = 5000

// PreviewParseFilenames handles POST /api/tag/preview_parse_filenames/.
//
//	{
//	  "paths":   ["album/Artist - Album - 01 - Title.flac", ...], // relative to MUSIC_DIR
//	  "options": {
//	    "separator": "\\s*-\\s*",                                  // optional
//	    "pattern":   "^(?P<artist>.+?) - (?P<album>.+?) - ...$"       // optional
//	  }
//	}
//
//	→ 200 OK { token, results: [...same shape as cache.ParsedResult...] }
//	→ 400 Bad  if paths is missing / empty / over MaxPreviewRows, or the
//	           pattern does not compile / names a field that does not exist.
//	→ 422 Unprocessable if any path fails SafeJoin (suspicious payload).
//
// # Why a bad pattern is a 400 and not a per-row unparsable
//
// The pattern used to be compiled inside the per-file loop, so a typo
// turned every row unparsable and the response said nothing about why —
// 5000 files, one silent mistake. It is now compiled once, here, and the
// error names the offending group and lists the fields that exist. The
// preview is worthless with a broken pattern, so failing the request is
// more useful than returning a table of blanks.
func PreviewParseFilenames(c *gin.Context) {
	var req struct {
		Paths   []string           `json:"paths" binding:"required"`
		Options utils.ParseOptions `json:"options"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}
	if len(req.Paths) == 0 {
		Failure(c, "paths is empty")
		return
	}
	if len(req.Paths) > MaxPreviewRows {
		Failure(c, fmt.Sprintf("too many paths (max %d)", MaxPreviewRows))
		return
	}

	// Compile before touching any path: the pattern is the one input a
	// typo can ruin silently, and the error is worth reporting on its own
	// rather than as 5000 unparsable rows.
	pattern, err := utils.CompilePattern(req.Options.Pattern)
	if err != nil {
		Failure(c, err.Error())
		return
	}

	root := utils.MusicRoot()
	results := make([]cache.ParsedResult, 0, len(req.Paths))
	for _, rel := range req.Paths {
		rel = strings.TrimPrefix(rel, "/")
		abs, err := utils.SafeJoin(root, rel)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":  "unsafe_path",
				"path":   rel,
				"detail": err.Error(),
			})
			return
		}
		parsed := utils.PortParseFilenameCompiled(stripDir(abs), req.Options, pattern)
		status := parsed.Status
		if status == "" {
			status = utils.StatusUnparsable
		}
		results = append(results, cache.ParsedResult{
			Path:        abs,
			Title:       parsed.Title,
			Artist:      parsed.Artist,
			Album:       parsed.Album,
			AlbumArtist: parsed.AlbumArtist,
			Genre:       parsed.Genre,
			Year:        parsed.Year,
			TrackNumber: parsed.TrackNumber,
			DiscNumber:  parsed.DiscNumber,
			Status:      status,
		})
	}

	token, err := cache.DefaultPreviewCache.Save(cache.ParsedBundle{Results: results})
	if err != nil {
		Failure(c, "save preview: "+err.Error())
		return
	}
	SuccessData(c, gin.H{
		"token":   token,
		"results": results,
	})
}

// ApplyOverwrite is one per-row override the frontend can submit in the
// apply call. Path key is the EXACT path returned by preview (preserves
// ambiguity resolution — if the user typed a new artist in the modal
// for a row, that override wins over the parsed value).
type ApplyOverwrite struct {
	Path        string `json:"path"`
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"albumartist,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Year        string `json:"year,omitempty"`
	TrackNumber string `json:"tracknumber,omitempty"`
	DiscNumber  string `json:"discnumber,omitempty"`
}

// ApplyParsedFilenames handles POST /api/tag/apply_parsed_filenames/.
//
//	{
//	  "token":     "abc123...",
//	  "overrides": [{"path": "/music/...", "artist": "X", "title": "Y"}]
//	}
//
//	→ 200 OK { task_id, state }
//	→ 401 Unauth if ErrTokenExpired (re-preview needed).
//	→ 422 Unprocessable if any path fails SafeJoin on re-verify.
func ApplyParsedFilenames(c *gin.Context) {
	var req struct {
		Token     string           `json:"token" binding:"required"`
		Overrides []ApplyOverwrite `json:"overrides"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}

	bundle, err := cache.DefaultPreviewCache.Load(req.Token)
	if err != nil {
		if errors.Is(err, cache.ErrTokenExpired) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":  "preview_expired",
				"detail": "re-preview required (preview TTL elapsed or token unknown)",
			})
			return
		}
		Failure(c, "load preview: "+err.Error())
		return
	}

	// Index overrides by absolute path for O(1) lookup.
	ovrByPath := make(map[string]ApplyOverwrite, len(req.Overrides))
	for _, ov := range req.Overrides {
		if ov.Path == "" {
			continue
		}
		ovrByPath[ov.Path] = ov
	}

	// Per-row merge + re-verification. Keep the unparsable rows in the
	// bundle so the worker can skip them silently (operator may have
	// previewed 1000 files and only wants to write the 700 that parsed).
	root := utils.MusicRoot()
	finalRows := make([]cache.ParsedResult, 0, len(bundle.Results))
	for _, row := range bundle.Results {
		// Defence-in-depth: re-validate even though preview did it. Cheap;
		// runs once per row. SafeAbs rather than SafeJoin(TrimPrefix(...))
		// for the reason in update.go — TrimPrefix is a no-op when the
		// prefix does not match, and SafeJoin would then treat the
		// absolute path as relative (REVIEW.md P2-6).
		safe, sErr := utils.SafeAbs(root, row.Path)
		if sErr != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":  "unsafe_path",
				"detail": sErr.Error(),
			})
			return
		}
		row.Path = safe
		if ov, ok := ovrByPath[safe]; ok {
			// A non-empty override replaces the parsed value; an empty one
			// leaves it alone. That is the whole semantic, and it is why
			// this path cannot delete a tag: there is no way to say "clear
			// it" here, by design. Clearing is the batch editor's job
			// (see handler.tagIntent for the wire shape it uses).
			overrode := false
			for _, f := range []struct {
				dst *string
				val string
			}{
				{&row.Title, ov.Title},
				{&row.Artist, ov.Artist},
				{&row.Album, ov.Album},
				{&row.AlbumArtist, ov.AlbumArtist},
				{&row.Genre, ov.Genre},
				{&row.Year, ov.Year},
				{&row.TrackNumber, ov.TrackNumber},
				{&row.DiscNumber, ov.DiscNumber},
			} {
				if f.val != "" {
					*f.dst = f.val
					overrode = true
				}
			}
			// A row the pattern could not read becomes writable once the
			// user supplies a value by hand — the override IS the parse at
			// that point, and the worker skips unparsable rows. The comment
			// that used to sit here claimed the opposite of what the code
			// did, which is worse than either behaviour: it would have
			// stopped the next reader from fixing the bug.
			if overrode && row.Status == utils.StatusUnparsable {
				row.Status = utils.StatusOK
			}
		}
		finalRows = append(finalRows, row)
	}

	taskclient.Init()
	t, err := tasks.NewTypedTask(tasks.TypeApplyParsedFilenames,
		&tasks.ApplyParsedFilenamesPayload{Results: finalRows},
		asynq.Queue("default"), asynq.MaxRetry(3),
		asynq.Timeout(2*time.Hour),
	)
	if err != nil {
		Failure(c, "encode task: "+err.Error())
		return
	}
	info, err := taskclient.Enqueue(t)
	if err != nil {
		Failure(c, "enqueue: "+err.Error())
		return
	}

	SuccessData(c, gin.H{
		"task_id":      info.ID,
		"type":         info.Type,
		"queue":        info.Queue,
		"state":        info.State,
		"row_count":    len(finalRows),
		"apply_target": "tags_only", // scope: artist + title; rename / covers not touched
	})
}

// stripDir returns the basename of an absolute path. Mirrors the
// TS mirror and the filename-parser contract test, which both operate
// on basenames only.
func stripDir(abs string) string {
	if i := strings.LastIndex(abs, "/"); i >= 0 {
		return abs[i+1:]
	}
	return abs
}
