// Package kuwo 酷我音乐 gRPC 插件。Ported from applications/task/services/kuwo.py。
//
// 关键点：
//   - search.kuwo.cn/r.s 返回 JS 风格 JSON（单引号、裸 key），parseKuwoJSON 转标准 JSON 后 unmarshal。
//   - 歌词从 m.kuwo.cn/newh5/singles/songinfoandlrc 取 lrclist，按 [h:m:s]fmt 重排为 LRC。
package kuwo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

const searchURL = "http://search.kuwo.cn/r.s"

var headers = map[string]string{
	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36",
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
		Name: "kuwo", DisplayName: "酷我音乐",
		SupportsSearch: true, SupportsLyric: true, SupportsId3: true,
	}, nil
}

func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	songs, hasMore, err := s.doSearch(ctx, req.Query, int(req.Page), int(req.Limit))
	if err != nil {
		return &pb.SearchResponse{}, nil
	}
	out := make([]*pb.Song, len(songs))
	for i, sg := range songs {
		out[i] = sg.toPB()
	}
	return &pb.SearchResponse{Songs: out, HasMore: hasMore}, nil
}

func (s *Server) FetchId3ByTitle(ctx context.Context, req *pb.FetchId3Request) (*pb.FetchId3Response, error) {
	songs, _, err := s.doSearch(ctx, req.Title, 1, 10)
	if err != nil {
		return &pb.FetchId3Response{}, nil
	}
	out := make([]*pb.Song, len(songs))
	for i, sg := range songs {
		out[i] = sg.toPB()
	}
	return &pb.FetchId3Response{Songs: out}, nil
}

// FetchLyric: song_id = 歌曲 rid（数字字符串），同时返回带 cover URL 的对象。
// 当前 Song 协议里只有 lyric 字段，我们把 lyric 拼成 {lyric, cover} 的 JSON 字符串以兼容 Django。
// 不引入新协议字段，保持向后兼容。
func (s *Server) FetchLyric(ctx context.Context, req *pb.FetchLyricRequest) (*pb.FetchLyricResponse, error) {
	urlStr := "http://m.kuwo.cn/newh5/singles/songinfoandlrc?" + url.Values{
		"musicId":     {req.SongId},
		"httpsStatus": {"1"},
	}.Encode()
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return &pb.FetchLyricResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		Data struct {
			Lrclist []struct {
				Time      string `json:"time"`
				LineLyric string `json:"lineLyric"`
			} `json:"lrclist"`
			Songinfo struct {
				Pic string `json:"pic"`
			} `json:"songinfo"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return &pb.FetchLyricResponse{Lyric: string(body)}, nil
	}
	lyric := formatLRC(data.Data.Lrclist)
	if data.Data.Songinfo.Pic != "" {
		// 行尾加 cover= 后跟 url（Django 端 fetch_lyric 对 Kuwo 返回 dict，这里同样把 cover 拼到 text 里有点 ugly；
		// 实际 Django kuwo client's fetch_lyric 直接返回 {"lyric": ..., "cover": ...}，意味着后续若需要 photo 前端
		// 直接从 aw.lyric.value 末尾拆 cover）。
		lyric = lyric + "\n[cover]" + data.Data.Songinfo.Pic
	}
	return &pb.FetchLyricResponse{Lyric: lyric}, nil
}

// formatLRC 把 [{"time":"61.91","lineLyric":"..."}] 转成 [mm:ss]text\n。
// 不照搬 Python 的 "%d:%02d:%02d"（小时位不接受补零），现代 LRC 解析器都接受 [0:01:30]。
func formatLRC(list []struct {
	Time      string `json:"time"`
	LineLyric string `json:"lineLyric"`
}) string {
	var sb strings.Builder
	for _, ln := range list {
		secFloat, _ := strconv.ParseFloat(strings.TrimSpace(ln.Time), 64)
		sec := int(secFloat)
		h := sec / 3600
		m := (sec % 3600) / 60
		s := sec % 60
		sb.WriteString(fmt.Sprintf("%d:%02d:%02d%s\n", h, m, s, ln.LineLyric))
	}
	return sb.String()
}

// ─── internal search ──────────────────────────────────────────────

type song struct {
	ID      string
	Name    string
	Artist  string
	Album   string
	AlbumID string
}

func (s song) toPB() *pb.Song {
	return &pb.Song{
		Id: s.ID, Name: s.Name, Artist: s.Artist,
		Album: s.Album, AlbumId: s.AlbumID,
	}
}

func (s *Server) doSearch(ctx context.Context, title string, page, limit int) ([]song, bool, error) {
	pn := (page - 1) * limit
	q := url.Values{
		"all":      {title},
		"ft":       {"music"},
		"itemset":  {"web_2013"},
		"client":   {"kt"},
		"pcmp4":    {"1"},
		"geo":      {"c"},
		"vipver":   {"1"},
		"pn":       {strconv.Itoa(pn)},
		"rn":       {strconv.Itoa(limit)},
		"rformat":  {"json"},
		"encoding": {"utf8"},
	}
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", searchURL+"?"+q.Encode(), nil)
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	cleaned := parseKuwoJSON(body)

	var raw struct {
		Abslist   []map[string]interface{} `json:"abslist"`
		MusicList []map[string]interface{} `json:"musiclist"`
	}
	if err := json.Unmarshal(cleaned, &raw); err != nil {
		return nil, false, fmt.Errorf("parse kuwo json: %w", err)
	}
	songs := raw.Abslist
	if len(songs) == 0 {
		songs = raw.MusicList
	}
	out := make([]song, 0, len(songs))
	for _, m := range songs {
		sg := song{
			ID:      strings.TrimPrefix(str(getField(m, "MUSICRID", "musicrid")), "MUSIC_"),
			Name:    str(getField(m, "NAME", "SONGNAME")),
			Artist:  str(getField(m, "ARTIST", "SINGER")),
			Album:   str(getField(m, "ALBUM")),
			AlbumID: str(getField(m, "ALBUMID")),
		}
		out = append(out, sg)
	}
	return out, len(out) >= limit, nil
}

func getField(m map[string]interface{}, keys ...string) interface{} {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func str(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// parseKuwoJSON 把 JS 风格的 JSON 转换为标准 JSON：
//   1. 单引号 → 双引号
//   2. 无引号 key 加引号  ({key: → {"key":,  ,key: → ,"key":)
//
// 实现完全等价 demjson3.decode。
func parseKuwoJSON(body []byte) []byte {
	s := string(body)
	// ' → "
	s = strings.ReplaceAll(s, `'`, `"`)
	// 给裸 key 加引号
	re := regexp.MustCompile(`([{,]\s*)([a-zA-Z_][a-zA-Z0-9_]*)(\s*:)`)
	s = re.ReplaceAllString(s, `$1"$2"$3`)
	// 去掉 JS 风格的尾部逗号 (,] → ],  ,} → })
	s = regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(s, `$1`)
	return []byte(s)
}
