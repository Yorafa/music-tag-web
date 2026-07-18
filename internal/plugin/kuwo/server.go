// Package kuwo 酷我音乐 gRPC 插件。Ported from applications/task/services/kuwo.py。
//
// 关键点：
//   - search.kuwo.cn/r.s 返回 JS 风格 JSON（单引号、裸 key），parseKuwoJSON 转标准 JSON 后 unmarshal。
//   - 歌词从 m.kuwo.cn/newh5/singles/songinfoandlrc 取 lrclist，按 [h:m:s]fmt 重排为 LRC。
//
// CSRF / cookie ergonomics:
//
//	Kuwo's modern search endpoint gates anonymous calls behind a csrf cookie
//	that has to be primed from a homepage visit. Replicating the cookie
//	handshake via http.CookieJar (idempotent, per-server) is cleaner than
//	baking in a token-rotation game against an undocumented endpoint.
//	The first call per Server pays the warm-up cost; subsequent calls reuse
//	the seeded cookies via the shared jar.
package kuwo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

const (
	searchURL     = "http://search.kuwo.cn/r.s"
	homepageURL   = "https://www.kuwo.cn/"
	warmupTimeout = 5 * time.Second
)

var headers = map[string]string{
	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36",
}

type Server struct {
	pb.UnimplementedTagSourceServer
	client     *http.Client
	warmupOnce sync.Once
}

func NewServer() *Server {
	// cookiejar.New with nil Options accepts whatever cookies the warmup
	// GET sets — no SameSite / domain allow-lists needed, exactly what we
	// want for mimicking a real browser against kuwo.
	jar, err := cookiejar.New(nil)
	if err != nil {
		// Treat as fatal: a search-only client without a jar would regress
		// to the original failure mode (no csrf seeding). Log + return a
		// server with an empty jar-like fallback so /r.s still gets called
		// and the operator sees a useful error in `docker logs kuwo`.
		log.Printf("[kuwo] cookiejar.New: %v — search may fail csrf check", err)
		jar, _ = cookiejar.New(nil)
	}
	return &Server{
		client: &http.Client{
			Timeout: 10 * time.Second,
			Jar:     jar,
		},
	}
}

func (s *Server) GetPluginInfo(_ context.Context, _ *pb.PluginInfoRequest) (*pb.PluginInfoResponse, error) {
	return &pb.PluginInfoResponse{
		Name: "kuwo", DisplayName: "酷我音乐",
		SupportsSearch: true, SupportsLyric: true, SupportsId3: true,
		SupportsAudioUrl: true,
	}, nil
}

func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	songs, hasMore, err := s.doSearch(ctx, req.Query, int(req.Page), int(req.Limit))
	if err != nil {
		return nil, err
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
	// CSRF seed (no-op on subsequent calls thanks to sync.Once).
	s.warmupOnce.Do(func() {
		warmupCtx, cancel := context.WithTimeout(context.Background(), warmupTimeout)
		defer cancel()
		req, _ := http.NewRequestWithContext(warmupCtx, "GET", homepageURL, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if resp, err := s.client.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		} else {
			log.Printf("[kuwo] warmup: %v — first /r.s request may be csrf-rejected", err)
		}
	})
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
		// kuwo 上游偶发返回非 JSON 体（HTML 错误页、JS 模板渲染异常、响应中
		// 嵌入意外字符、字符截断…）。不要把整次搜索变成 gRPC fail —— gateway
		// 那一侧还在依次 fan-out 其余 plugin；记一行诊断日志、返 0 首歌、
		// nil error，让前端照常拿到搜索结果，其它 plugin 不受影响。
		log.Printf("[kuwo] doSearch 上游返回的响应无法解析为 JSON (响应长度=%d, 头 80 字节=%q): %v",
			len(cleaned), preview(cleaned, 80), err)
		return nil, false, nil
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

// preview 给原始字节返回前 n 字节的可读文本，用于诊断日志（避免 dump 整个
// 几 MB 响应）。截断处追加说明便于人眼识别。
func preview(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}

// parseKuwoJSON 把 JS 风格的 JSON 转换为标准 JSON：
//  1. 单引号 → 双引号
//  2. 无引号 key 加引号  ({key: → {"key":,  ,key: → ,"key":)
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

// GetAudioURL fetches a short-lived upstream audio-stream URL for the given
// kuwo song rid. Best-effort: paid tracks or anti-bot blocks result in empty
// url, which we propagate as ("", nil) so the gateway /api/stream proxy
// fallback (handler.StreamAudio) can take over.
//
// Endpoint choice: www.kuwo.cn/api/v1/www/music/playUrl?mid={rid}&type=music is
// the canonical 2024-era free-track endpoint. The older songinfoandlrc
// response sometimes carries data.playurl in payload side-effects but we
// don't depend on that here — playUrl is the explicit audio-fetch endpoint.
//
// The `Secret` header is documented upstream and is required for anonymous
// 2024-26 traffic; rotating keys require updating kuwoSecret.
const (
	kuwoAudioURL = "https://www.kuwo.cn/api/v1/www/music/playUrl"
	kuwoSecret   = "10373b58aee58943f95eaf17d38bc9cf50fbbef8e4bf4ec6401a3ae3ef8154560507f032"
)

// GetAudioURL handler (gRPC) on kuwo.TagSource.
func (s *Server) GetAudioURL(ctx context.Context, req *pb.GetAudioRequest) (*pb.GetAudioResponse, error) {
	if req.Id == "" {
		return &pb.GetAudioResponse{}, nil
	}
	urlStr := fmt.Sprintf("%s?mid=%s&type=music&httpsStatus=1", kuwoAudioURL, req.Id)
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	httpReq.Header.Set("User-Agent", headers["User-Agent"])
	httpReq.Header.Set("Referer", "https://www.kuwo.cn/")
	httpReq.Header.Set("Secret", kuwoSecret)
	resp, err := s.client.Do(httpReq)
	if err != nil {
		log.Printf("[kuwo] GetAudioURL http error: %v", err)
		return &pb.GetAudioResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		Data struct {
			Url string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		log.Printf("[kuwo] GetAudioURL unmarshal (head=%q): %v", preview(body, 80), err)
		return &pb.GetAudioResponse{}, nil
	}
	return &pb.GetAudioResponse{Url: data.Data.Url}, nil
}
