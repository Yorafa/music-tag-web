// Package migu 咪咕音乐 gRPC 插件。Ported from applications/task/services/music_resource.py MiGuMusicClient。
//
// 搜索走 pd.musicapp.migu.cn 公开端点，POST params。
// 不支持原生分页：pageNo=N、pageSize=N 直接传即可（已验证有效）。
package migu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

const baseURL = "http://pd.musicapp.migu.cn/MIGUM2.0/v1.0/content"

var headers = map[string]string{
	"User-Agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 13_2_3 like Mac OS X) AppleWebKit/605.1.15",
	"Referer":    "http://music.migu.cn/",
}

type Server struct {
	pb.UnimplementedTagSourceServer
	client *http.Client
}

func NewServer() *Server {
	return &Server{client: &http.Client{Timeout: 10 * time.Second}}
}

func (s *Server) GetPluginInfo(_ context.Context, _ *pb.PluginInfoRequest) (*pb.PluginInfoResponse, error) {
	return &pb.PluginInfoResponse{
		Name: "migu", DisplayName: "咪咕音乐",
		SupportsSearch: true, SupportsLyric: true, SupportsId3: true,
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

// FetchLyric: song_id 是搜索返回 lyricUrl/trcUrl，直接 GET 拿到 LRC 文本。
func (s *Server) FetchLyric(ctx context.Context, req *pb.FetchLyricRequest) (*pb.FetchLyricResponse, error) {
	if req.SongId == "" {
		return &pb.FetchLyricResponse{}, nil
	}
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", req.SongId, nil)
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return &pb.FetchLyricResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return &pb.FetchLyricResponse{Lyric: string(body)}, nil
}

type song struct {
	ID       string
	Name     string
	Artist   string
	ArtistID string
	Album    string
	AlbumID  string
	AlbumImg string
	Year     string
}

func (s song) toPB() *pb.Song {
	return &pb.Song{
		Id: s.ID, Name: s.Name, Artist: s.Artist,
		Album: s.Album, AlbumId: s.AlbumID,
		AlbumImg: s.AlbumImg, Year: s.Year,
	}
}

func (sv *Server) doSearch(ctx context.Context, title string, page, limit int) ([]song, bool, error) {
	q := url.Values{
		"ua":          {"Android_migu"},
		"version":     {"5.0.1"},
		"text":        {title},
		"pageNo":      {strconv.Itoa(page)},
		"pageSize":    {strconv.Itoa(limit)},
		"searchSwitch": {`{"song":1,"album":0,"singer":0,"tagSong":0,"mvSong":0,"songlist":0,"bestShow":1}`},
	}
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/search_all.do", nil)
	q.Encode()
	httpReq.URL.RawQuery = q.Encode()
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := sv.client.Do(httpReq)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var raw struct {
		SongResultData struct {
			Result []map[string]interface{} `json:"result"`
		} `json:"songResultData"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("migu parse: %w", err)
	}
	out := make([]song, 0, len(raw.SongResultData.Result))
	for _, item := range raw.SongResultData.Result {
		sg := song{
			ID:    str(item["lyricUrl"]),
			Name:  str(item["name"]),
			Year:  "",
		}
		if singers, ok := item["singers"].([]interface{}); ok {
			var names []string
			for _, sd := range singers {
				if m, ok := sd.(map[string]interface{}); ok {
					names = append(names, str(m["name"]))
				}
			}
			sg.Artist = joinSlash(names)
		}
		if albums, ok := item["albums"].([]interface{}); ok && len(albums) > 0 {
			if m, ok := albums[0].(map[string]interface{}); ok {
				sg.Album = str(m["name"])
				sg.AlbumID = str(m["id"])
			}
		}
		if imgs, ok := item["imgItems"].([]interface{}); ok && len(imgs) > 0 {
			if m, ok := imgs[0].(map[string]interface{}); ok {
				sg.AlbumImg = str(m["img"])
			}
		}
		// 也尝试 trcUrl 作为 lyric 备用
		if sg.ID == "" {
			sg.ID = str(item["trcUrl"])
		}
		out = append(out, sg)
	}
	return out, len(out) >= limit, nil
}

func str(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func joinSlash(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "/"
		}
		out += p
	}
	return out
}
