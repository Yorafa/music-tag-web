package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/plugin"
)

// ─── fake plugin used by ListSources test ─────────────────────────────────

type fakeListSource struct {
	name        string
	displayName string
	search      bool
	lyric       bool
	id3         bool
	audioURL    bool
}

func (f *fakeListSource) Name() string           { return f.name }
func (f *fakeListSource) DisplayName() string    { return f.displayName }
func (f *fakeListSource) SupportsSearch() bool   { return f.search }
func (f *fakeListSource) SupportsLyric() bool    { return f.lyric }
func (f *fakeListSource) SupportsId3() bool      { return f.id3 }
func (f *fakeListSource) SupportsAudioURL() bool { return f.audioURL }
func (f *fakeListSource) Search(_ context.Context, _ string, _, _ int) (*plugin.SearchResult, error) {
	return nil, nil
}
func (f *fakeListSource) FetchID3ByTitle(_ context.Context, _ string) ([]plugin.Song, error) {
	return nil, nil
}
func (f *fakeListSource) FetchLyric(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *fakeListSource) GetAudioURL(_ context.Context, _ string) (string, error) {
	return "", nil
}

func TestListSources_ReturnsRegisteredPluginsWithCapabilities(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	plugin.InstallMockTagSource("foo", &fakeListSource{
		name: "foo", displayName: "FooSource",
		search: true, lyric: true, id3: true, audioURL: true,
	})
	plugin.InstallMockTagSource("bar", &fakeListSource{
		name: "bar", displayName: "BarSource",
		search: false, lyric: false, id3: false, audioURL: false,
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/sources/", ListSources)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/sources/", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var envelope struct {
		Result  bool         `json:"result"`
		Code    string       `json:"code"`
		Data    []SourceInfo `json:"data"`
		Message string       `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v: body=%s", err, w.Body.String())
	}
	if !envelope.Result {
		t.Fatalf("envelope.result=false: %s", w.Body.String())
	}
	if len(envelope.Data) != 2 {
		t.Fatalf("len(data)=%d (want 2: 2 tag mocks, no synthesised download fallback since source.go no longer hardcodes youtube)", len(envelope.Data))
	}
	// Sorted alphabetically by name (per handler contract). With no
	// download-source mock registered, only the 2 tag-source entries appear
	// — no synthesised youtube fallback is emitted anymore (dedup'd by the
	// plugin registry; a DownloadSource is only surfaced when it has
	// actually registered via gRPC / init()).
	if envelope.Data[0].Name != "bar" || envelope.Data[1].Name != "foo" {
		t.Errorf("order=%v (want bar, foo)", []string{envelope.Data[0].Name, envelope.Data[1].Name})
	}
	foo := envelope.Data[1]
	if foo.DisplayName != "FooSource" {
		t.Errorf("display_name=%q", foo.DisplayName)
	}
	if foo.Kind != "tag" {
		t.Errorf("kind=%q (want tag)", foo.Kind)
	}
	if !foo.Searchable || !foo.Lyric || !foo.SupportsId3 || !foo.SupportsAudioUrl {
		t.Errorf("foo capabilities not all true: %+v", foo)
	}
	bar := envelope.Data[0]
	// bar is the "all capabilities disabled" fixture (search/lyric/id3/audioURL
	// all false) so SourceInfo surfaces a metadata-only tag source. The only
	// assertion that matters for downstream PlayButton/media-URL fallback is
	// SupportsAudioUrl — keep that explicit so a future refactor flipping
	// the other fields doesn't mask this regression.
	if bar.SupportsAudioUrl {
		t.Errorf("bar.SupportsAudioUrl=true (want false for audio-only-disabled source)")
	}
}

// ─── download-source fixtures + fallback tests ───────────────────────────────

type fakeListDownload struct {
	name        string
	displayName string
}

func (f *fakeListDownload) Name() string        { return f.name }
func (f *fakeListDownload) DisplayName() string { return f.displayName }
func (f *fakeListDownload) Search(_ context.Context, _ string, _ int) ([]plugin.DownloadItem, error) {
	return nil, nil
}
func (f *fakeListDownload) Download(_ context.Context, _, _ string, _ plugin.DownloadOptions) (*plugin.DownloadResult, error) {
	return nil, nil
}

// TestListSources_DownloadSourceRegisteredAsKindDownload pins that a
// registered gRPC download-source plugin is emitted with Kind=download
// and SupportsAudioUrl=true when its name is "youtube" (the only
// DownloadSource with the gateway-side playback contract).
func TestListSources_DownloadSourceRegisteredAsKindDownload(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	plugin.InstallMockDownloadSource("youtube", &fakeListDownload{
		name:        "youtube",
		displayName: "YouTube",
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/sources/", ListSources)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/sources/", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var envelope struct {
		Result bool         `json:"result"`
		Data   []SourceInfo `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if !envelope.Result {
		t.Fatalf("envelope.result=false body=%s", w.Body.String())
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("len(data)=%d (want 1) body=%s", len(envelope.Data), w.Body.String())
	}
	got := envelope.Data[0]
	if got.Name != "youtube" || got.DisplayName != "YouTube" || got.Kind != "download" {
		t.Errorf("name/display_name/kind mismatch: %+v", got)
	}
	if !got.SupportsAudioUrl {
		t.Errorf("SupportsAudioUrl=false (want true — registry-provided SourceInfo carries this flag; no hardcoded youtube contract anymore)")
	}
	if got.Lyric || got.SupportsId3 {
		t.Errorf("download-source invariants violated (lyric / supports_id3 must be false): %+v", got)
	}
}

// TestListSources_NoDownloadSourceWhenUnregistered pins the new contract:
// when no plugin (tag or download) is in the registry, /api/sources/
// returns an empty data array. source.go no longer synthesises a
// youtube fallback — a DownloadSource is only surfaced when it has
// actually registered via gRPC / init(). The frontend's download-source
// picker relies on this so unregistered sources stay hidden rather than
// appearing prematurely.
func TestListSources_NoDownloadSourceWhenUnregistered(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/sources/", ListSources)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/sources/", nil)
	r.ServeHTTP(w, req)

	var envelope struct {
		Data []SourceInfo `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if len(envelope.Data) != 0 {
		t.Fatalf("len(data)=%d (want 0 — no synthesised fallback anymore) body=%s", len(envelope.Data), w.Body.String())
	}
}

// TestListSources_NoDuplicateYouTubeWhenRegistered guards against the
// fallback double-emitting YouTube on top of a registry-provided
// download-source entry.
func TestListSources_NoDuplicateYouTubeWhenRegistered(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	plugin.InstallMockDownloadSource("youtube", &fakeListDownload{
		name:        "youtube",
		displayName: "YouTube",
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/sources/", ListSources)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/sources/", nil)
	r.ServeHTTP(w, req)

	var envelope struct {
		Data []SourceInfo `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	count := 0
	for _, e := range envelope.Data {
		if e.Name == "youtube" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("youtubeCount=%d (want exactly 1; defensive fallback must not duplicate)", count)
	}
}
