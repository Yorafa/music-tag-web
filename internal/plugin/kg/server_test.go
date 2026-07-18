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
