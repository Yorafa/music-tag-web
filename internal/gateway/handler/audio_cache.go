package handler

import (
	"time"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/audiocache"
	"go-music-tag/internal/audit"
	"go-music-tag/internal/config"
)

// The download cache (AUDIO_CACHE_DIR) is the staging area /api/stream
// serves from and /api/download short-circuits on. It grew without bound:
// every track previewed or added left a copy behind, in a docker named
// volume that survives `compose down`. The worker now enforces a size cap
// on a schedule; these two endpoints are the other half — what is actually
// on disk, and a button for the human who wants the space back now.
//
// Neither endpoint touches the library. A cache file is a copy: deleting one
// costs a re-download if it is wanted again, and nothing in MUSIC_DIR points
// at it (persistCacheToLibrary copies, it does not link or move).

// cacheSourceUsage is the per-source breakdown in the usage report.
type cacheSourceUsage struct {
	Bytes      int64  `json:"bytes"`
	BytesHuman string `json:"bytes_human"`
	Files      int    `json:"files"`
}

type cacheUsageReport struct {
	Root       string                      `json:"root"`
	Exists     bool                        `json:"exists"`
	Bytes      int64                       `json:"bytes"`
	BytesHuman string                      `json:"bytes_human"`
	Files      int                         `json:"files"`
	BySource   map[string]cacheSourceUsage `json:"by_source"`
	// AutoPrune is the worker's periodic policy, surfaced so the settings
	// page can say why the cache is the size it is instead of leaving the
	// user to wonder.
	AutoPrune struct {
		Enabled       bool   `json:"enabled"`
		MaxMB         int64  `json:"max_mb"`
		MaxHuman      string `json:"max_human"`
		MinAgeMinutes int    `json:"min_age_minutes"`
	} `json:"auto_prune"`
	// MinAgeMinutes is the guard a manual clear applies too, so the dialog
	// can promise the same protection the automatic one gives.
	MinAgeMinutes int `json:"min_age_minutes"`
}

func buildCacheUsage() cacheUsageReport {
	cfg := config.Current()
	st := audiocache.Inspect()
	rep := cacheUsageReport{
		Root:          st.Root,
		Exists:        st.Exists,
		Bytes:         st.Bytes,
		BytesHuman:    audiocache.FormatBytes(st.Bytes),
		Files:         st.Files,
		BySource:      map[string]cacheSourceUsage{},
		MinAgeMinutes: cfg.AudioCacheMinAgeMinutes,
	}
	for src, u := range st.BySource {
		rep.BySource[src] = cacheSourceUsage{
			Bytes:      u.Bytes,
			BytesHuman: audiocache.FormatBytes(u.Bytes),
			Files:      u.Files,
		}
	}
	rep.AutoPrune.Enabled = cfg.AudioCacheMaxMB > 0
	rep.AutoPrune.MaxMB = cfg.AudioCacheMaxMB
	rep.AutoPrune.MaxHuman = audiocache.FormatBytes(cfg.AudioCacheMaxMB * 1024 * 1024)
	rep.AutoPrune.MinAgeMinutes = cfg.AudioCacheMinAgeMinutes
	return rep
}

// GetAudioCache handles GET /api/audio_cache/ — what the download cache is
// holding right now. Read-only, so GET is right.
func GetAudioCache(c *gin.Context) {
	SuccessData(c, buildCacheUsage())
}

type cacheClearReport struct {
	Removed     int               `json:"removed"`
	FreedBytes  int64             `json:"freed_bytes"`
	FreedHuman  string            `json:"freed_human"`
	KeptRecent  int               `json:"kept_recent"`
	KeptHuman   string            `json:"kept_recent_human"`
	Failed      map[string]string `json:"failed"`
	All         bool              `json:"all"`
	BeforeHuman string            `json:"before_human"`
	After       cacheUsageReport  `json:"after"`
}

// ClearAudioCache handles POST /api/audio_cache/clear.
//
// Body: { all?: bool }. `all` drops the recent-file guard, which the
// dialog only sends after an explicit second confirmation. The guard
// exists because /api/stream ServeFiles straight out of this directory:
// a file deleted while it is the one being played stops playing, and one
// deleted moments before a download task copies it into the library fails
// that copy. `all` is the escape hatch for someone who wants the disk
// back regardless, and it is a separate click so it cannot happen by
// accident.
func ClearAudioCache(c *gin.Context) {
	var req struct {
		All bool `json:"all"`
	}
	// A body is optional: no body means "clear everything not protected".
	_ = c.ShouldBindJSON(&req)

	cfg := config.Current()
	minAge := time.Duration(cfg.AudioCacheMinAgeMinutes) * time.Minute
	if req.All {
		minAge = 0
	}
	// limit 0 = no cap = "everything eligible", which is what a manual
	// clear means. The cap is the worker's policy, not this button's.
	sel := audiocache.Select(0, minAge)
	paths := make([]string, 0, len(sel.Files))
	for _, f := range sel.Files {
		paths = append(paths, f.Path)
	}
	res := audiocache.Remove(paths)

	rep := cacheClearReport{
		Removed:     res.Removed,
		FreedBytes:  res.FreedBytes,
		FreedHuman:  audiocache.FormatBytes(res.FreedBytes),
		KeptRecent:  sel.RecentFiles,
		KeptHuman:   audiocache.FormatBytes(sel.RecentBytes),
		Failed:      res.Failed,
		All:         req.All,
		BeforeHuman: audiocache.FormatBytes(sel.TotalBytes),
		After:       buildCacheUsage(),
	}

	status := audit.StatusSuccess
	if len(res.Failed) > 0 && res.Removed == 0 {
		status = audit.StatusFailed
	} else if len(res.Failed) > 0 {
		status = audit.StatusPartial
	}
	// Audited because a human asked for it. The scheduled prune is not:
	// it runs every 30 minutes and would drown the log this table is for.
	audit.Log(c.Request.Context(), audit.ActionAudioCacheClear, rep.After.Root, "admin", status,
		res.Removed+len(res.Failed), map[string]interface{}{
			"removed":      res.Removed,
			"freed_bytes":  res.FreedBytes,
			"kept_recent":  sel.RecentFiles,
			"failed":       len(res.Failed),
			"all":          req.All,
			"requested_by": "audio_cache_clear",
			"before_human": rep.BeforeHuman,
			"after_human":  rep.After.BytesHuman,
		}, nil)

	SuccessData(c, rep)
}
