// Package plugin defines the core interfaces and registry for tag-source
// and download-source plugins. Plugins can be registered in-process (init())
// or connected remotely via gRPC / HTTP — all through the same interface.
package plugin

import "context"

// Song is the normalized result from any tag source.
type Song struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Artist   string  `json:"artist"`
	ArtistID string  `json:"artist_id"`
	Album    string  `json:"album"`
	AlbumID  string  `json:"album_id"`
	AlbumImg string  `json:"album_img"`
	Year     string  `json:"year"`
	Source   string  `json:"source"`
	Genre    string  `json:"genre,omitempty"`
	Mid      string  `json:"mid,omitempty"`
	Cover    string  `json:"cover,omitempty"`
	Score    float64 `json:"score,omitempty"`
	// Duration is the track length in SECONDS, already normalized by the
	// plugin. Upstream units differ (NetEase reports milliseconds, the
	// rest seconds), so the conversion belongs at the edge rather than in
	// every consumer. Zero means unknown: MusicBrainz search results
	// carry no length, and that is a normal empty rather than a failure.
	Duration float64 `json:"duration,omitempty"`
}

// SearchResult wraps a search response with pagination info.
type SearchResult struct {
	Songs   []Song `json:"songs"`
	HasMore bool   `json:"has_more"`
}

// --- Tag-source plugin (music metadata) ---

// TagSource is the interface every music metadata plugin must implement.
// A plugin can be a local implementation or a gRPC-backed stub.
type TagSource interface {
	// Name returns the plugin identifier (e.g. "netease", "kugou").
	Name() string
	// DisplayName returns a human-readable name (e.g. "网易云音乐").
	DisplayName() string
	// SupportsSearch reports whether incremental (paginated) search is available.
	SupportsSearch() bool
	// SupportsLyric reports whether lyrics can be fetched.
	SupportsLyric() bool
	// SupportsId3 reports whether the source answer FetchID3ByTitle. AcoustID
	// answers true even though it doesn't answer Search — they are
	// orthogonal capabilities and consumers may consult either or both.
	SupportsId3() bool
	// SupportsAudioURL reports whether the source can hand out a playable
	// audio-stream URL for a known song id (e.g. netease / kuwo / kugou /
	// migu / qmusic). Musicbrainz / AcoustID return false here because
	// they only carry metadata — callers should pre-check and skip probing.
	SupportsAudioURL() bool

	// Search performs a paginated search for music tracks.
	Search(ctx context.Context, query string, page, limit int) (*SearchResult, error)
	// FetchID3ByTitle returns the best matches for a title (top-N).
	FetchID3ByTitle(ctx context.Context, title string) ([]Song, error)
	// FetchLyric returns the lyric text for a song ID.
	FetchLyric(ctx context.Context, songID string) (string, error)
	// GetAudioURL returns a short-lived upstream audio-stream URL for the
	// given song id. Plugins that don't implement SupportsAudioURL return
	// ("", nil) rather than an error so the caller can treat empty as
	// "not streamable" without distinguishing unimplemented from missing.
	// Empty URL paired with SupportsAudioURL() == true signals a transient
	// upstream failure (the gateway should fall back to /api/stream proxy).
	GetAudioURL(ctx context.Context, songID string) (string, error)
}

// --- Download-source plugin ---

// DownloadItem is one result from a download source search.
type DownloadItem struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Duration  float64 `json:"duration"`
	URL       string  `json:"url"`
	Channel   string  `json:"channel"`
	Thumbnail string  `json:"thumbnail"`
}

// DownloadResult wraps the result of a download operation.
type DownloadResult struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path,omitempty"`
	FileName string `json:"file_name,omitempty"`
	Error    string `json:"error,omitempty"`
}

// DownloadOptions carries the optional yt-dlp tuning knobs forwarded from
// the worker's download:generic task (DownloadPayload.ExtraJSON). Empty
// fields mean "plugin default" (bestaudio/best, keep original container,
// 192 kbps). The worker and the plugin each sanitize these independently
// (see internal/ytdlp) — the values may NOT be concatenated into a shell
// string anywhere without going through SanitizeYTDLP* first.
type DownloadOptions struct {
	Format       string
	OutputFormat string
	Quality      string
}

// DownloadSource handles searching and downloading audio from platforms
// like YouTube.
type DownloadSource interface {
	Name() string
	DisplayName() string

	Search(ctx context.Context, query string, maxResults int) ([]DownloadItem, error)
	Download(ctx context.Context, videoID, downloadDir string, opts DownloadOptions) (*DownloadResult, error)
}
