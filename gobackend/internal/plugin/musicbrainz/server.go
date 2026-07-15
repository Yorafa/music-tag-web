// Package musicbrainz MusicBrainz gRPC 插件。
//
// 严格遵循 MusicBrainz API 速率：1 req/s（多 goroutine 共享一个全局限速器）。
// 不提供歌词 (SupportsLyric=false)。
package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

const API = "https://musicbrainz.org/ws/2"

var headers = map[string]string{
	"User-Agent": "MusicTagWeb/2.0 ( https://github.com/charlesxie/music-tag-web )",
	"Accept":     "application/json",
}

// 全局限速器：所有 goroutine 共享 1 req/s 节流。
var (
	lastCallMu sync.Mutex
	lastCall   time.Time
)

// throttle 跨 goroutine 串行化调用间隔。
// 注意锁不会 defer Unlock：手动控制，避免 ctx 提前返回时被双重释放。
func throttle(ctx context.Context) error {
	lastCallMu.Lock()
	now := time.Now()
	since := now.Sub(lastCall)
	if since >= 1100*time.Millisecond {
		lastCall = now
		lastCallMu.Unlock()
		return nil
	}
	wait := 1100*time.Millisecond - since
	lastCallMu.Unlock()

	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return ctx.Err()
	}

	lastCallMu.Lock()
	lastCall = time.Now()
	lastCallMu.Unlock()
	return nil
}

type Server struct {
	pb.UnimplementedTagSourceServer
	client *http.Client
}

func NewServer() *Server {
	return &Server{client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *Server) GetPluginInfo(_ context.Context, _ *pb.PluginInfoRequest) (*pb.PluginInfoResponse, error) {
	return &pb.PluginInfoResponse{
		Name: "musicbrainz", DisplayName: "MusicBrainz",
		SupportsSearch: true, SupportsLyric: false, SupportsId3: true,
	}, nil
}

func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	songs, hasMore, err := s.doSearch(ctx, req.Query, int(req.Page), int(req.Limit))
	if err != nil {
		return &pb.SearchResponse{}, nil
	}
	out := make([]*pb.Song, len(songs))
	for i := range songs {
		out[i] = songs[i].toPB()
	}
	return &pb.SearchResponse{Songs: out, HasMore: hasMore}, nil
}

func (s *Server) FetchId3ByTitle(ctx context.Context, req *pb.FetchId3Request) (*pb.FetchId3Response, error) {
	songs, _, err := s.doSearch(ctx, req.Title, 1, 10)
	if err != nil {
		return &pb.FetchId3Response{}, nil
	}
	out := make([]*pb.Song, len(songs))
	for i := range songs {
		out[i] = songs[i].toPB()
	}
	return &pb.FetchId3Response{Songs: out}, nil
}

// FetchLyric: MusicBrainz 不提供歌词，按约定返回 error，handler 层会归一化成空串。
func (s *Server) FetchLyric(_ context.Context, _ *pb.FetchLyricRequest) (*pb.FetchLyricResponse, error) {
	return &pb.FetchLyricResponse{Lyric: ""}, nil
}

type song struct {
	ID       string
	Name     string
	Artist   string
	ArtistID string
	Album    string
	Genre    string
}

func (s song) toPB() *pb.Song {
	return &pb.Song{
		Id: s.ID, Name: s.Name, Artist: s.Artist,
		ArtistId: s.ArtistID, Album: s.Album, Genre: s.Genre,
	}
}

func (sv *Server) doSearch(ctx context.Context, title string, page, limit int) ([]song, bool, error) {
	if err := throttle(ctx); err != nil {
		return nil, false, err
	}
	offset := (page - 1) * limit

	urlStr := fmt.Sprintf("%s/recording/?query=%s&fmt=json&limit=%d&offset=%d",
		API,
		url.QueryEscape(`recording:"`+title+`"`),
		limit, offset,
	)
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := sv.client.Do(httpReq)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 503 {
		// 速率触发，sleep 后重试一次
		time.Sleep(2 * time.Second)
		_ = throttle(ctx)
		resp2, err2 := sv.client.Do(httpReq)
		if err2 != nil {
			return nil, false, err2
		}
		defer resp2.Body.Close()
		resp = resp2
	}
	body, _ := io.ReadAll(resp.Body)

	var raw struct {
		Recordings []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			ArtistCredit []struct {
				Name       string `json:"name"`
				JoinPhrase string `json:"joinphrase"`
				Artist     struct {
					ID string `json:"id"`
				} `json:"artist"`
			} `json:"artist-credit"`
			Releases []struct {
				Title string `json:"title"`
			} `json:"releases"`
			Tags []struct {
				Name string `json:"name"`
			} `json:"tags"`
		} `json:"recordings"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("musicbrainz parse: %w", err)
	}
	out := make([]song, 0, len(raw.Recordings))
	for _, rec := range raw.Recordings {
		sg := song{
			ID:   rec.ID,
			Name: rec.Title,
		}
		// artirst-credit 用 joinphrase 串起来
		var artist string
		var artistID string
		for _, ac := range rec.ArtistCredit {
			artist += ac.Name + ac.JoinPhrase
			if artistID == "" {
				artistID = ac.Artist.ID
			}
		}
		sg.Artist = artist
		sg.ArtistID = artistID
		if len(rec.Releases) > 0 {
			sg.Album = rec.Releases[0].Title
		}
		if len(rec.Tags) > 0 {
			sg.Genre = rec.Tags[0].Name
		}
		out = append(out, sg)
	}
	hasMore := raw.Count > offset+len(out)
	return out, hasMore, nil
}

var _ = strconv.Atoi // 保留占位