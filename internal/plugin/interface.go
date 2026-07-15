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

	// Search performs a paginated search for music tracks.
	Search(ctx context.Context, query string, page, limit int) (*SearchResult, error)
	// FetchID3ByTitle returns the best matches for a title (top-N).
	FetchID3ByTitle(ctx context.Context, title string) ([]Song, error)
	// FetchLyric returns the lyric text for a song ID.
	FetchLyric(ctx context.Context, songID string) (string, error)
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

// DownloadSource handles searching and downloading audio from platforms
// like YouTube.
type DownloadSource interface {
	Name() string
	DisplayName() string

	Search(ctx context.Context, query string, maxResults int) ([]DownloadItem, error)
	Download(ctx context.Context, videoID, downloadDir string) (*DownloadResult, error)
}
