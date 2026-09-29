package tasks

import (
	"context"
	"log"
	"time"

	"github.com/hibiken/asynq"

	"go-music-tag/internal/audiocache"
)

// PruneAudioCachePayload carries the policy rather than reading config at
// run time, so a scheduled run is a record of what the operator had set
// when it was scheduled and the manual enqueue path can ask for something
// else.
type PruneAudioCachePayload struct {
	// MaxBytes is the cap. 0 means "no cap" and is also how the manual
	// "clear everything eligible" path is expressed.
	MaxBytes int64 `json:"max_bytes"`
	// MinAgeMinutes protects recently written files from deletion. See
	// audiocache.Select for why that guard is not optional.
	MinAgeMinutes int `json:"min_age_minutes"`
}

// PruneAudioCacheHandler deletes the oldest cache files until the download
// cache is back under its size cap.
//
// It runs on a schedule, so its quiet path is the common one: a cache well
// under the cap must not produce a log line every 30 minutes, and must not
// write an audit row either. Only an actual deletion is worth a record.
type PruneAudioCacheHandler struct{}

func (h *PruneAudioCacheHandler) ProcessTask(ctx context.Context, t Task) error {
	var p PruneAudioCachePayload
	if raw, ok := t.Payload.(*PruneAudioCachePayload); ok && raw != nil {
		p = *raw
	}
	sel := audiocache.Select(p.MaxBytes, time.Duration(p.MinAgeMinutes)*time.Minute)
	if len(sel.Files) == 0 {
		if sel.RecentFiles > 0 {
			// Over the cap but everything in it is protected. Saying so is
			// the difference between "the cap is not working" and "the cap
			// declined to delete this hour's downloads".
			log.Printf("[audio-cache] %s over cap but kept %d recent file(s) (%s) within the %dm guard",
				audiocache.FormatBytes(sel.TotalBytes), sel.RecentFiles,
				audiocache.FormatBytes(sel.RecentBytes), p.MinAgeMinutes)
		}
		return nil
	}
	paths := make([]string, 0, len(sel.Files))
	for _, f := range sel.Files {
		paths = append(paths, f.Path)
	}
	res := audiocache.Remove(paths)
	log.Printf("[audio-cache] pruned %d file(s), freed %s (cache was %s, cap %s)",
		res.Removed, audiocache.FormatBytes(res.FreedBytes),
		audiocache.FormatBytes(sel.TotalBytes), audiocache.FormatBytes(p.MaxBytes))
	for path, why := range res.Failed {
		// One unlink failing is not worth retrying the whole run — the
		// files are already gone — but it must not vanish silently, since
		// a permission problem here means the cache can only grow.
		log.Printf("[audio-cache] WARNING: could not remove %s: %s", path, why)
	}
	return nil
}

// NewPruneAudioCacheMux registers the TypePruneAudioCache handler.
func NewPruneAudioCacheMux(mux *asynq.ServeMux, h Handler) {
	mux.HandleFunc(TypePruneAudioCache, (&asynqAdapter{
		decode: func(data []byte) (interface{}, error) {
			var p PruneAudioCachePayload
			err := Decode(data, &p)
			return &p, err
		},
		h: h,
	}).ProcessTask)
}
