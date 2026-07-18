// Package kg implements the KuGou music tag source as a gRPC server.
// Ported from applications/task/services/kugou.py KugouClient.
package kg

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

const searchURL = "https://complexsearch.kugou.com/v2/search/song"

var keyCodeTemplate = "NVPh5oo715z5DIWAeQlhMDsWXXQV4hwt" +
	"bitrate=0clienttime={time}clientver=2000dfid=-" +
	"inputtype=0iscorrection=1isfuzzy=0keyword={keyword}" +
	"mid={time}page={page}pagesize={pagesize}" +
	"platform=WebFilterprivilege_filter=0srcappid=2919tag=em" +
	"userid=-1uuid={time}NVPh5oo715z5DIWAeQlhMDsWXXQV4hwt"

// Server implements the TagSource gRPC service for KuGou.
type Server struct {
	pb.UnimplementedTagSourceServer
	client *http.Client
}

// NewServer creates a new KuGou plugin server.
func NewServer() *Server {
	return &Server{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Server) GetPluginInfo(ctx context.Context, _ *pb.PluginInfoRequest) (*pb.PluginInfoResponse, error) {
	return &pb.PluginInfoResponse{
		Name:             "kugou",
		DisplayName:      "酷狗音乐",
		SupportsSearch:   true,
		SupportsLyric:    true,
		SupportsId3:      true,
		SupportsAudioUrl: true,
	}, nil
}

func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	songs, hasMore, err := s.doSearch(ctx, req.Query, int(req.Page), int(req.Limit))
	if err != nil {
		return nil, err
	}
	pbSongs := make([]*pb.Song, len(songs))
	for i, song := range songs {
		pbSongs[i] = mapToPBSong(song)
	}
	return &pb.SearchResponse{Songs: pbSongs, HasMore: hasMore}, nil
}

func (s *Server) FetchId3ByTitle(ctx context.Context, req *pb.FetchId3Request) (*pb.FetchId3Response, error) {
	songs, _, err := s.doSearch(ctx, req.Title, 1, 10)
	if err != nil {
		return &pb.FetchId3Response{}, nil
	}
	pbSongs := make([]*pb.Song, len(songs))
	for i, song := range songs {
		pbSongs[i] = mapToPBSong(song)
	}
	return &pb.FetchId3Response{Songs: pbSongs}, nil
}

func (s *Server) FetchLyric(ctx context.Context, req *pb.FetchLyricRequest) (*pb.FetchLyricResponse, error) {
	url := fmt.Sprintf("http://m.kugou.com/app/i/krc.php?cmd=100&timelength=999999&hash=%s", req.SongId)
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return &pb.FetchLyricResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return &pb.FetchLyricResponse{Lyric: string(body)}, nil
}

func (s *Server) doSearch(ctx context.Context, title string, page, pagesize int) ([]map[string]interface{}, bool, error) {
	millis := fmt.Sprintf("%d", time.Now().UnixMilli())

	p := strings.NewReplacer(
		"{time}", millis,
		"{keyword}", title,
		"{page}", fmt.Sprintf("%d", page),
		"{pagesize}", fmt.Sprintf("%d", pagesize),
	).Replace(keyCodeTemplate)
	signature := kugouSignature(p)

	reqURL := fmt.Sprintf(
		"%s?keyword=%s&page=%d&pagesize=%d&bitrate=0&isfuzzy=0&tag=em&inputtype=0&platform=WebFilter&userid=-1&clientver=2000&iscorrection=1&privilege_filter=0&srcappid=2919&clienttime=%s&mid=%s&uuid=%s&dfid=-&signature=%s",
		searchURL, title, page, pagesize, millis, millis, millis, signature,
	)

	httpReq, _ := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if derr := json.NewDecoder(resp.Body).Decode(&result); derr != nil {
		return nil, false, fmt.Errorf("kg json decode: %w", derr)
	}

	data, _ := result["data"].(map[string]interface{})
	lists, _ := data["lists"].([]interface{})

	songs := make([]map[string]interface{}, 0, len(lists))
	for _, l := range lists {
		if m, ok := l.(map[string]interface{}); ok {
			artists := strings.ReplaceAll(fmt.Sprintf("%v", m["SingerName"]), "<em>", "")
			artists = strings.ReplaceAll(artists, "</em>", "")
			m["artist"] = strings.ReplaceAll(artists, "、", ",")
			m["id"] = m["FileHash"]
			name := strings.ReplaceAll(fmt.Sprintf("%v", m["SongName"]), "<em>", "")
			m["name"] = strings.ReplaceAll(name, "</em>", "")
			m["artist_id"] = m["SingerId"]
			m["album"] = m["AlbumName"]
			m["album_id"] = m["AlbumID"]
			if img, ok := m["Image"].(string); ok {
				m["album_img"] = strings.ReplaceAll(img, "{size}", "150")
			}
			m["year"] = m["PublishTime"]
			songs = append(songs, m)
		}
	}

	return songs, len(songs) >= pagesize, nil
}

func kugouSignature(text string) string {
	h := md5.Sum([]byte(text))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

func mapToPBSong(m map[string]interface{}) *pb.Song {
	getStr := func(key string) string {
		if v, ok := m[key]; ok {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}
	return &pb.Song{
		Id:       getStr("id"),
		Name:     getStr("name"),
		Artist:   getStr("artist"),
		ArtistId: getStr("artist_id"),
		Album:    getStr("album"),
		AlbumId:  getStr("album_id"),
		AlbumImg: getStr("album_img"),
		Year:     getStr("year"),
	}
}

// GetAudioURL fetches a short-lived upstream audio-stream URL for the given
// kugou song hash (id == FileHash from doSearch). Best-effort: when the
// upstream returns empty url (anti-bot, paid track, mobile-only CDN), we
// propagate ("", nil) so the gateway /api/stream proxy fallback can take
// over.
//
// Endpoint choice: kugou.com/yy/index.php?r=play/getdata&hash={hash} returns
// `data.play_url` (direct mp3 URL) for free tracks. Some intermediate kugou
// responses require a `dfid` cookie primed from a homepage visit, but the
// anonymous path is functional for the common case.
const kgAudioURL = "https://www.kugou.com/yy/index.php?r=play/getdata"

func (s *Server) GetAudioURL(ctx context.Context, req *pb.GetAudioRequest) (*pb.GetAudioResponse, error) {
	if req.Id == "" {
		return &pb.GetAudioResponse{}, nil
	}
	urlStr := fmt.Sprintf("%s&hash=%s", kgAudioURL, req.Id)
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36")
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return &pb.GetAudioResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var raw struct {
		Data struct {
			PlayURL string `json:"play_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || raw.Data.PlayURL == "" {
		return &pb.GetAudioResponse{}, nil
	}
	return &pb.GetAudioResponse{Url: raw.Data.PlayURL}, nil
}
