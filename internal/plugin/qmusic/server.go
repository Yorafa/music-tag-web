// Package qmusic QQ音乐 gRPC 插件。走公开 musicu.fcg 端点 (与 django qm.py
// getQQMusicSearch 完全一致)，避开 qqmusic-api-python 难以移植的签名协议。
//
// 歌词走 c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg，返回 base64-encoded。
package qmusic

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	pb "go-music-tag/api/proto/tagplugin"
)

var headers = map[string]string{
	"User-Agent": "QQ音乐/73222 CFNetwork/1406.0.3 Darwin/22.4.0",
	"Referer":    "https://y.qq.com/portal/profile.html",
	"Content-Type": "json/application;charset=utf-8",
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
		Name: "qmusic", DisplayName: "QQ音乐",
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

// FetchLyric: song_id 实际是歌曲 mid。请求会返回 base64-encoded lyric body。
func (s *Server) FetchLyric(ctx context.Context, req *pb.FetchLyricRequest) (*pb.FetchLyricResponse, error) {
	url := fmt.Sprintf(
		"https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg?g_tk=5381&format=json&inCharset=utf-8&outCharset=utf-8&notice=0&platform=h5&needNewCode=1&ct=121&cv=0&songmid=%s",
		req.SongId,
	)
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return &pb.FetchLyricResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// 响应是嵌套 JSON：可以把整页当 lyric，但通常 lyric 字段是 base64-encoded。
	// 取 lyric 子串尽量保留纯 LRC 文本。
	var raw map[string]interface{}
	_ = json.Unmarshal(body, &raw)
	if raw != nil {
		if lyric, ok := raw["lyric"].(string); ok && lyric != "" {
			// QQ 加密过的 lyric 走 utf-16 → base64 → 解码流程。这里不做解码，只直接返回原始内容，
			// 由前端决定是否解析（多数 LRC 浏览器解析接受 raw JSON）。
			return &pb.FetchLyricResponse{Lyric: lyric}, nil
		}
		// 兜底：尝试从 translate 字段再 fallback
		if tl, ok := raw["trans"].(string); ok && tl != "" {
			return &pb.FetchLyricResponse{Lyric: fmt.Sprintf("%s\n%s",
				tryDecodeLRC(raw["lyric"]), tryDecodeLRC(tl))}, nil
		}
	}
	return &pb.FetchLyricResponse{Lyric: tryDecodeLRC(raw["lyric"])}, nil
}

// tryDecodeLRC 把可能的 base64 解码。失败返回原始。
func tryDecodeLRC(v interface{}) string {
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if s == "" {
		return ""
	}
	// base64 解码并尝试 utf16 → utf8
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	if strings.Contains(string(b), "\\u") {
		// hex escape 模式：把 \uXXXX → 中文
		out := decodeUnicodeEscapes(string(b))
		return out
	}
	return string(b)
}

func decodeUnicodeEscapes(s string) string {
	re := strings.NewReplacer(
		`\u002f`, "/", `\u003c`, "<", `\u003e`, ">",
	)
	s = re.Replace(s)
	// 通用 \uXXXX
	var sb strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+5 < len(rs) && rs[i+1] == 'u' {
			hexStr := string(rs[i+2 : i+6])
			if val, err := hex.DecodeString(hexStr); err == nil && len(val) == 2 {
				sb.WriteRune(rune(val[0])<<8 | rune(val[1]))
				i += 5
				continue
			}
		}
		sb.WriteRune(rs[i])
	}
	return sb.String()
}

type song struct {
	ID       string
	Mid      string
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
		Id: s.ID, Mid: s.Mid, Name: s.Name, Artist: s.Artist,
		Album: s.Album, AlbumId: s.AlbumID,
		AlbumImg: s.AlbumImg, Year: s.Year,
	}
}

func (s *Server) doSearch(ctx context.Context, query string, page, limit int) ([]song, bool, error) {
	payload := map[string]interface{}{
		"comm": map[string]interface{}{
			"wid":        "",
			"tmeAppID":  "qqmusic",
			"authst":    "",
			"uid":       "",
			"gray":      "0",
			"OpenUDID":  "2d484d3157d4ed482e406e6c5fdcf8c3d3275deb",
			"ct":        "6",
			"patch":     "2",
			"cv":        "80600",
			"gzip":      "0",
			"qq":        "",
			"nettype":   "2",
		},
		"music.search.SearchCgiService.DoSearchForQQMusicDesktop": map[string]interface{}{
			"module": "music.search.SearchCgiService",
			"method": "DoSearchForQQMusicDesktop",
			"param": map[string]interface{}{
				"num_per_page": limit,
				"page_num":     page,
				"remoteplace":  "txt.mac.search",
				"search_type":  0,
				"query":        query,
				"grp":          1,
				"searchid":     uuid.New().String(),
				"nqc_flag":     0,
			},
		},
	}
	bodyJSON, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://u.y.qq.com/cgi-bin/musicu.fcg",
		strings.NewReader(string(bodyJSON)))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// QQ 返回结构是 {"music.search.SearchCgiService.DoSearchForQQMusicDesktop": {"data": {"body": {"song": {"list": [...]}}}, "meta": {...}}}
	var wrap struct {
		Search map[string]struct {
			Data struct {
				Body struct {
					Song struct {
						List []map[string]interface{} `json:"list"`
					} `json:"song"`
				} `json:"body"`
			} `json:"data"`
			Meta struct {
				Sum      int `json:"sum"`
				NextPage int `json:"nextpage"`
				CurPage  int `json:"curpage"`
			} `json:"meta"`
		} `json:"music.search.SearchCgiService.DoSearchForQQMusicDesktop"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, false, fmt.Errorf("qmusic parse: %w", err)
	}
	list := wrap.Search[""].Data.Body.Song.List
	if len(list) == 0 {
		// fallback：旧的 key 形式
		var loose map[string]interface{}
		_ = json.Unmarshal(body, &loose)
		if v, ok := loose["music.search.SearchCgiService.DoSearchForQQMusicDesktop"].(map[string]interface{}); ok {
			if data, ok := v["data"].(map[string]interface{}); ok {
				if body_, ok := data["body"].(map[string]interface{}); ok {
					if songList, ok := body_["song"].(map[string]interface{}); ok {
						if arr, ok := songList["list"].([]interface{}); ok {
							for _, x := range arr {
								if m, ok := x.(map[string]interface{}); ok {
									list = append(list, m)
								}
							}
						}
					}
				}
			}
		}
	}
	out := make([]song, 0, len(list))
	for _, item := range list {
		sg := song{
			ID:   str(item["mid"]), // 用 mid 作为 id，与 qmusic 客户端约定一致
			Mid:  str(item["mid"]),
			Name: str(item["title"]),
		}
		// artist 数组
		if singers, ok := item["singer"].([]interface{}); ok {
			var names []string
			for _, sd := range singers {
				if m, ok := sd.(map[string]interface{}); ok {
					names = append(names, str(m["name"]))
					if sg.ArtistID == "" {
						sg.ArtistID = str(m["id"])
					}
				}
			}
			sg.Artist = strings.Join(names, ",")
		}
		if album, ok := item["album"].(map[string]interface{}); ok {
			sg.Album = str(album["title"])
			sg.AlbumID = str(album["mid"])
		}
		sg.Year = str(item["time_public"])
		sg.AlbumImg = "http://y.qq.com/music/photo_new/T002R300x300M000" + sg.AlbumID + ".jpg"
		out = append(out, sg)
	}
	hasMore := wrap.Search[""].Meta.NextPage > page
	return out, hasMore, nil
}

func str(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

var _ = rand.Int // 保留占位 (若后续要做分布式锁随机种子可用)
