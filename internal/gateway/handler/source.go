// Package handler — endpoint for the frontend to ask the backend about
// its registered plugins. Implements Stage A of `docs/plugable-plugins.md`
// (ListSources).
//
// Stage B (runtime YAML overrides: RefreshSourceOverrides +
// GetSourceOverride) was retired here — see the block comment above
// RefreshSourceOverrides for why. The two halves still share this file
// because they answer the same `sources` concern.
package handler

import (
	"net/http"
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

	for _, name := range plugin.ListTagSources() {
		ts, err := plugin.GetTagSource(name)
		if err != nil {
			continue // race-condition safety during shutdown
		}
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
		out = append(out, SourceInfo{
			Name:        ds.Name(),
			DisplayName: ds.DisplayName(),
			Kind:        "download",
			Searchable:  true,
			Lyric:       false,
			// DownloadSource implementations (YouTube) serve their
			// downloaded files via /api/stream once the /api/download/
			// task has landed the file on disk. We surface this as a
			// playable URL capability so the frontend's PlayButton
			// routes to that proxy. A future DownloadSource that doesn't
			// expose a preview path would override this here (or grow a
			// SupportsAudioURL method — deferred to avoid the "radical
			// rewrite" scope of the broader refactor).
			SupportsAudioUrl: true,
			DefaultOn:        true,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	SuccessData(c, out)
}

// ─── Stage B (runtime source overrides): retired ───────────────────────────
//
// REVIEW.md P0-2. The previous implementation of the two handlers below
// looked functional and returned 200 with a `refreshed` count, but the
// count was structurally always 0 and no override was ever applied.
//
// Why it could not work: RefreshOverrides walks the plugin registry and
// type-asserts each entry to plugin.SecretConfigurable /
// plugin.APIBaseConfigurable. In every real deployment the registry holds
// *plugin.GRPCTagSource values, which implement neither interface. The
// types that DO implement them (*migu.Server, *kuwo.Server, *kg.Server,
// *qmusic.Server) live in separate plugin processes/containers, and the
// gRPC contract in api/proto has no RPC capable of carrying an override
// across that boundary. So both assertions were always false.
//
// The unit tests passed because they registered in-process mocks that did
// implement the interfaces — a shape that never occurs in production.
//
// Decision: report the capability honestly (501) rather than keep a UI
// affirmation of work that does not happen. Reviving this feature requires
// a new `ApplyOverride` RPC in tag_source.proto plus locking and
// scheme-validation on the plugin-side setters (REVIEW.md P1-4); that is a
// feature, tracked separately, not a bug fix.
//
// Kept as registered routes returning 501 (rather than deleting them) so an
// older cached frontend bundle gets a clear machine-readable answer instead
// of a 404 that reads like a deploy problem.

// notImplementedDetail is shared by both retired Stage B handlers so the
// frontend only has to recognise one `error` discriminator.
const sourceOverrideNotImplemented = "source_override_not_implemented"

// SetSourceOverrideDir is retained as an accepted no-op so cmd/gateway
// keeps compiling without a conditional. The directory is no longer read.
func SetSourceOverrideDir(string) {}

// RefreshSourceOverrides handles POST /api/sources/refresh/.
//
//	→ 501 Not Implemented { "error": "source_override_not_implemented", "detail": ... }
//
// See the block comment above for why this is not a regression.
func RefreshSourceOverrides(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": sourceOverrideNotImplemented,
		"detail": "运行时插件 override 暂不支持：插件运行在独立进程中，" +
			"当前 gRPC 协议没有传递 override 的通道。请直接修改插件配置后重启插件容器。",
	})
}

// GetSourceOverride handles GET /api/sources/override/.
//
//	→ 501 Not Implemented { "error": "source_override_not_implemented", "detail": ... }
func GetSourceOverride(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error":  sourceOverrideNotImplemented,
		"detail": "运行时插件 override 暂不支持，无可展示的生效配置。",
	})
}
