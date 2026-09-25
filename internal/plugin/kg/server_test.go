package kg

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// ─── shared fixtures ───────────────────────────────────────────────────────

type kgRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *kgRT) RoundTrip(req *http.Request) (*http.Response, error) { return r.fn(req) }

func jsonResp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode:    code,
		Status:        http.StatusText(code),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// kgSearchBody is in the shape doSearch unmarshals: {"data":{"lists":[...]}}.
// Each lists element carries FileHash/SongName/SingerName/SingerId/AlbumName/
// AlbumID/Image/PublishTime — every key the mapToPBSong m-To-PB mapping reads.
const kgSearchBody = `{
  "data": {
    "lists": [
      {
        "FileHash": "abc123",
        "SongName": "Hello <em>remix</em>",
        "SingerName": "Adele、Beat",
        "SingerId": 100,
        "AlbumName": "25",
        "AlbumID": 200,
        "Image": "https://example/{size}.jpg",
        "PublishTime": "2015-03-01"
      }
    ]
  }
}`

// ─── GetPluginInfo ─────────────────────────────────────────────────────────

func TestServer_GetPluginInfo(t *testing.T) {
	srv := NewServer()
	info, err := srv.GetPluginInfo(context.Background(), &pb.PluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo: %v", err)
	}
	if info.Name != "kugou" {
		t.Errorf("Name = %q, want kugou", info.Name)
	}
	if info.DisplayName != "酷狗音乐" {
		t.Errorf("DisplayName = %q, want 酷狗音乐", info.DisplayName)
	}
	for _, gate := range []bool{info.SupportsSearch, info.SupportsLyric, info.SupportsId3} {
		if !gate {
			t.Errorf("expected all-true; got %+v", info)
		}
	}
}

// ─── Search ────────────────────────────────────────────────────────────────

func TestServer_Search_HappyPath(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, kgSearchBody), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "hello", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	s := resp.Songs[0]
	if s.Id != "abc123" {
		t.Errorf("Id = %q, want abc123 (FileHash)", s.Id)
	}
	if s.Name != "Hello remix" {
		t.Errorf("Name = %q, want <em> stripped", s.Name)
	}
	if s.Artist != "Adele,Beat" {
		t.Errorf("Artist = %q, want 、→, replaced", s.Artist)
	}
	if s.Album != "25" {
		t.Errorf("Album = %q, want 25", s.Album)
	}
	if s.AlbumImg != "https://example/150.jpg" {
		t.Errorf("AlbumImg = %q, want {size}→150 substituted", s.AlbumImg)
	}
}

func TestServer_Search_HasMoreWhenFullPage(t *testing.T) {
	srv := NewServer()
	var b strings.Builder
	b.WriteString(`{"data":{"lists":[`)
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"FileHash":"h","SongName":"s","SingerName":"a","SingerId":1,"AlbumName":"al","AlbumID":2,"Image":"","PublishTime":""}`)
	}
	b.WriteString(`]}}`)
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, b.String()), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if !resp.HasMore {
		t.Errorf("HasMore=false; want true (10 items, limit 10)")
	}
}

func TestServer_Search_NetworkErrorReturnsErr(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return nil, io.EOF
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err == nil {
		t.Errorf("transport err should propagate as gRPC err; got nil err")
	}
	if resp != nil {
		t.Errorf("transport err should leave resp nil; got %+v", resp)
	}
}

func TestServer_Search_EmptyResult(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, `{"data":{"lists":[]}}`), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if len(resp.Songs) != 0 || resp.HasMore {
		t.Errorf("empty result: got len=%d hasMore=%v", len(resp.Songs), resp.HasMore)
	}
}

// ─── GetAudioURL (m.kugou getSongInfo) ─────────────────────────────────────

// TestServer_GetAudioURL_ReturnsUrl pins the 2026-era playback fix: the
// www.kugou.com/yy/index.php?r=play/getdata endpoint now returns err_code
// 20010/30020 for anonymous traffic, but m.kugou.com/app/i/getSongInfo.php
// (cmd=playInfo) still serves free tracks with a real `url`. GetAudioURL
// must call that endpoint and return the url field.
func TestServer_GetAudioURL_ReturnsUrl(t *testing.T) {
	body := `{"errcode":0,"url":"https://sharefs.kugou.com/v3/abc.mp3","pay_type":0,"privilege":0}`
	var gotURL string
	srv := NewServer()
	srv.client.Transport = &kgRT{fn: func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResp(200, body), nil
	}}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "abc123"})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if !strings.Contains(gotURL, "cmd=playInfo") || !strings.Contains(gotURL, "hash=abc123") {
		t.Errorf("request URL=%q (want getSongInfo cmd=playInfo + hash)", gotURL)
	}
	// The H5 endpoint rejects bare requests (errcode 0 + empty url) unless
	// they carry the dfid/mid/platid device-fingerprint params (verified
	// 2026-07). Pin them so a future refactor can't silently drop them.
	for _, want := range []string{"dfid=" + kgDFID, "mid=" + kgMID, "platid=4"} {
		if !strings.Contains(gotURL, want) {
			t.Errorf("request URL=%q missing %q (device fingerprint params)", gotURL, want)
		}
	}
	if resp.GetUrl() != "https://sharefs.kugou.com/v3/abc.mp3" {
		t.Errorf("url=%q (want getSongInfo url field)", resp.GetUrl())
	}
}

// TestServer_GetAudioURL_FallsBackToTrackercdn pins the 2026-08 fallback:
// when getSongInfo errcodes (anti-bot 1002, paid, region-lock), GetAudioURL
// retries the trackercdn mirrors (reference go-music-dl kugou fetchTracker-
// SongInfo). The cmd=4 mirror with md5(hash+"kgcloud") key serves a url for
// free tracks; the fallback must lower-case the hash for the tracker API.
func TestServer_GetAudioURL_FallsBackToTrackercdn(t *testing.T) {
	srv := NewServer()
	var trackerURL string
	srv.client.Transport = &kgRT{fn: func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.String(), "getSongInfo") {
			return jsonResp(200, `{"errcode":1002,"url":""}`), nil // anti-bot rate limit
		}
		trackerURL = req.URL.String()
		return jsonResp(200, `{"status":1,"errcode":0,"url":"https://fsvippc.tx.kugou.com/real.mp3"}`), nil
	}}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "ABC123"})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if resp.GetUrl() != "https://fsvippc.tx.kugou.com/real.mp3" {
		t.Errorf("url=%q (want trackercdn fallback url)", resp.GetUrl())
	}
	if !strings.Contains(trackerURL, "trackercdn") {
		t.Errorf("fallback request=%q (want trackercdn mirror)", trackerURL)
	}
	if !strings.Contains(trackerURL, "hash=abc123") {
		t.Errorf("fallback request=%q (want lowercase hash)", trackerURL)
	}
}

// TestPickKgURL_HandlesStringAndArray pins the trackercdn url/backup_url
// shape tolerance: the fields can be a plain string OR a mirror array, and
// the raw body may escape slashes (\\/) which must be un-escaped.
func TestPickKgURL_HandlesStringAndArray(t *testing.T) {
	if got := pickKgURL("https://a/1.mp3"); got != "https://a/1.mp3" {
		t.Errorf("string pick=%q", got)
	}
	if got := pickKgURL([]interface{}{"", "https://b/2.mp3"}); got != "https://b/2.mp3" {
		t.Errorf("array pick=%q", got)
	}
	if got := pickKgURL(42); got != "" {
		t.Errorf("non-string pick=%q (want empty)", got)
	}
}

// TestServer_GetAudioURL_PaidTrackReturnsEmpty pins the paid/region-locked
// path: paid tracks come back errcode 0 + empty url (or errcode != 0) AND
// the trackercdn fallback also returns nothing — GetAudioURL yields ("",
// nil) so the gateway keeps the "preview unavailable" envelope.
func TestServer_GetAudioURL_PaidTrackReturnsEmpty(t *testing.T) {
	body := `{"errcode":0,"url":"","pay_type":3,"privilege":10}`
	srv := NewServer()
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "abc123"})
	if resp.GetUrl() != "" {
		t.Errorf("url=%q (want empty on paid track)", resp.GetUrl())
	}
}

// ─── FetchId3ByTitle ───────────────────────────────────────────────────────

func TestServer_FetchId3ByTitle_Happy(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, kgSearchBody), nil
	}}
	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "hello"})
	if len(resp.Songs) != 1 || resp.Songs[0].Name != "Hello remix" {
		t.Errorf("unexpected: %+v", resp.Songs)
	}
}

// ─── FetchLyric ────────────────────────────────────────────────────────────

func TestServer_FetchLyric_Happy(t *testing.T) {
	srv := NewServer()
	body := "[00:00.00]Hello\n[00:01.50]world"
	srv.client.Transport = &kgRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "abc123"})
	if !strings.Contains(resp.Lyric, "Hello") {
		t.Errorf("Lyric = %q, want contains Hello", resp.Lyric)
	}
}

// ─── Pure helper ───────────────────────────────────────────────────────────

func TestKugouSignature_KnownVector(t *testing.T) {
	// MD5("abc") → "900150983cd24fb0d6963f7d28e17f72", uppercased as
	// kugouSignature does.
	const want = "900150983CD24FB0D6963F7D28E17F72"
	if got := kugouSignature("abc"); got != want {
		t.Errorf("kugouSignature(\"abc\") = %q, want %q", got, want)
	}
}

func TestKugouSignature_StableForFixedInput(t *testing.T) {
	// Whether or not we ship the correct hash, signatures must be stable for
	// the same input across calls (catches any `time.Now()` leak).
	a := kugouSignature("hello")
	b := kugouSignature("hello")
	if a != b {
		t.Errorf("signature not stable: %q vs %q", a, b)
	}
}
