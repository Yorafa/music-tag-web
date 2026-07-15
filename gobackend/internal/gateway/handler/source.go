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
	DefaultOn   bool `json:"default_on"` // server advisory only; user prefs live in the frontend
}

// ListSources handles GET /api/sources/.
// Returns every registered tag- and download-source with capability flags.
// The frontend renders its picker/toggle off this list, so adding a new
// built-in plugin only requires registering it server-side.
//
// Output is sorted alphabetically by `name` for stable UI ordering.
func ListSources(c *gin.Context) {
	out := []SourceInfo{}

	for _, name := range plugin.ListTagSources() {
		ts, err := plugin.GetTagSource(name)
		if err != nil {
			continue // race-condition safety during shutdown
		}
		out = append(out, SourceInfo{
			Name:        ts.Name(),
			DisplayName: ts.DisplayName(),
			Kind:        "tag",
			Searchable:  ts.SupportsSearch(),
			Lyric:       ts.SupportsLyric(),
			SupportsId3: ts.SupportsId3(),
			DefaultOn:   true,
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
		out = append(out, SourceInfo{
			Name:        ds.Name(),
			DisplayName: ds.DisplayName(),
			Kind:        "download",
			Searchable:  true,
			Lyric:       false,
			DefaultOn:   true,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	SuccessData(c, out)
}
