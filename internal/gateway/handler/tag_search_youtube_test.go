package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/plugin"
)

// youtubeSearchStub returns one DownloadItem so SearchMusic can map it
// into plugin.Song for the unified /api/search_music/ response.
type youtubeSearchStub struct {
	name string
}

func (y *youtubeSearchStub) Name() string        { return y.name }
func (y *youtubeSearchStub) DisplayName() string { return "YouTube" }
func (y *youtubeSearchStub) Search(_ context.Context, _ string, _ int) ([]plugin.DownloadItem, error) {
	return []plugin.DownloadItem{{
		ID:        "dQw4w9WgXcQ",
		Title:     "Never Gonna Give You Up",
		Channel:   "Rick Astley",
		Thumbnail: "https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg",
		Duration:  213,
		URL:       "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
	}}, nil
}
func (y *youtubeSearchStub) Download(_ context.Context, _, _ string, _ plugin.DownloadOptions) (*plugin.DownloadResult, error) {
	return nil, nil
}

// TestSearchMusic_YouTubeMapsVideoID pins the contract that made
// PlayButton hit GET /api/stream/?src=youtube&id= → 400 "missing src or id".
// DownloadItem.ID must land on Song.ID; without it the frontend builds
// an empty id query and StreamAudio rejects the request.
func TestSearchMusic_YouTubeMapsVideoID(t *testing.T) {
	plugin.ResetForTesting()
	t.Cleanup(plugin.ResetForTesting)

	plugin.InstallMockDownloadSource("youtube", &youtubeSearchStub{name: "youtube"})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/search_music/", SearchMusic)

	body := []byte(`{"query":"rick","sources":["youtube"],"pages":{},"limit":10}`)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/search_music/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var envelope struct {
		Result bool `json:"result"`
		Data   struct {
			Songs []plugin.Song `json:"songs"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if !envelope.Result {
		t.Fatalf("result=false body=%s", w.Body.String())
	}
	if len(envelope.Data.Songs) != 1 {
		t.Fatalf("len(songs)=%d want 1", len(envelope.Data.Songs))
	}
	song := envelope.Data.Songs[0]
	if song.ID != "dQw4w9WgXcQ" {
		t.Errorf("song.ID=%q want dQw4w9WgXcQ (PlayButton needs this for /api/stream?id=)", song.ID)
	}
	if song.Source != "youtube" {
		t.Errorf("song.Source=%q want youtube", song.Source)
	}
	if song.Name != "Never Gonna Give You Up" {
		t.Errorf("song.Name=%q", song.Name)
	}
	if song.Artist != "Rick Astley" {
		t.Errorf("song.Artist=%q", song.Artist)
	}
}
