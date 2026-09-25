// Package kuwo 酷我音乐 gRPC 插件。Ported from applications/task/services/kuwo.py and
// aligned with the reference go-music-dl / music-lib kuwo package (2026-08):
//
//   - Search uses the www.kuwo.cn searchMusicBykeyWord endpoint, which returns
//     clean, properly-escaped JSON (abslist[]). The previous search.kuwo.cn/r.s
//     endpoint served JS-style JSON whose MINFO/N_MINFO values embed raw
//     double quotes inside single-quoted strings — the naive '→" conversion
//     corrupted the payload (invalid character 'b' after object key:value
//     pair) and blanked the whole source (verified 2026-08).
//   - 歌词从 m.kuwo.cn/newh5/singles/songinfoandlrc 取 lrclist，按 [h:m:s]fmt 重排为 LRC。
package kuwo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

// searchURL is the kuwo web search endpoint (reference go-music-dl). It needs
// no csrf cookie handshake and returns standard JSON — unlike the retired
// search.kuwo.cn/r.s JS-style endpoint.
const searchURL = "http://www.kuwo.cn/search/searchMusicBykeyWord"

var headers = map[string]string{
	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36",
}

type Server struct {
	pb.UnimplementedTagSourceServer
	client *http.Client
}

func NewServer() *Server {
	return &Server{
		client: &http.Client{Timeout: 10 * time.Second},
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
	ID       string
	Name     string
	Artist   string
	Album    string
	AlbumID  string
	AlbumImg string
}

func (s song) toPB() *pb.Song {
	return &pb.Song{
		Id: s.ID, Name: s.Name, Artist: s.Artist,
		Album: s.Album, AlbumId: s.AlbumID,
		AlbumImg: s.AlbumImg,
	}
}

// kuwoSearchItem mirrors one abslist / musiclist row of the clean-JSON
// searchMusicBykeyWord response (field names verified 2026-08).
type kuwoSearchItem struct {
	MusicRID string `json:"MUSICRID"`
	SongName string `json:"SONGNAME"`
	Artist   string `json:"ARTIST"`
	Singer   string `json:"SINGER"` // musiclist cohort uses SINGER (legacy fallback)
	Album    string `json:"ALBUM"`
	AlbumID  string `json:"ALBUMID"`
	HtsMVPIC string `json:"hts_MVPIC"`
}

func (s *Server) doSearch(ctx context.Context, title string, page, limit int) ([]song, bool, error) {
	pn := (page - 1) * limit
	q := url.Values{
		"all":                {title},
		"vipver":             {"1"},
		"client":             {"kt"},
		"ft":                 {"music"},
		"cluster":            {"0"},
		"strategy":           {"2012"},
		"encoding":           {"utf8"},
		"rformat":            {"json"},
		"mobi":               {"1"},
		"issubtitle":         {"1"},
		"show_copyright_off": {"1"},
		"pn":                 {strconv.Itoa(pn)},
		"rn":                 {strconv.Itoa(limit)},
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

	var raw struct {
		Abslist   []kuwoSearchItem `json:"abslist"`
		MusicList []kuwoSearchItem `json:"musiclist"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		// kuwo 上游偶发返回非 JSON 体（HTML 错误页、JS 模板渲染异常、响应中
		// 嵌入意外字符、字符截断…）。不要把整次搜索变成 gRPC fail —— gateway
		// 那一侧还在依次 fan-out 其余 plugin；记一行诊断日志、返 0 首歌、
		// nil error，让前端照常拿到搜索结果，其它 plugin 不受影响。
		log.Printf("[kuwo] doSearch 上游返回的响应无法解析为 JSON (响应长度=%d, 头 80 字节=%q): %v",
			len(body), preview(body, 80), err)
		return nil, false, nil
	}
	items := raw.Abslist
	if len(items) == 0 {
		items = raw.MusicList
	}
	out := make([]song, 0, len(items))
	for _, it := range items {
		artist := it.Artist
		if artist == "" {
			artist = it.Singer // musiclist cohort uses SINGER
		}
		out = append(out, song{
			ID:       strings.TrimPrefix(it.MusicRID, "MUSIC_"),
			Name:     it.SongName,
			Artist:   artist,
			Album:    it.Album,
			AlbumID:  it.AlbumID,
			AlbumImg: it.HtsMVPIC,
		})
	}
	return out, len(out) >= limit, nil
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

// GetAudioURL fetches a short-lived upstream audio-stream URL for the given
// kuwo song rid. Best-effort: paid tracks or anti-bot blocks result in empty
// url, which we propagate as ("", nil) so the gateway /api/stream proxy
// fallback (handler.StreamAudio) can take over.
//
// kuwoAudioURL is the antiserver endpoint used to resolve a playable audio
// URL from a song rid. The previous www.kuwo.cn/api/v1/www/music/playUrl
// endpoint now returns `{"success":false,"message":"The request is
// illegal!"}` for anonymous traffic — the `Secret` header scheme was retired
// upstream (verified 2026-07). antiserver.kuwo.cn/anti.s?type=convert_url3
// still serves free 128k previews without any secret:
//
//	https://antiserver.kuwo.cn/anti.s?type=convert_url3&rid=MUSIC_<rid>&format=mp3&response=url
//	→ {"code":200,"msg":"success","url":"https://nf-sycdn.kuwo.cn/...mp3"}
//
// kuwoSecret is kept as a deprecated no-op for the YAML override surface
// (plan C.4); the antiserver path does not use it.
var (
	kuwoAudioURL = "https://antiserver.kuwo.cn/anti.s"
	kuwoSecret   = "" // deprecated: playUrl Secret scheme retired 2026
)

// SetSecret overwrites the package-level kuwoSecret. Exposed for the plugin
// YAML override flow (plan C.4 Step 1: const→var runtime override); called by
// internal/plugin/registry.go::RefreshOverrides only.
func (s *Server) SetSecret(secret string) {
	kuwoSecret = secret
}

// SetAPIBase overwrites the package-level kuwoAudioURL. Same lifecycle as
// SetSecret above.
func (s *Server) SetAPIBase(apiBase string) {
	kuwoAudioURL = apiBase
}

// GetAudioURL handler (gRPC) on kuwo.TagSource. Resolves a playable audio
// URL via the antiserver convert_url3 endpoint (the Secret-free replacement
// for the retired playUrl API). Best-effort: paid / region-locked tracks
// return empty url, which we propagate as ("", nil) so the gateway
// /api/stream proxy fallback (handler.StreamAudio) keeps the "preview
// unavailable" envelope.
func (s *Server) GetAudioURL(ctx context.Context, req *pb.GetAudioRequest) (*pb.GetAudioResponse, error) {
	if req.Id == "" {
		return &pb.GetAudioResponse{}, nil
	}
	// rid must carry the MUSIC_ prefix for convert_url3 (search already
	// strips it when building Song.Id, so we re-add it here).
	rid := req.Id
	if !strings.HasPrefix(rid, "MUSIC_") {
		rid = "MUSIC_" + rid
	}
	urlStr := fmt.Sprintf("%s?type=convert_url3&rid=%s&format=mp3&response=url", kuwoAudioURL, rid)
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	httpReq.Header.Set("User-Agent", headers["User-Agent"])
	httpReq.Header.Set("Referer", "http://www.kuwo.cn/")
	resp, err := s.client.Do(httpReq)
	if err != nil {
		log.Printf("[kuwo] GetAudioURL http error: %v", err)
		return &pb.GetAudioResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		log.Printf("[kuwo] GetAudioURL unmarshal (head=%q): %v", preview(body, 80), err)
		return &pb.GetAudioResponse{}, nil
	}
	if data.Code != 200 || data.URL == "" {
		// Ops-side signal: antiserver returns code!=200 or empty url for
		// paid / region-locked tracks. Body head goes to docker logs so
		// `grep '[kuwo] GetAudioURL empty url'` shows what came back.
		log.Printf("[kuwo] GetAudioURL empty url code=%d upstream status=%d body=%q",
			data.Code, resp.StatusCode, preview(body, 80))
		return &pb.GetAudioResponse{}, nil
	}
	return &pb.GetAudioResponse{Url: data.URL}, nil
}
