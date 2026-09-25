package youtube

import (
	"encoding/json"
	"testing"
)

// TestPickThumbnail pins the search-entry thumbnail reader.
//
// Search runs `yt-dlp --flat-playlist --dump-json`, and that mode does not
// emit a `thumbnail` string at all — it emits a `thumbnails` array of
// {url, width, height}. Reading the string field is why every YouTube
// row came back with an empty cover: the code looked for a key the
// command never produces, so it silently got "" and no error.
func TestPickThumbnail(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "picks the largest entry from the thumbnails array",
			raw:  `{"id":"x","thumbnails":[{"url":"http://a/360.jpg","width":360,"height":202},{"url":"http://a/720.jpg","width":720,"height":404}]}`,
			want: "http://a/720.jpg",
		},
		{
			name: "falls back to the legacy thumbnail string",
			// Older yt-dlp builds, and --dump-json without --flat-playlist,
			// do emit this. Keep honouring it.
			raw:  `{"id":"x","thumbnail":"http://a/legacy.jpg"}`,
			want: "http://a/legacy.jpg",
		},
		{
			name: "prefers the array over the string when both are present",
			raw:  `{"id":"x","thumbnail":"http://a/stale.jpg","thumbnails":[{"url":"http://a/fresh.jpg","width":720}]}`,
			want: "http://a/fresh.jpg",
		},
		{
			name: "an entry with no width still yields its url",
			raw:  `{"id":"x","thumbnails":[{"url":"http://a/only.jpg"}]}`,
			want: "http://a/only.jpg",
		},
		{
			name: "skips entries with an empty url",
			raw:  `{"id":"x","thumbnails":[{"url":"","width":720},{"url":"http://a/real.jpg","width":360}]}`,
			want: "http://a/real.jpg",
		},
		{
			name: "an empty array falls through to nothing",
			raw:  `{"id":"x","thumbnails":[]}`,
			want: "",
		},
		{
			name: "no thumbnail information at all",
			raw:  `{"id":"x","title":"t"}`,
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var entry ytSearchEntry
			if err := json.Unmarshal([]byte(c.raw), &entry); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := entry.thumbnailURL(); got != c.want {
				t.Errorf("thumbnailURL() = %q, want %q", got, c.want)
			}
		})
	}
}
