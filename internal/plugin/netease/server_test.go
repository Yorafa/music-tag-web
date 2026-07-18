package netease

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// ─── shared test fixtures ──────────────────────────────────────────────────

// neteaseRT implements http.RoundTripper so a test can intercept outbound
// HTTPS requests without needing httptest.NewServer or a const→var refactor.
// The plugin's Server struct has `client *http.Client`, whose `Transport`
// field is settable from a same-package test.
type neteaseRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *neteaseRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.fn(req)
}

func jsonResp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode:    code,
		Status:        http.StatusText(code),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// neteaseSearchBody mirrors the /api/search/get response shape that doSearch
// unmarshals. Includes a publishTime (int64 ms) so the year-extraction path
// in normalize runs end-to-end. ID is left as a JSON string here so the
// numeric/string-coerce fallback in str() is exercised against both shapes.
const neteaseSearchBody = `{
  "result": {
    "songs": [
      {
        "id": "12345",
        "name": "Hello",
        "ar": [{"id": 100, "name": "Adele"}],
        "al": {"id": 200, "name": "25", "picUrl": "https://example/cover.jpg"},
        "publishTime": 1425168000000
      }
    ]
  }
}`

// ─── GetPluginInfo (pure) ──────────────────────────────────────────────────

func TestServer_GetPluginInfo(t *testing.T) {
	srv, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	info, err := srv.GetPluginInfo(context.Background(), &pb.PluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo: %v", err)
	}
	if info.Name != "netease" {
		t.Errorf("Name = %q, want netease", info.Name)
	}
	if info.DisplayName != "网易云音乐" {
		t.Errorf("DisplayName = %q, want 网易云音乐", info.DisplayName)
	}
	for _, gate := range []bool{info.SupportsSearch, info.SupportsLyric, info.SupportsId3} {
		if !gate {
			t.Errorf("expected search/lyric/id3 all true; got one false: %+v", info)
		}
	}
}

// ─── Search ────────────────────────────────────────────────────────────────

func TestServer_Search_HappyPath(t *testing.T) {
	srv, _ := NewServer()
	srv.client.Transport = &neteaseRT{
		fn: func(*http.Request) (*http.Response, error) { return jsonResp(200, neteaseSearchBody), nil },
	}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "hello", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len(Songs)=%d, want 1", len(resp.Songs))
	}
	s := resp.Songs[0]
	if s.Id != "12345" {
		t.Errorf("Id = %q, want 12345", s.Id)
	}
	if s.Name != "Hello" {
		t.Errorf("Name = %q, want Hello", s.Name)
	}
	if s.Artist != "Adele" {
		t.Errorf("Artist = %q, want Adele", s.Artist)
	}
	if s.ArtistId != "100" {
		t.Errorf("ArtistId = %q, want 100", s.ArtistId)
	}
	if s.Album != "25" {
		t.Errorf("Album = %q, want 25", s.Album)
	}
	if s.AlbumId != "200" {
		t.Errorf("AlbumId = %q, want 200", s.AlbumId)
	}
	if s.AlbumImg != "https://example/cover.jpg" {
		t.Errorf("AlbumImg = %q, want https://example/cover.jpg", s.AlbumImg)
	}
	if s.Year != "2015" {
		t.Errorf("Year = %q, want 2015 (publishTime=1425168000000ms → 2015-03-01)", s.Year)
	}
	// 1 song < limit 10 → HasMore should be false.
	if resp.HasMore {
		t.Errorf("HasMore=true; want false (len<limit)")
	}
}

func TestServer_Search_HasMoreTrueWhenFullPage(t *testing.T) {
	srv, _ := NewServer()
	// 10 distinct songs to satisfy `len(songs) >= int(req.Limit)`. String id
	// keeps the contract with the test fixture's string assertion at first;
	// str() handles numeric too.
	var b strings.Builder
	b.WriteString(`{"result":{"songs":[`)
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`","name":"s","ar":[{"id":1,"name":"a"}],"al":{}}`)
	}
	b.WriteString(`]}}`)
	srv.client.Transport = &neteaseRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, b.String()), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if !resp.HasMore {
		t.Errorf("HasMore=false; want true (full page)")
	}
}

func TestServer_Search_NetworkErrorReturnsErr(t *testing.T) {
	srv, _ := NewServer()
	srv.client.Transport = &neteaseRT{
		fn: func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF },
	}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err == nil {
		t.Errorf("transport err should propagate as gRPC err; got nil err; resp=%+v", resp)
	}
	if resp != nil {
		t.Errorf("transport err should leave resp nil; got %+v", resp)
	}
}

func TestServer_Search_MalformedJSONReturnsErr(t *testing.T) {
	srv, _ := NewServer()
	srv.client.Transport = &neteaseRT{
		fn: func(*http.Request) (*http.Response, error) { return jsonResp(200, `not json`), nil },
	}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err == nil {
		t.Errorf("malformed JSON should propagate as gRPC err; got nil err (resp=%+v)", resp)
	}
	if resp != nil {
		t.Errorf("malformed JSON should leave resp nil; got %+v", resp)
	}
}

// TestServer_Search_NumericIDDoesNotPanic verifies the original bug —
// /api/search/get delivers id as a JSON number. The previous hard
// `.(string)` cast in Search panicked and crashed the plugin process.
// Now str() handles string|number|float/json.Number gracefully.
func TestServer_Search_NumericIDDoesNotPanic(t *testing.T) {
	srv, _ := NewServer()
	body := `{"result":{"songs":[{"id":12345,"name":"Hello","ar":[{"id":100,"name":"Adele"}],"al":{"id":200,"name":"25"}}]}}`
	srv.client.Transport = &neteaseRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "hi", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search on numeric ID should not error; got %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len(Songs)=%d, want 1", len(resp.Songs))
	}
	if resp.Songs[0].Id != "12345" {
		t.Errorf("Id = %q, want 12345 (numeric ID should coerce without trailing .0)", resp.Songs[0].Id)
	}
}

// ─── FetchId3ByTitle ───────────────────────────────────────────────────────

func TestServer_FetchId3ByTitle_HappyPath(t *testing.T) {
	srv, _ := NewServer()
	srv.client.Transport = &neteaseRT{
		fn: func(*http.Request) (*http.Response, error) { return jsonResp(200, neteaseSearchBody), nil },
	}
	resp, err := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "Hello"})
	if err != nil {
		t.Fatalf("FetchId3ByTitle: %v", err)
	}
	if len(resp.Songs) != 1 || resp.Songs[0].Name != "Hello" {
		t.Errorf("unexpected: %+v", resp.Songs)
	}
}

// ─── FetchLyric ────────────────────────────────────────────────────────────

func TestServer_FetchLyric_HappyPath(t *testing.T) {
	srv, _ := NewServer()
	body := `{"lrc":{"lyric":"[00:00.00]Hello\n[00:01.00]world\n"}}`
	srv.client.Transport = &neteaseRT{
		fn: func(*http.Request) (*http.Response, error) { return jsonResp(200, body), nil },
	}
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "12345"})
	if !strings.Contains(resp.Lyric, "Hello") || !strings.Contains(resp.Lyric, "world") {
		t.Errorf("Lyric = %q, want contains Hello+world", resp.Lyric)
	}
}

func TestServer_FetchLyric_NetworkErrorReturnsEmpty(t *testing.T) {
	srv, _ := NewServer()
	srv.client.Transport = &neteaseRT{
		fn: func(*http.Request) (*http.Response, error) { return nil, io.EOF },
	}
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "12345"})
	if resp.Lyric != "" {
		t.Errorf("Lyric on network error should be empty; got %q", resp.Lyric)
	}
}

// ─── Pure helpers ──────────────────────────────────────────────────────────

func TestNewServer_StableAcrossCalls(t *testing.T) {
	// Two calls to NewServer must succeed and both produce a non-nil *http.Client.
	// Replaces the pre-/api/search/get version of this test that asserted on
	// a package-level AES key (deleted when we dropped the encryption pipeline).
	a, errA := NewServer()
	if errA != nil {
		t.Fatalf("first NewServer: %v", errA)
	}
	b, errB := NewServer()
	if errB != nil {
		t.Fatalf("second NewServer: %v", errB)
	}
	if a.client == nil || b.client == nil {
		t.Errorf("expected non-nil client in both servers")
	}
}

// TestStr_NumericIDCoerces verifies the helper that protects us from the
// regression that motivated rewriting /api/linux/forward → /api/search/get.
// Real upstream returns numeric JSON values for ID; str() must drop the
// trailing ".0" so downstream consumers see "12345", not "12345.0".
func TestStr_NumericIDCoerces(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{nil, ""},
		{"hello", "hello"},
		{float64(12345), "12345"},
		{float64(12345.5), "12345.5"},
		{int(7), "7"},
		{int64(99), "99"},
	}
	for _, c := range cases {
		if got := str(c.in); got != c.want {
			t.Errorf("str(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
