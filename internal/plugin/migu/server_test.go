package migu

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// ─── shared fixtures ───────────────────────────────────────────────────────

type miguRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *miguRT) RoundTrip(req *http.Request) (*http.Response, error) { return r.fn(req) }

func jsonResp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode:    code,
		Status:        http.StatusText(code),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

const miguSearchBody = `{
  "songResultData": {
    "result": [
      {
        "name": "Hello",
        "singers": [{"name": "Adele"}],
        "albums": [{"name": "25", "id": 200}],
        "imgItems": [{"img": "https://example/cover.jpg"}],
        "lyricUrl": "https://example/lrc/abc.lrc"
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
	if info.Name != "migu" {
		t.Errorf("Name = %q, want migu", info.Name)
	}
	if info.DisplayName != "咪咕音乐" {
		t.Errorf("DisplayName = %q, want 咪咕音乐", info.DisplayName)
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
	srv.client.Transport = &miguRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, miguSearchBody), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "hello", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	s := resp.Songs[0]
	if s.Name != "Hello" {
		t.Errorf("Name = %q, want Hello", s.Name)
	}
	if s.Artist != "Adele" {
		t.Errorf("Artist = %q, want Adele (single-singer path)", s.Artist)
	}
	if s.Album != "25" || s.AlbumId != "200" {
		t.Errorf("Album=%+v want 25/id 200", s)
	}
	if s.AlbumImg != "https://example/cover.jpg" {
		t.Errorf("AlbumImg = %q", s.AlbumImg)
	}
	// Id is sourced from lyricUrl — production sets this so FetchLyric can
	// re-issue a GET later. Ours gets carried through verbatim.
	if s.Id != "https://example/lrc/abc.lrc" {
		t.Errorf("Id = %q should equal lyricUrl", s.Id)
	}
	// migu has 1 row < limit 10 → HasMore=false.
	if resp.HasMore {
		t.Errorf("HasMore=true; want false")
	}
}

func TestServer_Search_MultiArtistJoinedBySlash(t *testing.T) {
	body := `{"songResultData":{"result":[{"name":"x","singers":[{"name":"a"},{"name":"b"}],"lyricUrl":"u","albums":[],"imgItems":[]}]}}`
	srv := NewServer()
	srv.client.Transport = &miguRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if resp.Songs[0].Artist != "a/b" {
		t.Errorf("multi-artist should be a/b; got %q", resp.Songs[0].Artist)
	}
}

func TestServer_Search_HasMoreOnFullPage(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"songResultData":{"result":[`)
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"s","singers":[],"albums":[],"imgItems":[]}`)
	}
	b.WriteString(`]}}`)
	srv := NewServer()
	srv.client.Transport = &miguRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, b.String()), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if !resp.HasMore {
		t.Errorf("HasMore=false; want true")
	}
}

func TestServer_Search_NetworkErrorReturnsErr(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &miguRT{fn: func(*http.Request) (*http.Response, error) {
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

func TestServer_Search_InvalidJSONReturnsErr(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &miguRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, `garbage`), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err == nil {
		t.Errorf("invalid JSON should propagate as gRPC err; got nil err (resp=%+v)", resp)
	}
	if resp != nil {
		t.Errorf("invalid JSON should leave resp nil; got %+v", resp)
	}
}

// ─── FetchId3ByTitle ───────────────────────────────────────────────────────

func TestServer_FetchId3ByTitle_Happy(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &miguRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, miguSearchBody), nil
	}}
	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "x"})
	if len(resp.Songs) != 1 || resp.Songs[0].Name != "Hello" {
		t.Errorf("unexpected: %+v", resp.Songs)
	}
}

// ─── FetchLyric ────────────────────────────────────────────────────────────
//
// migu FetchLyric uses req.SongId directly as the URL. To avoid the roundTripper
// dispatching on URL.host in tests, we feed the SongId as the URL of a local
// httptest.NewServer — and the mock transport routes that hostname to the test
// server. This is the only plugin where FetchLyric URL-comes-from-input.

func TestServer_FetchLyric_Happy(t *testing.T) {
	body := "[00:00.00]Hello\n[00:01.00]world"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	// Build a stub server so the test URL ends up in our `SongId` field; any
	// GET to that URL (migu's FetchLyric behavior) should return `body`.
	u, _ := url.Parse(srv.URL)
	s := NewServer()
	s.client.Transport = &miguRT{fn: func(req *http.Request) (*http.Response, error) {
		// Sanity: migu FetchLyric GETs the SongId URL verbatim.
		if req.URL.Host != u.Host {
			t.Errorf("FetchLyric should hit SongId host %q; got %q", u.Host, req.URL.Host)
		}
		return jsonResp(200, body), nil
	}}
	resp, _ := s.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: srv.URL + "/lrc"})
	if !strings.Contains(resp.Lyric, "Hello") || !strings.Contains(resp.Lyric, "world") {
		t.Errorf("Lyric = %q, want contains Hello+world", resp.Lyric)
	}
}

func TestServer_FetchLyric_EmptySongIdReturnsEmpty(t *testing.T) {
	srv := NewServer()
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: ""})
	if resp.Lyric != "" {
		t.Errorf("empty SongId should yield empty Lyric; got %q", resp.Lyric)
	}
}
