// Package netease implements the NetEase Cloud Music tag source as a gRPC server.
// Ported from applications/task/services/music_resource.py NetEaseMusicClient and
// aligned with the reference go-music-dl / music-lib netease package (2026-08):
//
//   - Search uses the linux-client forward pipeline
//     (POST /api/linux/forward with AES-128-ECB `eparams`, public key) as the
//     primary path, with the legacy unencrypted /api/search/get as a fallback.
//   - GetAudioURL resolves real stream URLs via the weapi pipeline
//     (POST /weapi/song/enhance/player/url, AES-128-CBC + RSA — both fixed
//     public keys, portable in pure Go, see crypto.go).
//
// Both encryption schemes use fixed public keys, so unlike the old
// /api/linux/forward AES rotation failure mode ({"code":400} on cold calls)
// the current keys are stable and verified against the live upstream.
package netease

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

const baseURL = "https://music.163.com"

// forwardURL is the linux-client forward pipeline — the primary search path.
// The body is an AES-128-ECB-encrypted `eparams` envelope pointing at the
// cloudsearch endpoint (see crypto.go::encryptLinux). Response is the plain
// cloudsearch JSON shape (result.songs[].ar/al).
//
// https, not http (REVIEW.md P1-4): this carries the user's search term and
// the returned song metadata, both of which were plaintext on the wire and
// modifiable in transit.
const forwardURL = "https://music.163.com/api/linux/forward"

// searchURL is the legacy unencrypted search endpoint, kept as a fallback
// when the forward pipeline is unreachable or returns an empty result.
const searchURL = baseURL + "/api/search/get"

// weapiURL is the weapi audio-stream endpoint used by GetAudioURL
// (AES-128-CBC + RSA form body, see crypto.go::encryptWeapi).
//
// https, not http (REVIEW.md P1-4): the response carries the signed CDN
// audio URL, so plaintext here let a network position swap the stream for
// one of their choosing.
const weapiURL = "https://music.163.com/weapi/song/enhance/player/url"

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

// doSearch runs the linux-forward pipeline first, falling back to the legacy
// /api/search/get endpoint on transport/parse errors or an empty result (the
// forward pipeline is the reference go-music-dl path; the legacy endpoint is
// kept so a single upstream quirk never blanks the whole source). Returns the
// raw per-song maps ({id, name, ar|artists, al|album, publishTime}) for the
// normalize step to fold into a stable schema.
func (s *Server) doSearch(ctx context.Context, title string, offset, limit int) ([]map[string]interface{}, error) {
	songs, err := s.doLinuxForwardSearch(ctx, title, offset, limit)
	if err == nil && len(songs) > 0 {
		return songs, nil
	}
	if err != nil {
		log.Printf("[netease] linux forward search failed (%v); falling back to /api/search/get", err)
	} else {
		log.Printf("[netease] linux forward search returned 0 songs; falling back to /api/search/get")
	}
	return s.doLegacySearch(ctx, title, offset, limit)
}

// doLinuxForwardSearch POSTs an AES-128-ECB-encrypted `eparams` envelope to
// /api/linux/forward. The envelope targets the cloudsearch endpoint; the
// response is plain cloudsearch JSON.
func (s *Server) doLinuxForwardSearch(ctx context.Context, title string, offset, limit int) ([]map[string]interface{}, error) {
	ep := map[string]interface{}{
		"method": "POST",
		// Left as http: this is a *parameter* naming the endpoint that
		// netease's own forward service will call on our behalf, not a URL
		// we dial. It travels inside the AES-128-ECB envelope, so it is not
		// plaintext on the wire. The request we actually make is forwardURL,
		// which is https.
		"url": "http://music.163.com/api/cloudsearch/pc",
		"params": map[string]interface{}{
			"s":      title,
			"type":   1,
			"offset": offset,
			"limit":  limit,
		},
	}
	raw, _ := json.Marshal(ep)
	form := url.Values{"eparams": {encryptLinux(string(raw))}}
	resp, err := s.doPostWithContext(ctx, forwardURL,
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	return parseSearchSongs(resp)
}

// doLegacySearch POSTs the unencrypted form body to /api/search/get.
func (s *Server) doLegacySearch(ctx context.Context, title string, offset, limit int) ([]map[string]interface{}, error) {
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
	return parseSearchSongs(resp)
}

// parseSearchSongs pulls the songs array out of the shared response shapes:
// {"code":...,"result":{"songs":[...]}} (both forward and legacy) or a
// legacy top-level {"songs":[...]}.
func parseSearchSongs(resp []byte) ([]map[string]interface{}, error) {
	var result map[string]interface{}
	if uerr := json.Unmarshal(resp, &result); uerr != nil {
		return nil, fmt.Errorf("netease json unmarshal: %w", uerr)
	}
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

// GetAudioURL resolves a real stream URL via the weapi pipeline
// (POST /weapi/song/enhance/player/url). The weapi scheme uses fixed public
// keys (AES-128-CBC nonce + random secKey, RSA of the reversed secKey — see
// crypto.go), so it is fully reproducible from pure Go — no sidecar, no
// rotating-key guessing.
//
// Response shape (verified 2026-08):
//
//	{"code":200,"data":[{"id":...,"url":"http://m7.music.126.net/...mp3?e=...","br":320000,"code":200}]}
//
// Non-CN exit IPs (and anonymous callers of paid tracks) get an EMPTY body
// from the upstream — that is a geo/VIP restriction, not a client bug. We
// propagate ("", nil) so the gateway /api/stream keeps the "preview
// unavailable: source=netease" envelope instead of a misleading 200. From a
// CN / logged-in deployment the URL populates and playback works.
//
// Best-effort contract preserved: ("", nil) on any failure.
func (s *Server) GetAudioURL(ctx context.Context, req *pb.GetAudioRequest) (*pb.GetAudioResponse, error) {
	if req.Id == "" {
		return &pb.GetAudioResponse{}, nil
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"ids":        []string{req.Id},
		"br":         320000,
		"csrf_token": "",
	})
	params, encSecKey, err := encryptWeapi(string(payload))
	if err != nil {
		// Best-effort contract above: no audio URL rather than an error.
		log.Printf("[netease] GetAudioURL weapi encrypt id=%s: %v", req.Id, err)
		return &pb.GetAudioResponse{}, nil
	}
	form := url.Values{
		"params":    {params},
		"encSecKey": {encSecKey},
	}
	resp, err := s.doPostWithContext(ctx, weapiURL,
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		log.Printf("[netease] GetAudioURL http error id=%s: %v", req.Id, err)
		return &pb.GetAudioResponse{}, nil
	}
	var data struct {
		Code int `json:"code"`
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if uerr := json.Unmarshal(resp, &data); uerr != nil {
		log.Printf("[netease] GetAudioURL unmarshal id=%s (len=%d): %v", req.Id, len(resp), uerr)
		return &pb.GetAudioResponse{}, nil
	}
	if data.Code != 200 || len(data.Data) == 0 || data.Data[0].URL == "" {
		// Empty body / empty url = geo or VIP restriction (see doc above).
		log.Printf("[netease] GetAudioURL empty url code=%d id=%s body_len=%d", data.Code, req.Id, len(resp))
		return &pb.GetAudioResponse{}, nil
	}
	return &pb.GetAudioResponse{Url: data.Data[0].URL}, nil
}
