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
//	  "paths":   ["album/Artist - Title.flac", ...],   // relative to MUSIC_DIR
//	  "options": {"separator": "\\s*-\\s*", "fallback_regex": "..."} // optional
//	}
//
//	→ 200 OK { token, results: [...same shape as cache.ParsedResult...] }
//	→ 400 Bad  if paths is missing / empty / over MaxPreviewRows.
//	→ 422 Unprocessable if any path fails SafeJoin (suspicious payload).
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
		parsed := utils.PortParseFilename(stripDir(abs), req.Options)
		status := parsed.Status
		if status == "" {
			status = utils.StatusUnparsable
		}
		results = append(results, cache.ParsedResult{
			Path:   abs,
			Artist: parsed.Artist,
			Title:  parsed.Title,
			Status: status,
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
	Path   string `json:"path"`
	Artist string `json:"artist,omitempty"`
	Title  string `json:"title,omitempty"`
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
			if ov.Artist != "" {
				row.Artist = ov.Artist
			}
			if ov.Title != "" {
				row.Title = ov.Title
			}
			// Overrides do not flip an "unparsable" to "ok" — the worker
			// still skips it. This is intentional: if the file wasn't
			// parsed at preview, the user is overriding blindly.
			// Future: surface "override applied to unparsable" badge.
			if row.Status == utils.StatusUnparsable && (ov.Artist != "" || ov.Title != "") {
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
