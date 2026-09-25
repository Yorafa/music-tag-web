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
	"log"
	"net/http"
	"strings"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin"
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
			// Kugou's WebFilter search reports `Duration` in SECONDS
			// (verified live: 119 for a 1:59 track — the same value
			// NetEase sends as 119133 ms). Also accept `duration` in case
			// the shape moves back to the mobile endpoint.
			if d, ok := m["Duration"]; ok {
				m["duration"] = plugin.DurationFromSeconds(d)
			} else {
				m["duration"] = plugin.DurationFromSeconds(m["duration"])
			}
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
		Duration: plugin.DurationFromSeconds(m["duration"]),
	}
}

// GetAudioURL fetches a short-lived upstream audio-stream URL for the given
// kugou song hash (id == FileHash from doSearch). Best-effort: when the
// upstream returns empty url (anti-bot, paid track, mobile-only CDN), we
// propagate ("", nil) so the gateway /api/stream proxy fallback can take
// over.
//
// kgAudioURL is the mobile song-info endpoint used to resolve a playable
// audio URL from a kugou FileHash. The previous
// https://www.kugou.com/yy/index.php?r=play/getdata endpoint now returns
// err_code 20010 / 30020 for anonymous traffic (verified 2026-07), but the
// H5-era m.kugou.com getSongInfo.php?cmd=playInfo still serves free tracks:
//
//	https://m.kugou.com/app/i/getSongInfo.php?cmd=playInfo&hash=<hash>&dfid=<dfid>&mid=<mid>&platid=4
//	→ {"errcode":0,"url":"https://sharefs.kugou.com/...mp3","pay_type":0}
//
// Paid / region-locked tracks come back with errcode 0 + empty url (or
// pay_type>0) — we propagate empty so the gateway keeps the "preview
// unavailable" envelope.
//
// kgAudioURL is declared as `var` (not `const`) so the plugin YAML override
// (plan C.4 / Stage B) can swap it at runtime via (*Server).SetAPIBase.
// Note: kg does NOT ship with a Secret constant in source — anonymous
// search is unauthenticated and the upstream uses an MD5 signature derived
// from the public `keyCodeTemplate` (also a const). SetSecret is intentionally
// NOT exposed for kg; secrets: {} entries in the YAML are no-ops here.
var kgAudioURL = "https://m.kugou.com/app/i/getSongInfo.php"

// kgDFID / kgMID are the device-fingerprint params the H5 getSongInfo
// endpoint requires (bare requests return errcode 0 + empty url). Values
// verified against a real free-track lookup in 2026-07; both are opaque to
// the CDN and stable across calls.
var (
	kgDFID = "3iH5Iv2u6WqG3y2O2d1d0eZ0"
	kgMID  = "5c7d3e0d2e4f4a8e9c8f6d5e4b3a2c1d"
)

// SetAPIBase overwrites the package-level kgAudioURL for the plugin YAML
// override flow.
// SetAPIBase repoints the upstream. Rejects anything that is not https
// (REVIEW.md P1-4): kgAudioURL is concatenated into every subsequent
// request, so an unvalidated override could both downgrade the connection
// and redirect it to an attacker-controlled host. On rejection the previous
// value is kept — a bad config file must not break a running plugin.
func (s *Server) SetAPIBase(apiBase string) {
	if err := plugin.ValidateAPIBase(apiBase); err != nil {
		log.Printf("[kg] SetAPIBase rejected: %v (keeping %q)", err, kgAudioURL)
		return
	}
	kgAudioURL = apiBase
}

func (s *Server) GetAudioURL(ctx context.Context, req *pb.GetAudioRequest) (*pb.GetAudioResponse, error) {
	if req.Id == "" {
		return &pb.GetAudioResponse{}, nil
	}
	// The H5 endpoint rejects bare requests (empty url + errcode 0) unless
	// they carry the same dfid/mid/platid params the m.kugou.com app sends
	// (verified 2026-07). dfid is a stable cookie-format device id; mid is a
	// random device fingerprint that does not need to round-trip.
	urlStr := fmt.Sprintf("%s?cmd=playInfo&hash=%s&dfid=%s&mid=%s&platid=4",
		kgAudioURL, req.Id, kgDFID, kgMID)
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36")
	httpReq.Header.Set("Referer", "http://m.kugou.com/")
	resp, err := s.client.Do(httpReq)
	if err != nil {
		log.Printf("[kg] GetAudioURL http error: %v", err)
		return &pb.GetAudioResponse{}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var raw struct {
		ErrCode int    `json:"errcode"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		log.Printf("[kg] GetAudioURL unmarshal (head=%q): %v", previewKg(body, 80), err)
		return &pb.GetAudioResponse{}, nil
	}
	if raw.ErrCode == 0 && raw.URL != "" {
		return &pb.GetAudioResponse{Url: raw.URL}, nil
	}
	// getSongInfo failed (paid track / anti-bot errcode 1002 / region-lock):
	// fall back to the trackercdn mirrors the reference go-music-dl project
	// uses. Verified 2026-08: the cmd=4 mirror (key=md5(hash+"kgcloud"),
	// vip=1) serves a playable url even when getSongInfo errcodes.
	if u := s.trackercdnFallback(ctx, req.Id); u != "" {
		return &pb.GetAudioResponse{Url: u}, nil
	}
	// Ops-side signal for the kg 502-when-listening case: paid track
	// (errcode 0 + empty url + pay_type>0), anti-bot block, or
	// region-lock. Body head (first 80 bytes) included so
	// `docker logs | grep '\[kg\] GetAudioURL empty url'` shows
	// exactly what kugou returned.
	log.Printf("[kg] GetAudioURL empty url errcode=%d upstream status=%d body=%q (trackercdn fallback empty too)",
		raw.ErrCode, resp.StatusCode, previewKg(body, 80))
	return &pb.GetAudioResponse{}, nil
}

// trackercdnFallback tries the trackercdn mirrors from the reference
// go-music-dl project (kugou/kugou.go::fetchTrackerSongInfo). Returns the
// first non-empty url, or "" if every mirror fails. Mirrors are ordered
// best-first; each is keyed with the md5(hash+salt) the CDN expects and the
// hash must be lowercase for the tracker API.
func (s *Server) trackercdnFallback(ctx context.Context, hash string) string {
	hash = strings.ToLower(strings.TrimSpace(hash))
	keyV2 := kgMD5(hash + "kgcloudv2")
	keyOld := kgMD5(hash + "kgcloud")
	urls := []string{
		fmt.Sprintf("https://trackercdn.kugou.com/i/v2/?cdnBackup=1&behavior=download&pid=1&cmd=21&appid=1001&hash=%s&key=%s", hash, keyV2),
		fmt.Sprintf("http://trackercdn.kugou.com/i/?cmd=4&pid=1&forceDown=0&vip=1&hash=%s&key=%s", hash, keyOld),
	}
	for _, u := range urls {
		httpReq, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36")
		httpReq.Header.Set("Referer", "https://www.kugou.com/")
		resp, err := s.client.Do(httpReq)
		if err != nil {
			continue
		}
		body, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			continue
		}
		var m map[string]interface{}
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		if url := pickKgURL(m["url"]); url != "" {
			return strings.ReplaceAll(url, `\/`, "/")
		}
		if url := pickKgURL(m["backup_url"]); url != "" {
			return strings.ReplaceAll(url, `\/`, "/")
		}
	}
	return ""
}

// pickKgURL extracts the first non-empty string from a trackercdn `url` /
// `backup_url` field, which may be a plain string OR a JSON array of
// mirrors. Mirrors the reference kugou pickKugouURL.
func pickKgURL(v interface{}) string {
	switch u := v.(type) {
	case string:
		return u
	case []interface{}:
		for _, item := range u {
			if s, ok := item.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// kgMD5 returns the lowercase hex md5 of s (the tracker API keys are
// lowercase; kugouSignature above stays uppercase for the search API).
func kgMD5(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// previewKg returns the first n bytes of body for log lines. Used by
// [kg] GetAudioURL so docker logs capture enough of the upstream payload
// (status + json head) for ops to tell "anti-bot block" from "paid track"
// from "endpoint churn" at a glance. Mirrors kuwo/server.go::preview
// but kept plugin-local so the two plugins remain independently
// testable / deployable.
func previewKg(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}
