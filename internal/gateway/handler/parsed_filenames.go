// Package handler — parsed_filenames.go backs the 解析文件名 write.
//
// The flow:
//
//  1. POST /api/tag/apply_parsed_filenames/ (paths, options) →
//     - SafeJoin each path under MUSIC_DIR.
//     - PortParseFilename → cache.ParsedResult per path.
//     - Re-verify every path.
//     - Skip unparsable rows.
//     - Enqueue asynq TypeApplyParsedFilenames; return { task_id }.
//
// # The client previews, locally
//
// This used to be a two-call round trip: preview_parse_filenames parsed
// the paths, saved them behind a 10-minute one-shot token, and the apply
// spent that token. The token existed so the write could not be handed a
// plan the client had never seen — a good property for an endpoint whose
// caller is a script, and a bad one for a dialog: the client now renders
// the plan itself from each row's filename (frontend/parseAssist.ts
// mirrors PortParseFilename), and holding a token it does not read only
// bought an expired-preview 401 to a dialog left open over lunch.
//
// So the apply takes the paths and the rule together and derives the plan
// with the same code either way. The token path is kept so an older
// client — or a script that wants to preview and then write exactly what
// it previewed — still works.
//
// Security: every path is SafeJoined here and SafeAbs'd again on the way
// out. A 401 envelope (not 404) on ErrTokenExpired tells the frontend
// "re-preview" rather than "missing resource" — matches the
// auth/refresh-token retry UX.
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

// MaxParseRows caps a single apply to avoid DoS by a hostile caller
// posting millions of paths in one request. 5000 maps to the largest sane
// library bulk-edit (a label's full back catalogue) and stays under the
// 28 MiB body limit enforced by the /api middleware.
const MaxParseRows = 5000

// parseRequest is the shape both endpoints accept.
type parseRequest struct {
	Paths   []string           `json:"paths"`
	Options utils.ParseOptions `json:"options"`
}

// planPaths parses `paths` under MUSIC_DIR, SafeJoin-ing each one.
//
// It returns a 422-shaped error and false when a path escapes the root, so
// both callers answer a suspicious payload the same way instead of one of
// them inventing a second rule for it.
func planPaths(c *gin.Context, paths []string, opts utils.ParseOptions) ([]cache.ParsedResult, bool) {
	// Compile before touching any path: the pattern is the one input a
	// typo can ruin silently, and the error is worth reporting on its own
	// rather than as 5000 unparsable rows.
	pattern, err := utils.CompilePattern(opts.Pattern)
	if err != nil {
		Failure(c, err.Error())
		return nil, false
	}

	root := utils.MusicRoot()
	results := make([]cache.ParsedResult, 0, len(paths))
	for _, rel := range paths {
		rel = strings.TrimPrefix(rel, "/")
		abs, err := utils.SafeJoin(root, rel)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":  "unsafe_path",
				"path":   rel,
				"detail": err.Error(),
			})
			return nil, false
		}
		parsed := utils.PortParseFilenameCompiled(stripDir(abs), opts, pattern)
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
	return results, true
}

// checkPathCount answers the two refusals both endpoints share.
func checkPathCount(c *gin.Context, n int) bool {
	if n == 0 {
		Failure(c, "paths is empty")
		return false
	}
	if n > MaxParseRows {
		Failure(c, fmt.Sprintf("too many paths (max %d)", MaxParseRows))
		return false
	}
	return true
}

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
//	→ Failure(...) if paths is missing / empty / over MaxParseRows, or
//	           the pattern does not compile / names a field that does
//	           not exist. Note the envelope convention: Failure answers
//	           HTTP 200 with result:false and code "400" (see
//	           handler/response.go), and the frontend turns that into a
//	           thrown Error carrying `message` (api/envelope.ts).
//	→ 422 Unprocessable if any path fails SafeJoin (suspicious payload) —
//	           that one is a real status code, written directly.
//
// # Who calls this now
//
// The app does not: 解析文件名 renders its plan locally. This endpoint
// stays because it is the only way to obtain a token, which is the only
// way to write a plan that was parsed by the server rather than by the
// client — the property a script wants and the dialog gave up on
// purpose. The parsing below is the same code the apply runs.
//
// # Why a bad pattern fails the request and is not a per-row unparsable
//
// The pattern used to be compiled inside the per-file loop, so a typo
// turned every row unparsable and the response said nothing about why —
// 5000 files, one silent mistake. It is now compiled once, in
// planPaths, and the error names the offending group and lists the
// fields that exist. The preview is worthless with a broken pattern, so
// failing the request is more useful than returning a table of blanks.
//
// The client checks the same things before rendering anything
// (frontend/src/components/scraper/parseAssist.ts) and additionally
// rejects constructs Go's RE2 refuses but V8 accepts — lookahead,
// lookbehind, backreferences — which is verified against
// regexp.Compile in internal/utils/filenames_preset_test.go rather than
// assumed.
func PreviewParseFilenames(c *gin.Context) {
	var req parseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}
	if !checkPathCount(c, len(req.Paths)) {
		return
	}
	results, ok := planPaths(c, req.Paths, req.Options)
	if !ok {
		return
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
// apply call. Path key is the EXACT path the server parsed (preserves
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
// Two request shapes, one plan:
//
//	{ "paths": ["a/b.flac", ...], "options": { "pattern": "..." } }   // current
//	{ "token": "abc123..." }                                          // legacy
//
// Either way every row is re-verified against MUSIC_DIR on the way out,
// and unparsable rows are kept in the bundle so the worker skips them
// silently — an operator may have selected 1000 files and only want the
// 700 that parsed.
//
//	→ 200 OK { task_id, state }
//	→ 401 Unauth if ErrTokenExpired (a legacy token caller must
//	   re-preview).
//	→ 422 Unprocessable if any path fails SafeJoin on re-verify.
func ApplyParsedFilenames(c *gin.Context) {
	var req struct {
		Token     string           `json:"token"`
		Overrides []ApplyOverwrite `json:"overrides"`
		parseRequest
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}

	var rows []cache.ParsedResult
	switch {
	case req.Token != "":
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
		rows = bundle.Results
	default:
		if !checkPathCount(c, len(req.Paths)) {
			return
		}
		var ok bool
		rows, ok = planPaths(c, req.Paths, req.Options)
		if !ok {
			return
		}
	}

	// Index overrides by absolute path for O(1) lookup.
	ovrByPath := make(map[string]ApplyOverwrite, len(req.Overrides))
	for _, ov := range req.Overrides {
		if ov.Path == "" {
			continue
		}
		ovrByPath[ov.Path] = ov
	}

	// Per-row merge + re-verification.
	root := utils.MusicRoot()
	finalRows := make([]cache.ParsedResult, 0, len(rows))
	for _, row := range rows {
		// Defence-in-depth: re-validate even though the plan above did it.
		// Cheap; runs once per row. SafeAbs rather than
		// SafeJoin(TrimPrefix(...)) for the reason in update.go —
		// TrimPrefix is a no-op when the prefix does not match, and
		// SafeJoin would then treat the absolute path as relative
		// (REVIEW.md P2-6).
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
