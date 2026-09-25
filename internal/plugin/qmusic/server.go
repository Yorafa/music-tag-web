// Package qmusic QQ音乐 gRPC 插件。走公开 musicu.fcg 端点 (与 django qm.py
// getQQMusicSearch 完全一致)，避开 qqmusic-api-python 难以移植的签名协议。
//
// 歌词走 c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg，返回 base64-encoded。
//
// Module choice: the desktop module `DoSearchForQQMusicDesktop` is gated
// behind a session cookie check (responds with inner code 2001 + empty list
// for unauthenticated callers). The mobile module
// `DoSearchForQQMusicMobile` returns the same response shape but without
// needing a session, so we use it instead.
package qmusic

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin"
)

// qmusicSearchURL is the public QQ Music musicu.fcg POST endpoint that
// all `DoSearchForQQMusicMobile` traffic rides. Declared `var` (not
// `const`) so the plugin YAML override (plan C.4 / Stage B) can swap it
// at runtime via (*Server).SetAPIBase. Note: qmusic has NO Secret header
// (the `authst`/cookie layer is host-bound and rarely worth overriding),
// so SetSecret is intentionally NOT exposed here — secrets: {} entries
// in data/sources/qmusic.yaml are silently no-ops in (*plugin.Registry).
// RefreshOverrides. The lyric endpoint stays hardcoded inline in
// FetchLyric because its response-code signing pairs with that exact
// host; overriding only the search endpoint is the practical case.
var qmusicSearchURL = "https://u.y.qq.com/cgi-bin/musicu.fcg"

// SetAPIBase overwrites the package-level qmusicSearchURL for the
// plugin YAML override flow.
// SetAPIBase repoints the search endpoint. Rejects anything that is not
// https (REVIEW.md P1-4): qmusicSearchURL receives the user's search term,
// so a plaintext override would expose it in transit. On rejection the
// previous value is kept.
func (s *Server) SetAPIBase(apiBase string) {
	if err := plugin.ValidateAPIBase(apiBase); err != nil {
		log.Printf("[qmusic] SetAPIBase rejected: %v (keeping %q)", err, qmusicSearchURL)
		return
	}
	qmusicSearchURL = apiBase
}

var headers = map[string]string{
	"User-Agent":   "QQ音乐/73222 CFNetwork/1406.0.3 Darwin/22.4.0",
	"Referer":      "https://y.qq.com/portal/profile.html",
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
		SupportsAudioUrl: true,
	}, nil
}

func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	songs, hasMore, err := s.doSearch(ctx, req.Query, int(req.Page), int(req.Limit))
	if err != nil {
		return nil, err
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
			"wid":      "",
			"tmeAppID": "qqmusic",
			"authst":   "",
			"uid":      "",
			"gray":     "0",
			"OpenUDID": "2d484d3157d4ed482e406e6c5fdcf8c3d3275deb",
			"ct":       "6",
			"patch":    "2",
			"cv":       "80600",
			"gzip":     "0",
			"qq":       "",
			"nettype":  "2",
		},
		"music.search.SearchCgiService.DoSearchForQQMusicMobile": map[string]interface{}{
			"module": "music.search.SearchCgiService",
			"method": "DoSearchForQQMusicMobile",
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
		qmusicSearchURL,
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

	// QQ 返回结构是 {"music.search.SearchCgiService.DoSearchForQQMusicMobile": {"data": {"body": {"song": {"list": [...]}}}, "meta": {...}}}
	// 注意：当歌曲不存在时, `list` 字段可能取值为 null / [] / 数字 0。用 json.RawMessage
	// 避免父 unmarshal 因 `cannot unmarshal number into []map[string]interface{}`
	// 直接失败；后续由 isQMusicEmptyListSentinel + 条件 unmarshal 还原。
	//
	// BUGFIX (2026-07): the previous shape declared `Search map[string]struct{...}`
	// with the dotted JSON tag on the MAP field — encoding/json then unmarshals
	// the inner object's `data`/`meta` keys AS map keys, so every read via
	// `wrap.Search[""]` returned the zero struct: songs still arrived through the
	// loose fallback, but `has_more` was permanently false (meta.nextpage was
	// never read), breaking 加载更多 pagination. A plain struct field with the
	// dotted tag addresses the exact key and Data/Meta populate correctly.
	var wrap struct {
		Search struct {
			Data struct {
				Body struct {
					// 2026-07: QQ 把移动端搜索结果从 data.body.song.list 迁移到了
					// data.body.item_song（直接数组；meta 变成空对象，nextpage 不再
					// 返回）。item_song 是当前主 shape，song.list 保留作降级。
					ItemSong json.RawMessage `json:"item_song"`
					Song     struct {
						List json.RawMessage `json:"list"`
					} `json:"song"`
				} `json:"body"`
			} `json:"data"`
			Meta struct {
				Sum      int `json:"sum"`
				NextPage int `json:"nextpage"`
				CurPage  int `json:"curpage"`
			} `json:"meta"`
		} `json:"music.search.SearchCgiService.DoSearchForQQMusicMobile"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, false, fmt.Errorf("qmusic parse: %w", err)
	}
	// RawMessage 允许 QQ 返回 null / [] / 数字 0（空结果的三种 sentinel）。
	// 只对真正的数组走 typed unmarshal；其余一律当作「0 首歌」处理。
	var list []map[string]interface{}
	for _, raw := range []json.RawMessage{
		wrap.Search.Data.Body.ItemSong,  // 当前主 shape（2026-07 迁移后）
		wrap.Search.Data.Body.Song.List, // 老 shape 降级
	} {
		if isQMusicEmptyListSentinel(raw) {
			continue
		}
		var tmp []map[string]interface{}
		if err := json.Unmarshal(raw, &tmp); err == nil && len(tmp) > 0 {
			list = tmp
			break
		}
	}
	if len(list) == 0 {
		// fallback：loose parse path. Now that the typed struct field
		// addresses the dotted key correctly, this only fires on genuinely
		// empty/sentinel results or a schema shift we haven't mirrored yet.
		var loose map[string]interface{}
		_ = json.Unmarshal(body, &loose)
		if v, ok := loose["music.search.SearchCgiService.DoSearchForQQMusicMobile"].(map[string]interface{}); ok {
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
			Name: str(firstNonEmpty(item, "title", "name")),
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
			sg.Album = str(firstNonEmpty(album, "title", "name"))
			sg.AlbumID = str(album["mid"])
		}
		sg.Year = str(item["time_public"])
		sg.AlbumImg = "http://y.qq.com/music/photo_new/T002R300x300M000" + sg.AlbumID + ".jpg"
		out = append(out, sg)
	}
	// 新版 API 的 meta 是空对象（nextpage 恒为 0），退化为「满页即有更多」启发式
	// （kuwo/kg/migu 同款）。老版本 meta.nextpage 信号仍然优先。
	hasMore := len(out) >= limit
	if wrap.Search.Meta.NextPage > page {
		hasMore = true
	}
	return out, hasMore, nil
}

func str(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// firstNonEmpty returns the first non-empty string among the given keys of
// item. Used for title/name and album-title drift across QQ API cohorts
// (item_song uses `name` while the legacy song.list used `title`).
func firstNonEmpty(item map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v := str(item[k]); v != "" {
			return v
		}
	}
	return ""
}

// isQMusicEmptyListSentinel 判别 QQ Music 在 Empty-Result 路径上返回的
// 「不算数组」哨兵值：null / [] / 数字（包括 0 和负数）。识别成功后交给
// 商品逻辑跳过 typed unmarshal，避免对 json.RawMessage 调用 json.Unmarshal
// 仍然吃到 `cannot unmarshal number` 错误。
func isQMusicEmptyListSentinel(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "[]" {
		return true
	}
	// QQ 上次实际收到过的形态是「"list":0」与「"list":-1」。只要首字符是
	// `-` 或数字，都视为「不是数组」sentinel，不走 typed unmarshal。
	if len(s) > 0 && (s[0] == '-' || (s[0] >= '0' && s[0] <= '9')) {
		return true
	}
	return false
}

// GetAudioURL resolves a playable stream URL via the musicu.fcg
// music.vkey.GetVkey / UrlGetVkey module — the same one the reference
// go-music-dl project uses (qq/download.go). The trick: request MULTIPLE
// quality levels in one round-trip by passing a `filename` array
// (M800=320k, M500=128k — both built as <prefix><songmid><songmid>.mp3),
// then return the first filename that came back with a non-empty `purl`.
// This maximizes the chance of a playable URL (e.g. 128k free preview when
// 320k is VIP-locked) without a second request.
//
// Response shape (verified 2026-08):
//
//	{ "req_1": { "data": {
//	    "sip": ["http://aqqmusic.tc.qq.com/", ...],
//	    "midurlinfo": [{"filename":"M800<mid><mid>.mp3",
//	                    "purl":"...?guid=...&vkey=...&uin=&fromtag=3"}, ...]
//	}}}
//
// Full URL = sip[0] + purl (falls back to the ws.stream host when sip is
// absent). Anonymous calls return empty `purl` entries from non-Mainland-
// China exit IPs (geo-locked / paid), which we propagate as ("", nil) so the
// gateway /api/stream proxy envelope stays "preview unavailable:
// source=qmusic".
//
// Best-effort contract preserved: ("", nil) on any failure.
func (s *Server) GetAudioURL(ctx context.Context, req *pb.GetAudioRequest) (*pb.GetAudioResponse, error) {
	if req.Id == "" {
		return &pb.GetAudioResponse{}, nil
	}
	// <prefix><songmid><songmid>.mp3 — same filename scheme as the reference.
	filenames := []string{
		fmt.Sprintf("M800%s%s.mp3", req.Id, req.Id), // 320kbps
		fmt.Sprintf("M500%s%s.mp3", req.Id, req.Id), // 128kbps
	}
	guid := fmt.Sprintf("%d", rand.Int63n(9000000000)+1000000000)
	payload := map[string]interface{}{
		"comm": map[string]interface{}{
			"cv":          4747474,
			"ct":          "24",
			"format":      "json",
			"inCharset":   "utf-8",
			"outCharset":  "utf-8",
			"notice":      0,
			"platform":    "yqq.json",
			"needNewCode": 1,
			"uin":         0,
		},
		"req_1": map[string]interface{}{
			"module": "music.vkey.GetVkey",
			"method": "UrlGetVkey",
			"param": map[string]interface{}{
				"guid":      guid,
				"songmid":   []string{req.Id, req.Id},
				"songtype":  []int{0, 0},
				"uin":       "0",
				"loginflag": 1,
				"platform":  "20",
				"filename":  filenames,
			},
		},
	}
	bodyJSON, _ := json.Marshal(payload)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", qmusicSearchURL, strings.NewReader(string(bodyJSON)))
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		log.Printf("[qmusic] GetAudioURL http error: %v", err)
		return &pb.GetAudioResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var wrap struct {
		Req1 struct {
			Data struct {
				Sip        []string `json:"sip"`
				MidURLInfo []struct {
					Filename string `json:"filename"`
					Purl     string `json:"purl"`
				} `json:"midurlinfo"`
			} `json:"data"`
		} `json:"req_1"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		log.Printf("[qmusic] GetAudioURL unmarshal (head=%q): %v", previewQMusic(body, 120), err)
		return &pb.GetAudioResponse{}, nil
	}
	// Iterate the filenames we asked for (best → worst) and return the first
	// one the upstream mapped to a non-empty purl. Guards against the
	// response being reordered or missing entries.
	for _, expected := range filenames {
		for _, mi := range wrap.Req1.Data.MidURLInfo {
			if mi.Filename != expected || mi.Purl == "" {
				continue
			}
			if len(wrap.Req1.Data.Sip) > 0 && wrap.Req1.Data.Sip[0] != "" {
				return &pb.GetAudioResponse{Url: wrap.Req1.Data.Sip[0] + mi.Purl}, nil
			}
			// sip absent → reference hardcodes the ws.stream host.
			return &pb.GetAudioResponse{Url: "https://ws.stream.qqmusic.qq.com/" + mi.Purl}, nil
		}
	}
	// All purls empty — geo-locked / paid / not logged in. Same "preview
	// unavailable" envelope as before.
	return &pb.GetAudioResponse{}, nil
}

// previewQMusic returns the first n bytes of body for log lines (kept
// plugin-local, mirrors kuwo/kg/migu preview helpers).
func previewQMusic(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}
