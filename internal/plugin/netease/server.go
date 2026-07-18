// Package netease implements the NetEase Cloud Music tag source as a gRPC server.
// Ported from applications/task/services/music_resource.py NetEaseMusicClient.
//
// Endpoint choice: the historical /api/linux/forward pipeline rotated its AES key
// upstream and returns `{"code":400}` on cold calls; the simpler
// /api/search/get endpoint still serves unencrypted search results, so we use
// it instead. No eparams encryption required.
package netease

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

const baseURL = "https://music.163.com"

// searchURL is the unencrypted endpoint we use for Search/FetchId3ByTitle.
// Replaces the dead /api/linux/forward AES-encrypted pipeline that started
// returning {"code":400} on cold calls after NetEase rotated the symmetric key.
const searchURL = baseURL + "/api/search/get"

var defaultHeaders = map[string]string{
	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	"Referer":    "https://music.163.com/",
}

// Server implements the TagSource gRPC service for NetEase Cloud Music.
type Server struct {
	pb.UnimplementedTagSourceServer
	client *http.Client
}

// NewServer creates a new NetEase plugin server.
func NewServer() (*Server, error) {
	return &Server{
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (s *Server) GetPluginInfo(ctx context.Context, _ *pb.PluginInfoRequest) (*pb.PluginInfoResponse, error) {
	return &pb.PluginInfoResponse{
		Name:             "netease",
		DisplayName:      "网易云音乐",
		SupportsSearch:   true,
		SupportsLyric:    true,
		SupportsId3:      true,
		SupportsAudioUrl: true,
	}, nil
}

func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	offset := (req.Page - 1) * req.Limit
	songs, err := s.doSearch(ctx, req.Query, int(offset), int(req.Limit))
	if err != nil {
		return nil, err
	}
	normalized := s.normalize(songs)
	hasMore := len(songs) >= int(req.Limit)

	pbSongs := make([]*pb.Song, len(normalized))
	for i, song := range normalized {
		pbSongs[i] = songToPB(song)
	}
	return &pb.SearchResponse{Songs: pbSongs, HasMore: hasMore}, nil
}

func (s *Server) FetchId3ByTitle(ctx context.Context, req *pb.FetchId3Request) (*pb.FetchId3Response, error) {
	songs, err := s.doSearch(ctx, req.Title, 0, 10)
	if err != nil {
		return &pb.FetchId3Response{}, nil
	}
	normalized := s.normalize(songs)
	pbSongs := make([]*pb.Song, len(normalized))
	for i, song := range normalized {
		pbSongs[i] = songToPB(song)
	}
	return &pb.FetchId3Response{Songs: pbSongs}, nil
}

func (s *Server) FetchLyric(ctx context.Context, req *pb.FetchLyricRequest) (*pb.FetchLyricResponse, error) {
	if req.SongId == "" {
		return &pb.FetchLyricResponse{}, nil
	}
	url := fmt.Sprintf("%s/api/song/lyric?id=%s&lv=-1&kv=-1&tv=-1", baseURL, req.SongId)
	resp, err := s.doGetWithContext(ctx, url)
	if err != nil {
		return &pb.FetchLyricResponse{}, nil
	}
	var data map[string]interface{}
	json.Unmarshal(resp, &data)
	lrc, _ := data["lrc"].(map[string]interface{})
	lyric, _ := lrc["lyric"].(string)
	return &pb.FetchLyricResponse{Lyric: lyric}, nil
}

// --- Internal ---

// doSearch POSTs to /api/search/get with form body. Returns the un-shuffled
// raw song list (each element matching upstream's per-song schema: {id, name,
// ar|artists, al|album, publishTime}). The normalize step folds both old and
// new key shapes into a stable schema so the upstream drift doesn't leak.
func (s *Server) doSearch(ctx context.Context, title string, offset, limit int) ([]map[string]interface{}, error) {
	form := url.Values{
		"s":      {title},
		"type":   {"1"},
		"limit":  {strconv.Itoa(limit)},
		"offset": {strconv.Itoa(offset)},
	}
	resp, err := s.doPostWithContext(ctx, searchURL,
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if uerr := json.Unmarshal(resp, &result); uerr != nil {
		return nil, fmt.Errorf("netease json unmarshal: %w", uerr)
	}
	// The endpoint can return either {"code":...,"result":{"songs":[...]}}
	// or {"result":{"songs":[...]}}. Older payloads used
	// {"songs":[...]} at the top level. Iterate the known containers.
	var list []interface{}
	if resultMap, ok := result["result"].(map[string]interface{}); ok {
		if songs, ok := resultMap["songs"].([]interface{}); ok {
			list = songs
		}
	} else if songs, ok := result["songs"].([]interface{}); ok {
		list = songs
	}
	out := make([]map[string]interface{}, 0, len(list))
	for _, s := range list {
		if m, ok := s.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// songToPB is the per-row mapper fixed to NOT panic on numeric IDs. Older
// fixtures have `"id":"12345"` (string), but the real /api/search/get
// upstream returns `"id":12345` (number). The str() helper is safe in
// both directions; the previous hard `.(string)` cast crashed the
// plugin process on first mismatch, surfacing at the gateway as
// `code=Unavailable desc=error reading from server: EOF`.
func songToPB(m map[string]interface{}) *pb.Song {
	return &pb.Song{
		Id:       str(m["id"]),
		Name:     str(m["name"]),
		Artist:   str(m["artist"]),
		ArtistId: str(m["artist_id"]),
		Album:    str(m["album"]),
		AlbumId:  str(m["album_id"]),
		AlbumImg: str(m["album_img"]),
		Year:     str(m["year"]),
	}
}

// normalize promotes the per-song map to a stable schema. It accepts
// BOTH upstream key variants:
//   - /api/search/get      uses `artists`, `album`, `picUrl`
//   - /api/cloudsearch/get uses `ar`, `al`, `picUrl`
//
// Without this, switching between endpoints would orphan a code path and
// we'd ship empty Artist/Album/AlbumImg values at runtime.
func (s *Server) normalize(songs []map[string]interface{}) []map[string]interface{} {
	for _, song := range songs {
		// artist names + id
		artists := pickList(song, "ar", "artists")
		albumMap := pickMap(song, "al", "album")

		var artist, artistID string
		if len(artists) > 0 {
			names := make([]string, len(artists))
			for i, a := range artists {
				am := asMap(a)
				names[i] = str(am["name"])
			}
			artist = strings.Join(names, ",")
			if am, ok := artists[0].(map[string]interface{}); ok {
				artistID = str(am["id"])
			}
		}

		// year: prefer song-level publishTime (ms epoch); fall back to album.
		year := ""
		if pt, ok := song["publishTime"]; ok {
			if sec := epochMillisSeconds(pt); sec > 0 {
				year = strconv.Itoa(time.Unix(sec, 0).Year())
			}
		}
		if year == "" && albumMap != nil {
			if pt, ok := albumMap["publishTime"]; ok {
				if sec := epochMillisSeconds(pt); sec > 0 {
					year = strconv.Itoa(time.Unix(sec, 0).Year())
				}
			}
		}

		cover := str(pickStr(albumMap, "picUrl"))
		albumName := str(pickStr(albumMap, "name"))
		albumID := str(pickStr(albumMap, "id"))

		song["artist"] = artist
		song["artist_id"] = artistID
		song["album"] = albumName
		song["album_id"] = albumID
		song["album_img"] = cover
		song["year"] = year
	}
	return songs
}

// pickList accepts an interface{} value (map[string]interface{}) and returns
// the [] it contains under whichever of the candidate keys is present. Used
// by the upstream-key-agnostic normalize() flow.
func pickList(m map[string]interface{}, keys ...string) []interface{} {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if arr, ok := v.([]interface{}); ok {
				return arr
			}
		}
	}
	return nil
}

func pickMap(m map[string]interface{}, keys ...string) map[string]interface{} {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if mp, ok := v.(map[string]interface{}); ok {
				return mp
			}
		}
	}
	return nil
}

func pickStr(m map[string]interface{}, k string) interface{} {
	if m == nil {
		return nil
	}
	v, _ := m[k]
	return v
}

func asMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

// epochMillisSeconds returns an int64 Unix-seconds value if v is a numeric
// ms-epoch (e.g. JSON number 1425168000000 → 1425168000). Other shapes return 0.
func epochMillisSeconds(v interface{}) int64 {
	switch x := v.(type) {
	case float64:
		if x <= 0 {
			return 0
		}
		return int64(x / 1000)
	case int:
		if x <= 0 {
			return 0
		}
		return int64(x) / 1000
	case int64:
		if x <= 0 {
			return 0
		}
		return x / 1000
	case json.Number:
		if n, err := x.Int64(); err == nil && n > 0 {
			return n / 1000
		}
	}
	return 0
}

// str coerces any JSON-decoded value to a string. numbers come through as
// float64 (encoding/json), strings come as string, nil → "". This is the
// same defensive helper qmusic and kuwo already use; netease picks it up
// to fix the panic-on-numeric-ID regression.
func str(v interface{}) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case float64:
		// JSON numbers without a fractional part: drop the ".0" tail so the
		// downstream string round-trip looks like a normal ID, not "12345.0".
		if s == float64(int64(s)) {
			return strconv.FormatInt(int64(s), 10)
		}
		return strconv.FormatFloat(s, 'f', -1, 64)
	case int:
		return strconv.Itoa(s)
	case int64:
		return strconv.FormatInt(s, 10)
	case json.Number:
		return s.String()
	}
	return fmt.Sprintf("%v", v)
}

func (s *Server) doGetWithContext(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	for k, v := range defaultHeaders {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *Server) doPostWithContext(ctx context.Context, url, contentType string, body io.Reader) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", url, body)
	req.Header.Set("Content-Type", contentType)
	for k, v := range defaultHeaders {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// GetAudioURL is a documented stub for NetEase Cloud Music.
//
// As of 2024-2026, NetEase's audio-stream URL endpoint (/api/song/enhance/
// player/url) requires EAPI/WeAPI encryption (CryptoJS AES with rotating
// upstream keys) or a session cookie from a logged-in /vip account. The
// historical /api/linux/forward pipeline started returning {"code":400} on
// cold calls after NetEase rotated the symmetric key, so we cannot reliably
// reproduce the encryption from a Go-only client without a sidecar.
//
// Best-effort contract: we always return ("", nil) so callers (gateway
// /api/stream proxy or frontend PlayButton) treat empty url + SupportsAudioUrl
// == true as a "transient upstream failure" signal. The proxy path then asks
// NetEase through the gateway. SupportsAudioUrl=true here just tells callers
// the source CAN provide audio if asked via the proxy.
func (s *Server) GetAudioURL(_ context.Context, _ *pb.GetAudioRequest) (*pb.GetAudioResponse, error) {
	return &pb.GetAudioResponse{}, nil
}
