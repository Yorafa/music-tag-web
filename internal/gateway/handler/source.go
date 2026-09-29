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

// ─── 运行时 source 覆盖：不可用 ────────────────────────────────────────────
//
// 下面两个 handler 返回 501，因为这个能力在当前架构下做不到。
//
// 做不到的原因：RefreshOverrides 遍历插件注册表，把每个条目断言成
// plugin.SecretConfigurable / plugin.APIBaseConfigurable。而任何真实部署里
// 注册表装的全是 *plugin.GRPCTagSource，它不实现这两个接口；真正实现它们的
// 类型（*migu.Server / *kuwo.Server / *kg.Server / *qmusic.Server）在各自独立的
// 插件进程里，而 api/proto 里没有任何 RPC 能把一次覆盖送过这个边界。所以两次
// 断言恒为 false。
//
// 单测覆盖不到它，是因为测试注册的是实现了那两个接口的进程内 mock——生产里
// 不存在这种形状。
//
// 要真的支持它，需要在 tag_source.proto 里新增 `ApplyOverride` RPC，
// 并给插件侧的 setter 加锁和 scheme 校验。那是一项新功能，不是修 bug。
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
