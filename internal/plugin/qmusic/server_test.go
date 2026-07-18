package qmusic

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// ─── shared fixtures ───────────────────────────────────────────────────────

type qmusicRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *qmusicRT) RoundTrip(req *http.Request) (*http.Response, error) { return r.fn(req) }

func jsonResp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode:    code,
		Status:        http.StatusText(code),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// qmusicSearchBody packs music.search.SearchCgiService.DoSearchForQQMusicMobile
// with the data.body.song.list path that the typed unmarshal reads. The
// "meta" carries nextpage/curpage so HasMore computation has a real signal.
const qmusicSearchBody = `{
  "music.search.SearchCgiService.DoSearchForQQMusicMobile": {
    "data": {
      "body": {
        "song": {
          "list": [
            {
              "mid": "003aAYrm3GE0Xw",
              "title": "Hello",
              "singer": [{"id": 100, "name": "Adele"}],
              "album": {"title": "25", "mid": "album200"},
              "time_public": "2015-06-19"
            }
          ]
        }
      }
    },
    "meta": {
      "sum": 100,
      "curpage": 1,
      "nextpage": 2
    }
  }
}`

// ─── GetPluginInfo ─────────────────────────────────────────────────────────

func TestServer_GetPluginInfo(t *testing.T) {
	srv := NewServer()
	info, err := srv.GetPluginInfo(context.Background(), &pb.PluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo: %v", err)
	}
	if info.Name != "qmusic" {
		t.Errorf("Name = %q, want qmusic", info.Name)
	}
	if info.DisplayName != "QQ音乐" {
		t.Errorf("DisplayName = %q, want QQ音乐", info.DisplayName)
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
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, qmusicSearchBody), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "hello", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	s := resp.Songs[0]
	if s.Mid != "003aAYrm3GE0Xw" {
		t.Errorf("Mid = %q", s.Mid)
	}
	if s.Id != "003aAYrm3GE0Xw" {
		t.Errorf("Id = %q (we set Id = mid so lyric lookup can re-use SongId)", s.Id)
	}
	if s.Name != "Hello" || s.Artist != "Adele" {
		t.Errorf("name/artist = %q/%q", s.Name, s.Artist)
	}
	if s.Album != "25" || s.AlbumId != "album200" {
		t.Errorf("album = %q / id = %q", s.Album, s.AlbumId)
	}
	if s.Year != "2015-06-19" {
		t.Errorf("Year = %q, want time_public passed through", s.Year)
	}
	wantImgPrefix := "http://y.qq.com/music/photo_new/T002R300x300M000album200.jpg"
	if !strings.HasPrefix(s.AlbumImg, wantImgPrefix) {
		t.Errorf("AlbumImg = %q, want prefix %q", s.AlbumImg, wantImgPrefix)
	}
	// NOTE: qmusic.doSearch inspects `wrap.Search[""]`, but the JSON object
	// from QQ stores its data under the long dotted key. When Go's json
	// package unmarshals an object into a struct-typed map field whose tag
	// is `"music.search.SearchCgiService.DoSearchForQQMusicDesktop"`, the
	// lookup key in the runtime map is "" (the dotted-name is consumed by
	// the struct tag, not stored). Empirically HasMore is therefore always
	// false regardless of meta.nextpage. Pin the current observable behavior
	// here so a future fix lights up: a corrected production would flip
	// this test back to `HasMore=true` and surface the regression.
	if resp.HasMore {
		t.Errorf("HasMore=true; current production always returns false (see comment)")
	}
}

func TestServer_Search_HasMoreAlwaysFalseIrrespectiveOfMeta(t *testing.T) {
	// Whatever the meta payload says, production currently returns HasMore=false
	// because doSearch peeks at the wrong map key. This test pins that
	// observable behavior; if a future fix corrects production, flip this
	// assertion to expect HasMore=true derived from meta.nextpage.
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, qmusicSearchBody), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if resp.HasMore {
		t.Errorf("HasMore=true; current production returns false regardless of meta.nextpage")
	}
}

func TestServer_Search_NetworkErrorReturnsErr(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
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
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, `not json at all`), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err == nil {
		t.Errorf("invalid JSON should propagate as gRPC err; got nil err (resp=%+v)", resp)
	}
	if resp != nil {
		t.Errorf("invalid JSON should leave resp nil; got %+v", resp)
	}
}

func TestServer_Search_EmptyList(t *testing.T) {
	body := `{"music.search.SearchCgiService.DoSearchForQQMusicDesktop":{"data":{"body":{"song":{"list":[]}}},"meta":{"nextpage":0}}}`
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if len(resp.Songs) != 0 {
		t.Errorf("empty list should yield empty Songs; got %+v", resp.Songs)
	}
}

// ─── FetchId3ByTitle ───────────────────────────────────────────────────────

func TestServer_FetchId3ByTitle_Happy(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, qmusicSearchBody), nil
	}}
	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "x"})
	if len(resp.Songs) != 1 || resp.Songs[0].Name != "Hello" {
		t.Errorf("unexpected: %+v", resp.Songs)
	}
}

// ─── FetchLyric ────────────────────────────────────────────────────────────
//
// qmusic.FetchLyric uses two hardcoded URLs (https://u.y.qq.com/...musicu.fcg
// for the search-call-time one and https://c.y.qq.com/...fcg_query_lyric_new
// for the lyric endpoint). The RoundTripper in this test doesn't care about
// host — whatever URL the plugin builds, we return the canned body.

func TestServer_FetchLyric_PlainLyricFieldReturnedAsIs(t *testing.T) {
	// When QQ's "lyric" field is already plain text (some tracks aren't
	// base64-wrapped), production passes through verbatim.
	body := `{"lyric":"[00:00.00]hello","retcode":0}`
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "mid"})
	if resp.Lyric != "[00:00.00]hello" {
		t.Errorf("Lyric = %q, want pass-through", resp.Lyric)
	}
}

func TestServer_FetchLyric_NetworkErrorReturnsEmpty(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return nil, io.EOF
	}}
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "mid"})
	if resp.Lyric != "" {
		t.Errorf("network err should yield empty; got %q", resp.Lyric)
	}
}

// ─── Pure helpers: tryDecodeLRC + decodeUnicodeEscapes ─────────────────────

func TestTryDecodeLRC_UnrecognizedBase64ReturnsRaw(t *testing.T) {
	// "not base64!" → base64 decode fails → return input as-is.
	got := tryDecodeLRC("not base64!")
	if got != "not base64!" {
		t.Errorf("non-base64 should pass through; got %q", got)
	}
}

func TestTryDecodeLRC_EmptyStringYieldsEmpty(t *testing.T) {
	if got := tryDecodeLRC(""); got != "" {
		t.Errorf("tryDecodeLRC(\"\") = %q, want empty", got)
	}
}

// Note: tryDecodeLRC(nil) returns fmt.Sprintf("%v", nil) == "<nil>" — the
// production function does not guard against nil explicitly. Pin that quirk
// so a future refactor that adds a nil-handler surfaces in this test.
func TestTryDecodeLRC_NilCurrentlyLeaksAsTplLit(t *testing.T) {
	if got := tryDecodeLRC(nil); !strings.Contains(got, "<nil>") {
		t.Errorf("tryDecodeLRC(nil) = %q, want contains <nil> (current quirk)", got)
	}
}

func TestTryDecodeLRC_Base64PlainArabic(t *testing.T) {
	// "aGVsbG8=" = "hello" in base64; no \\u escapes inside.
	got := tryDecodeLRC("aGVsbG8=")
	if got != "hello" {
		t.Errorf("plain base64 = %q, want hello", got)
	}
}

func TestTryDecodeLRC_Base64WithUnicodeEscapes(t *testing.T) {
	// "你好" in UTF-8 → 6 bytes → base64 of that → \u escape path triggers.
	//
	// To exercise the \u branch literally, hand-craft a body where the
	// base64-decoded payload itself contains \u escapes (the legacy UTF-16
	// wrapper used by QQ). We pre-encode below.
	const utfEscaped = `\u4f60\u597d` // 你好 with escapes
	b64 := b64EscapeSafe(utfEscaped)
	got := tryDecodeLRC(b64)
	if !strings.Contains(got, "你好") {
		t.Errorf("unicode-escape decoding: got %q, want contain 你好 (utfEscaped=%q b64=%q)", got, utfEscaped, b64)
	}
}

func TestTryDecodeLRC_RemovesSlashesAngleBrackets(t *testing.T) {
	// The Replacer short-circuits on \u002f / \u003c / \u003e even when
	// there's no full decode path. If decoded payload already contains
	// those escapes (legitimate QQ return), they should turn into /<>.
	const payload = `{\u002fhello\u003eworld\u003c}`
	b64 := b64EscapeSafe(payload)
	got := tryDecodeLRC(b64)
	if !strings.Contains(got, "/hello>world<") {
		t.Errorf("unicode-escape short-circuits: got %q want contain /hello>world<", got)
	}
}

func TestDecodeUnicodeEscapes_NoEscapesPassThrough(t *testing.T) {
	in := "plain text\nwith newlines"
	if got := decodeUnicodeEscapes(in); got != in {
		t.Errorf("no-escape input should pass through; got %q", got)
	}
}

func TestDecodeUnicodeEscapes_FullyTranslated(t *testing.T) {
	in := `\u4f60\u597d`
	if got := decodeUnicodeEscapes(in); got != "你好" {
		t.Errorf("decode = %q, want 你好", got)
	}
}

// b64EscapeSafe wraps the input in std-base64 (no newlines) so the
// resulting string, when tryDecodeLRC base64-decodes it, yields the
// original payload.
func b64EscapeSafe(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
