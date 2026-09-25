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
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

// baseURL is the public migu CDN root for search/audio/lyric endpoints.
// Declared `var` (not `const`) so tests can swap it for an httptest.Server
// in NewClientForTest / TestDoSearch_* paths.
var baseURL = "http://pd.musicapp.migu.cn/MIGUM2.0/v1.0/content"

var headers = map[string]string{
	"User-Agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 13_2_3 like Mac OS X) AppleWebKit/605.1.15",
	"Referer":    "http://music.migu.cn/",
}

type Server struct {
	pb.UnimplementedTagSourceServer
	client *http.Client

	// lyricCache maps migu contentId → the lyric endpoint URL carried
	// alongside it in songResultData.result[]. Migu's search response
	// returns a transient lyricUrl/trcUrl that's only valid when hit from
	// migu's IP/CDN, so we cache the URL keyed by the canonical contentId
	// and FetchLyric resolves req.SongId → lyric endpoint later.
	lyricCache sync.Map
}

func NewServer() *Server {
	return &Server{client: &http.Client{Timeout: 10 * time.Second}}
}

func (s *Server) GetPluginInfo(_ context.Context, _ *pb.PluginInfoRequest) (*pb.PluginInfoResponse, error) {
	return &pb.PluginInfoResponse{
		Name: "migu", DisplayName: "咪咕音乐",
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

// FetchLyric: req.SongId is now the canonical numeric migu songid
// (set by Search). We resolve it back to the lyric endpoint via the
// lyricCache populated by Search. If the songid was never cached (e.g.
// FetchLyric called before Search for that id, or a server restart
// wiped the cache), we return empty rather than mis-GETting the songid
// against the lyric endpoint — that would surface a stream-shaped
// binary blob as "lyric text" downstream.
func (s *Server) FetchLyric(ctx context.Context, req *pb.FetchLyricRequest) (*pb.FetchLyricResponse, error) {
	if req.SongId == "" {
		return &pb.FetchLyricResponse{}, nil
	}
	cached, ok := s.lyricCache.Load(req.SongId)
	if !ok {
		return &pb.FetchLyricResponse{}, nil
	}
	lyricURL, ok := cached.(string)
	if !ok || lyricURL == "" {
		return &pb.FetchLyricResponse{}, nil
	}
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", lyricURL, nil)
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
		"ua":           {"Android_migu"},
		"version":      {"5.0.1"},
		"text":         {title},
		"pageNo":       {strconv.Itoa(page)},
		"pageSize":     {strconv.Itoa(limit)},
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
		// CRITICAL: Song.Id MUST be the migu contentId (the stable
		// upstream track id). GetAudioURL's MIGUM3.0 listen.do endpoint
		// REQUIRES contentId — the legacy numeric `id` (e.g. 3790007) is
		// rejected with 参数校验失败 contentId:must not be blank — and
		// copyrightId can be "0", so no in-process cache is needed: the
		// id alone survives the gateway/worker round trip and restart.
		// This mirrors the reference go-music-dl ID contract (which packs
		// contentId|resourceType|formatType into the id; we keep just
		// contentId and hardcode the verified-working resourceType=2 /
		// toneFlag=HQ listen combo).
		//
		// Field-name fallbacks cover API cohort drift: most responses have
		// `contentId`; on a future migration we may need `content_id` /
		// `resourceId` / legacy `id`. Try in order until first non-empty.
		sg := song{
			ID:   miguFirstNonEmpty(item, "contentId", "content_id", "resourceId", "id"),
			Name: str(item["name"]),
			Year: "",
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
		// Cache the lyric endpoint URL keyed by the canonical contentId so
		// FetchLyric can resolve req.SongId → lyricURL later. We prefer
		// lyricUrl (full LRC) and fall back to trcUrl (translation) only.
		if sg.ID != "" {
			if u := str(item["lyricUrl"]); u != "" {
				sv.lyricCache.Store(sg.ID, u)
			} else if u := str(item["trcUrl"]); u != "" {
				sv.lyricCache.Store(sg.ID, u)
			}
		}
		out = append(out, sg)
	}
	return out, len(out) >= limit, nil
}

// miguFirstNonEmpty returns the first non-empty string from the JSON
// object's keys, in order. Used to tolerate field-name drift across
// migu API cohorts (e.g. a future API version that renames `id` to
// `contentId`). Returns "" if all keys are absent or empty.
func miguFirstNonEmpty(item map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v := str(item[k]); v != "" {
			return v
		}
	}
	return ""
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

// miguListenURL is the MIGUM3.0 listen endpoint that replaced the retired
// audio_only route. It requires the {netType, resourceType, contentId,
// copyrightId, channel, toneFlag} param set — see GetAudioURL. Declared
// `var` so tests + the plugin YAML override can swap it.
var miguListenURL = "http://pd.musicapp.migu.cn/MIGUM3.0/v1.0/content/sub/listen.do"

// SetAPIBase overwrites the package-level migu base URLs. baseURL (used by
// Search's /search_all.do) is rewritten verbatim; miguListenURL is rebuilt
// from the new CDN root `apiBase`. Plan C.4 / Stage B wires the YAML
// override flow to call this on startup and on POST /api/sources/refresh.
// Migu has no Secret constant upstream (the public endpoint is anonymous),
// so SetSecret is intentionally NOT exposed here — secrets: {} entries in
// data/sources/migu.yaml are silently no-ops in
// (*plugin.Registry).RefreshOverrides.
func (s *Server) SetAPIBase(apiBase string) {
	baseURL = apiBase
	// listen.do lives under the MIGUM3.0 CDN root, a sibling of the
	// MIGUM2.0 base Search uses. Rebuild the path explicitly instead of
	// path-traversal concatenation (apiBase+"/../MIGUM3.0/...") which
	// breaks against custom mirror roots. Falls back to appending the
	// full MIGUM3.0 path when apiBase is already a bare host/root.
	base := strings.TrimSuffix(apiBase, "/")
	if strings.HasSuffix(base, "/MIGUM2.0/v1.0/content") {
		base = strings.TrimSuffix(base, "/MIGUM2.0/v1.0/content")
	}
	miguListenURL = base + "/MIGUM3.0/v1.0/content/sub/listen.do"
}

// GetAudioURL fetches a short-lived upstream audio-stream URL for the
// given migu contentId via the MIGUM3.0 listen.do endpoint.
//
// The old /audio_only/data route was retired upstream (299996) and listen.do
// rejects the legacy numeric songid (参数校验失败 contentId:must not be blank).
// Song.Id now IS the contentId (see doSearch), so GetAudioURL needs no
// in-process cache: contentId + copyrightId=0 (verified 2026-08 to return a
// playable url) + the verified resourceType=2 / toneFlag=HQ combo resolve a
// stream for any caller, across gateway/worker restarts. This mirrors the
// reference go-music-dl design where the download tuple travels inside the
// song id rather than process memory.
//
// Best-effort contract preserved: paid / region-locked tracks return empty
// songListens → ("", nil); callers keep the gateway /api/stream proxy
// fallback.
func (s *Server) GetAudioURL(ctx context.Context, req *pb.GetAudioRequest) (*pb.GetAudioResponse, error) {
	if req.Id == "" {
		return &pb.GetAudioResponse{}, nil
	}
	urlStr := fmt.Sprintf("%s?netType=01&resourceType=2&contentId=%s&copyrightId=0&channel=0&toneFlag=HQ",
		miguListenURL, req.Id)
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		log.Printf("[migu] GetAudioURL http error id=%s: %v", req.Id, err)
		return &pb.GetAudioResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var raw struct {
		Code        string `json:"code"`
		SongListens []struct {
			URL string `json:"url"`
		} `json:"songListens"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		log.Printf("[migu] GetAudioURL unmarshal id=%s (head=%q): %v", req.Id, previewMigu(body, 256), err)
		return &pb.GetAudioResponse{}, nil
	}
	if raw.Code != "000000" || len(raw.SongListens) == 0 || raw.SongListens[0].URL == "" {
		// Ops signal: paid / region-locked track or endpoint drift. Body
		// head (256 bytes) lets `docker logs | grep '[migu] GetAudioURL'`
		// distinguish 299999 param errors from legit empty songListens.
		log.Printf("[migu] GetAudioURL empty url code=%q id=%s status=%d body=%q",
			raw.Code, req.Id, resp.StatusCode, previewMigu(body, 256))
		return &pb.GetAudioResponse{}, nil
	}
	return &pb.GetAudioResponse{Url: raw.SongListens[0].URL}, nil
}

// previewMigu returns the first n bytes of body for log lines. Mirrors
// kuwo/server.go::preview and kg/server.go::previewKg; kept
// plugin-local so each plugin binary remains independently testable.
func previewMigu(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}
