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
	// meta.nextpage = 2 > page 1 → HasMore must be true. The fixture's
	// nextpage is the pagination signal the frontend 加载更多 button reads;
	// before the typed-parse fix doSearch peeked `wrap.Search[""]` and
	// HasMore was permanently false.
	if !resp.HasMore {
		t.Errorf("HasMore=false; want true (meta.nextpage=2 > page=1)")
	}
}

func TestServer_Search_HasMoreDerivedFromMetaNextPage(t *testing.T) {
	// meta.nextpage=2, request page=1 → HasMore=true.
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, qmusicSearchBody), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if !resp.HasMore {
		t.Errorf("HasMore=false; want true when meta.nextpage=2 > page=1")
	}
}

func TestServer_Search_HasMoreFalseWhenNextPageEqualsPage(t *testing.T) {
	// meta.nextpage = curpage = 1 → no more pages → HasMore=false.
	body := `{"music.search.SearchCgiService.DoSearchForQQMusicMobile":{"data":{"body":{"song":{"list":[{"mid":"a","title":"t"}]}}},"meta":{"sum":1,"nextpage":1,"curpage":1}}}`
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if resp.HasMore {
		t.Errorf("HasMore=true; want false when meta.nextpage=1 == page=1")
	}
	if len(resp.Songs) != 1 {
		t.Errorf("len=%d, want 1 (typed list parse must still work)", len(resp.Songs))
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

// TestServer_Search_ParsesNewItemSongShape pins the 2026-07 upstream
// migration: QQ moved mobile search results from data.body.song.list to
// data.body.item_song (a direct array; meta is now an empty object). The
// typed parse MUST read item_song — before this fix the plugin returned 0
// songs (the old song.list key simply no longer exists upstream).
func TestServer_Search_ParsesNewItemSongShape(t *testing.T) {
	body := `{"music.search.SearchCgiService.DoSearchForQQMusicMobile":{"code":0,"data":{"body":{"item_song":[{"mid":"001Bbywq2gicae","name":"搁浅","singer":[{"id":4558,"mid":"0025NhlN2yWrP4","name":"周杰伦"}],"album":{"id":20612,"mid":"003DFRzD192KKD","name":"七里香"},"time_public":"2004-08-03","file":{"media_mid":"004UlK9x0jeuow"}}]}},"meta":{}}}`
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "搁浅", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1 (item_song shape must parse)", len(resp.Songs))
	}
	s := resp.Songs[0]
	if s.Mid != "001Bbywq2gicae" || s.Id != "001Bbywq2gicae" {
		t.Errorf("Id/Mid = %q/%q (want 001Bbywq2gicae)", s.Id, s.Mid)
	}
	if s.Name != "搁浅" {
		t.Errorf("Name = %q (item_song uses `name`, want 搁浅)", s.Name)
	}
	if s.Artist != "周杰伦" {
		t.Errorf("Artist = %q", s.Artist)
	}
	if s.Album != "七里香" || s.AlbumId != "003DFRzD192KKD" {
		t.Errorf("album = %q/%q", s.Album, s.AlbumId)
	}
}

// TestServer_Search_HasMoreFallsBackToFullPage pins the meta-is-empty
// regression: the new API returns an empty `meta` object (no nextpage), so
// HasMore can no longer be derived from meta.nextpage. It must fall back to
// the full-page heuristic (len(out) >= limit) like kuwo/kg/migu — otherwise
// 加载更多 never lights up.
func TestServer_Search_HasMoreFallsBackToFullPage(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"music.search.SearchCgiService.DoSearchForQQMusicMobile":{"code":0,"data":{"body":{"item_song":[`)
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"mid":"m` + string(rune('a'+i)) + `","name":"s"}`)
	}
	b.WriteString(`]}},"meta":{}}}`)
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, b.String()), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if !resp.HasMore {
		t.Errorf("HasMore=false; want true (10 items, empty meta, limit 10 → full-page fallback)")
	}
}

// TestServer_GetAudioURL_ReturnsSipPlusPurl pins the multi-quality vkey
// dance: the musicu.fcg UrlGetVkey response carries `sip` (CDN roots) +
// `midurlinfo[].{filename,purl}`; GetAudioURL must pick the BEST filename
// (M800 320k first, then M500 128k) that got a non-empty purl and return
// sip[0]+purl (CN IP path).
func TestServer_GetAudioURL_ReturnsSipPlusPurl(t *testing.T) {
	body := `{"req_1":{"data":{"sip":["http://aqqmusic.tc.qq.com/","http://sjy6.stream.qqmusic.qq.com/"],"midurlinfo":[{"filename":"M800001Bbywq2gicae001Bbywq2gicae.mp3","purl":"M800001Bbywq2gicae001Bbywq2gicae.mp3?guid=1&vkey=ABC&uin=&fromtag=3"}]}}}`
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "001Bbywq2gicae"})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	const want = "http://aqqmusic.tc.qq.com/M800001Bbywq2gicae001Bbywq2gicae.mp3?guid=1&vkey=ABC&uin=&fromtag=3"
	if resp.GetUrl() != want {
		t.Errorf("url=%q (want %q)", resp.GetUrl(), want)
	}
}

// TestServer_GetAudioURL_PrefersHigherQuality pins the quality ordering:
// when BOTH M800 (320k) and M500 (128k) come back with purls, GetAudioURL
// must return the M800 one (filenames are matched in best-first order).
func TestServer_GetAudioURL_PrefersHigherQuality(t *testing.T) {
	body := `{"req_1":{"data":{"sip":["http://aqqmusic.tc.qq.com/"],"midurlinfo":[{"filename":"M500mm.mp3","purl":"LOW.mp3"},{"filename":"M800mm.mp3","purl":"HIGH.mp3"}]}}}`
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "m"})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if resp.GetUrl() != "http://aqqmusic.tc.qq.com/HIGH.mp3" {
		t.Errorf("url=%q (want M800/higher quality even when listed after M500)", resp.GetUrl())
	}
}

// TestServer_GetAudioURL_FallsBackToWSStream pins the no-sip path: when the
// response omits `sip`, GetAudioURL must still return a url by prefixing the
// reference project's hardcoded ws.stream host.
func TestServer_GetAudioURL_FallsBackToWSStream(t *testing.T) {
	body := `{"req_1":{"data":{"midurlinfo":[{"filename":"M500mm.mp3","purl":"low.mp3?guid=1&vkey=X"}]}}}`
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "m"})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if resp.GetUrl() != "https://ws.stream.qqmusic.qq.com/low.mp3?guid=1&vkey=X" {
		t.Errorf("url=%q (want ws.stream fallback host)", resp.GetUrl())
	}
}

// TestServer_GetAudioURL_EmptyPurlReturnsEmpty pins the geo-locked path:
// anonymous non-CN calls return purl="" (result 104003) — GetAudioURL must
// yield ("", nil) so the gateway keeps the "preview unavailable" envelope
// instead of a broken URL.
func TestServer_GetAudioURL_EmptyPurlReturnsEmpty(t *testing.T) {
	body := `{"req_1":{"data":{"sip":["http://aqqmusic.tc.qq.com/"],"midurlinfo":[{"filename":"M800mm.mp3","purl":""},{"filename":"M500mm.mp3","purl":""}]}}}`
	srv := NewServer()
	srv.client.Transport = &qmusicRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "m"})
	if resp.GetUrl() != "" {
		t.Errorf("url=%q (want empty when all purls empty)", resp.GetUrl())
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
