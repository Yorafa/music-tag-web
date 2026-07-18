// Package handler — endpoint for the frontend to ask the backend about
// its registered plugins. Implements Stage A of `docs/plugable-plugins.md`.
package handler

import (
	"sort"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/plugin"
)

// SourceInfo is one entry in the GET /api/sources/ response.
// The fields expose exactly what the frontend renders in the dynamic
// source picker + the per-source toggle in Settings.
type SourceInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"` // "tag" | "download"
	Searchable  bool   `json:"searchable"`
	Lyric       bool   `json:"lyric"`
	// SupportsId3 distinguishes sources that answer FetchID3ByTitle
	// (which `TagEditor` uses for tag-by-title scraping) from
	// search-only / download-only sources. Always false for `kind:"download"`
	// entries — `DownloadSource` has no equivalent capability.
	SupportsId3 bool `json:"supports_id3"`
	// SupportsAudioUrl mirrors plugin.PluginInfoResponse.supports_audio_url
	// (gated by the first GetPluginInfo round-trip per plugin). When false
	// the source is metadata-only (musicbrainz / acoustid) — the frontend's
	// PlayButton should surface "no preview available" rather than fetch
	// /api/stream?src=&id= for the user. Always false for `kind:"download"`
	// entries — DownloadSource has no streaming contract.
	SupportsAudioUrl bool `json:"supports_audio_url"`
	DefaultOn        bool `json:"default_on"` // server advisory only; user prefs live in the frontend
}

// ListSources handles GET /api/sources/.
// Returns every registered tag- and download-source with capability flags.
// The frontend renders its picker/toggle off this list, so adding a new
// built-in plugin only requires registering it server-side.
//
// Output is sorted alphabetically by `name` for stable UI ordering.
func ListSources(c *gin.Context) {
	out := []SourceInfo{}
	// seen guards the defensive fallback below so a registered YouTube
	// (via gRPC dial) isn't duplicated by the synthesised entry.
	seen := map[string]bool{}

	for _, name := range plugin.ListTagSources() {
		ts, err := plugin.GetTagSource(name)
		if err != nil {
			continue // race-condition safety during shutdown
		}
		seen[name] = true
		out = append(out, SourceInfo{
			Name:             ts.Name(),
			DisplayName:      ts.DisplayName(),
			Kind:             "tag",
			Searchable:       ts.SupportsSearch(),
			Lyric:            ts.SupportsLyric(),
			SupportsId3:      ts.SupportsId3(),
			SupportsAudioUrl: ts.SupportsAudioURL(),
			DefaultOn:        true,
		})
	}

	for _, name := range plugin.ListDownloadSources() {
		ds, err := plugin.GetDownloadSource(name)
		if err != nil {
			continue
		}
		// DownloadSource has no `SupportsSearch` method. Surface every
		// download source as searchable because the only download-route
		// capability the frontend reads is "presents a song-like list".
		seen[name] = true
		out = append(out, SourceInfo{
			Name:        ds.Name(),
			DisplayName: ds.DisplayName(),
			Kind:        "download",
			Searchable:  true,
			Lyric:       false,
			// YouTube is the only DownloadSource with a playback contract:
			// the gateway serves its YT_TMP_DIR/<id>.* files via
			// /api/stream?src=youtube once /api/youtube_download/ has
			// landed the file. Other future download sources get false
			// here unless they grow an analogous preview path.
			SupportsAudioUrl: ds.Name() == "youtube",
			DefaultOn:        true,
		})
	}

	// Defensive fallback: ensure YouTube appears in /api/sources/ even
	// when the gRPC plugin server hasn't registered — whether because
	// PLUGIN_YOUTUBE_ADDR wasn't supplied in env, the first-boot dial
	// hasn't completed yet, or the plugin container is unreachable.
	// Without this, the frontend source picker would hide YouTube
	// entirely instead of letting /api/youtube_search/ surface a
	// readable "youtube plugin not available" error. The `seen[name]`
	// guard above avoids duplicating the entry when the plugin IS
	// registered via the registry.
	if !seen["youtube"] {
		out = append(out, SourceInfo{
			Name:             "youtube",
			DisplayName:      "YouTube",
			Kind:             "download",
			Searchable:       true,
			Lyric:            false,
			SupportsId3:      false,
			SupportsAudioUrl: true,
			DefaultOn:        true,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	SuccessData(c, out)
}
