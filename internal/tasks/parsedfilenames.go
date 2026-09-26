// Package tasks — parsedfilenames.go is the C.2 Filename Parse
// worker. Triggered asynchronously by handler.ApplyParsedFilenames.
//
// Scope (deliberately narrow):
//   - Writes the tag fields the parser produced, and nothing else.
//   - A field the parser left empty is not written, so this can fill in
//     what a file is missing but can never delete a tag. Clearing is the
//     batch editor's job and goes through a different endpoint on purpose:
//     "the name said nothing about an album" and "delete the album" must
//     not be the same request.
//   - Skips "unparsable" rows silently.
//   - NO rename, NO sidecar lyrics/cover, NO template substitution.
//     The handler-level applyFileUpdate covers those; the worker is
//     scoped to bulk application of parsed fields so a 5000-row batch
//     finishes in seconds rather than seconds-per-row with cover fetches.
//
// Defence-in-depth: every path is re-SafeJoined under MUSIC_DIR
// before tag.Write even though the handler already did it. A future
// design that lets the handler pass through user-provided absolute
// paths must keep this guard intact.
package tasks

import (
	"context"
	"fmt"
	"log"
	"strings"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/tag"
	"go-music-tag/internal/utils"
)

// HandleApplyParsedFilenames is the asynq ProcessFunc adapter for
// TypeApplyParsedFilenames. Wired via NewApplyParsedFilenamesMux.
//
// Returns nil on per-row errors so a single bad file does not
// permanently fail and retry the entire bulk (asynq's MaxRetry=3
// would re-run all 5000 rows on a transient I/O glitch).
func HandleApplyParsedFilenames(ctx context.Context, t Task) error {
	payload, ok := t.Payload.(*ApplyParsedFilenamesPayload)
	if !ok {
		return fmt.Errorf("apply_parsed_filenames: bad payload type %T", t.Payload)
	}
	root := utils.MusicRoot()

	done, skipped, failed := 0, 0, 0
	for _, row := range payload.Results {
		if row.Status == utils.StatusUnparsable || row.Status == "" {
			skipped++
			continue
		}
		// Defence-in-depth SafeJoin.
		rel := strings.TrimPrefix(row.Path, root)
		safe, err := utils.SafeJoin(root, rel)
		if err != nil {
			log.Printf("apply_parsed_filenames: unsafe path %q: %v", row.Path, err)
			failed++
			continue
		}
		if row.Empty() {
			// Nothing was parsed and nothing was typed in: a no-op row.
			skipped++
			continue
		}
		upd := &tag.TagUpdate{}
		if row.Artist != "" {
			// Single-artist filename parses → []string{"X"}.
			// Comma-split mirrors applyFileUpdate's convention so an
			// operator-typed "Artist1, Artist2" still fans out into
			// tags correctly. (Pure splits on "/" would too; we keep
			// the same "," as the existing update handler.)
			parts := strings.Split(row.Artist, ",")
			cleaned := make([]string, 0, len(parts))
			for _, p := range parts {
				if v := strings.TrimSpace(p); v != "" {
					cleaned = append(cleaned, v)
				}
			}
			if len(cleaned) == 0 {
				cleaned = []string{row.Artist}
			}
			upd.Artist = cleaned
		}
		// Every remaining field is "write it if the row carries a value".
		// Each is a pointer into a local, which is fine: tag.Write reads
		// the values before returning, and each iteration of this loop
		// gets its own.
		for _, f := range []struct {
			val string
			dst **string
		}{
			{row.Title, &upd.Title},
			{row.Album, &upd.Album},
			{row.AlbumArtist, &upd.AlbumArtist},
			{row.Genre, &upd.Genre},
			{row.Year, &upd.Year},
			{row.TrackNumber, &upd.TrackNumber},
			{row.DiscNumber, &upd.DiscNumber},
		} {
			if f.val == "" {
				continue
			}
			v := f.val
			*f.dst = &v
		}
		if err := tag.Write(safe, upd); err != nil {
			log.Printf("apply_parsed_filenames: write %s: %v", safe, err)
			failed++
			continue
		}
		done++
	}
	log.Printf("apply_parsed_filenames: done=%d skipped=%d failed=%d (out of %d)",
		done, skipped, failed, len(payload.Results))

	status := audit.StatusSuccess
	if failed > 0 && done > 0 {
		status = audit.StatusPartial
	} else if failed > 0 && done == 0 {
		status = audit.StatusFailed
	} else if done == 0 && skipped > 0 {
		status = audit.StatusSkipped
	}

	audit.Log(ctx, audit.ActionFilenameParse, fmt.Sprintf("%d 个文件", len(payload.Results)), "worker", status, done, map[string]interface{}{
		"total":   len(payload.Results),
		"done":    done,
		"skipped": skipped,
		"failed":  failed,
	}, nil)

	return nil
}
