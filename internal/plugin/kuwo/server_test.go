package kuwo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// ─── shared fixtures ───────────────────────────────────────────────────────

type kuwoRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *kuwoRT) RoundTrip(req *http.Request) (*http.Response, error) { return r.fn(req) }

func jsonResp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode:    code,
		Status:        http.StatusText(code),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// kuwoSearchBody mirrors the clean-JSON searchMusicBykeyWord response shape
// (reference go-music-dl endpoint; verified 2026-08). Fields are properly
// escaped — no JS-style parsing needed.
const kuwoSearchBody = `{
  "abslist": [
    {
      "MUSICRID": "MUSIC_12345",
      "SONGNAME": "Hello",
      "ARTIST": "Adele",
      "ALBUM": "25",
      "ALBUMID": "200",
      "hts_MVPIC": "https://img4.kuwo.cn/wmvpic/abc.jpg"
    }
  ]
}`

// ─── GetPluginInfo ─────────────────────────────────────────────────────────

func TestServer_GetPluginInfo(t *testing.T) {
	srv := NewServer()
	info, err := srv.GetPluginInfo(context.Background(), &pb.PluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo: %v", err)
	}
	if info.Name != "kuwo" {
		t.Errorf("Name = %q, want kuwo", info.Name)
	}
	if info.DisplayName != "酷我音乐" {
		t.Errorf("DisplayName = %q, want 酷我音乐", info.DisplayName)
	}
	for _, gate := range []bool{info.SupportsSearch, info.SupportsLyric, info.SupportsId3} {
		if !gate {
			t.Errorf("expected all-true; got %+v", info)
		}
	}
}

// ─── formatLRC pure helper ─────────────────────────────────────────────────

func TestFormatLRC_HoursMinutesSeconds(t *testing.T) {
	lrclist := []struct {
		Time      string `json:"time"`
		LineLyric string `json:"lineLyric"`
	}{
		{Time: "0", LineLyric: "[00:00.00] already gone"}, // weird input; parser falls back to 0
		{Time: "61.91", LineLyric: "first line"},
		{Time: "3725.5", LineLyric: "second line"}, // 1h 2m 5s
	}
	got := formatLRC(lrclist)
	for _, want := range []string{"0:00:00", "0:01:01first line", "1:02:05second line"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatLRC missing %q; got %q", want, got)
		}
	}
}

func TestFormatLRC_Empty(t *testing.T) {
	if got := formatLRC(nil); got != "" {
		t.Errorf("empty lrclist should yield empty string; got %q", got)
	}
}

// ─── Search ────────────────────────────────────────────────────────────────

func TestServer_Search_HappyPath(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, kuwoSearchBody), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Query: "hello", Page: 1, Limit: 10})
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	s := resp.Songs[0]
	if s.Id != "12345" {
		t.Errorf("Id = %q, want 12345 (MUSIC_ prefix stripped)", s.Id)
	}
	if s.Name != "Hello" {
		t.Errorf("Name = %q, want Hello", s.Name)
	}
	if s.Artist != "Adele" || s.Album != "25" || s.AlbumId != "200" {
		t.Errorf("song=%+v", s)
	}
	if s.AlbumImg != "https://img4.kuwo.cn/wmvpic/abc.jpg" {
		t.Errorf("AlbumImg = %q (want hts_MVPIC cover)", s.AlbumImg)
	}
}

func TestServer_Search_FallsBackToMusicListWhenAbslistEmpty(t *testing.T) {
	// ABSLIST empty; MUSICLIST present with one entry (SINGER field cohort).
	body := `{"musiclist":[{"MUSICRID":"MUSIC_99","SONGNAME":"fallback","SINGER":"x","ALBUM":"y","ALBUMID":"5"}]}`
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Query: "x", Page: 1, Limit: 10})
	if len(resp.Songs) != 1 || resp.Songs[0].Name != "fallback" || resp.Songs[0].Artist != "x" {
		t.Errorf("musiclist fallback not honored; got %+v", resp.Songs)
	}
}

func TestServer_Search_HTTPErrorReturnsErr(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return nil, io.EOF
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err == nil {
		t.Errorf("transport err should propagate; got nil err (resp=%+v)", resp)
	}
	if resp != nil {
		t.Errorf("transport err should leave resp nil; got %+v", resp)
	}
}

func TestServer_Search_InvalidJSONToleratedAsEmpty(t *testing.T) {
	// kuwo 上游偶尔返回非 JSON体（HTML 错误页、JS 模板异常、字符截断…）。
	// 我们不要把整次搜索请求变成 gRPC fail —— log 一行、返 0 首歌、nil error，
	// 让 gateway 仍能 fan-out 其余 plugin，前端照常拿到其余来源的搜索结果。
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, `{garbage`), nil
	}}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err != nil {
		t.Errorf("unparseable JSON must yield empty Songs + nil err; got err=%v (resp=%+v)", err, resp)
	}
	if resp == nil {
		t.Fatalf("resp nil on empty Songs is unexpected (Search contract: non-nil resp with Songs=[])")
	}
	if len(resp.Songs) != 0 {
		t.Errorf("unparseable JSON should yield 0 songs; got %+v", resp.Songs)
	}
}

// ─── GetAudioURL (antiserver convert_url3) ─────────────────────────────────

// TestServer_GetAudioURL_AntiserverReturnsUrl pins the 2026-era playback
// fix: the www.kuwo.cn/api/v1/www/music/playUrl endpoint now 403s anonymous
// traffic ("The request is illegal!") — the Secret header scheme was
// retired. GetAudioURL must call antiserver convert_url3 with the MUSIC_
// prefixed rid and return its `url` field.
func TestServer_GetAudioURL_AntiserverReturnsUrl(t *testing.T) {
	body := `{"code":200,"msg":"success","url":"https://nf-sycdn.kuwo.cn/abc123.mp3"}`
	var gotURL string
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResp(200, body), nil
	}}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "12345"})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if !strings.Contains(gotURL, "type=convert_url3") || !strings.Contains(gotURL, "rid=MUSIC_12345") {
		t.Errorf("request URL=%q (want antiserver convert_url3 + MUSIC_ prefixed rid)", gotURL)
	}
	if resp.GetUrl() != "https://nf-sycdn.kuwo.cn/abc123.mp3" {
		t.Errorf("url=%q (want antiserver url field)", resp.GetUrl())
	}
}

// TestServer_GetAudioURL_Non200CodeReturnsEmpty pins the paid/region-locked
// path: antiserver returns code!=200 (or empty url) for tracks it can't
// serve — GetAudioURL yields ("", nil) so the gateway keeps the "preview
// unavailable" envelope.
func TestServer_GetAudioURL_Non200CodeReturnsEmpty(t *testing.T) {
	body := `{"code":201,"msg":"forbidden","url":""}`
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "12345"})
	if resp.GetUrl() != "" {
		t.Errorf("url=%q (want empty on non-200 code)", resp.GetUrl())
	}
}

// ─── FetchId3ByTitle ───────────────────────────────────────────────────────

func TestServer_FetchId3ByTitle_Happy(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, kuwoSearchBody), nil
	}}
	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "Hello"})
	if len(resp.Songs) != 1 || resp.Songs[0].Id != "12345" {
		t.Errorf("unexpected: %+v", resp.Songs)
	}
}

// ─── FetchLyric ────────────────────────────────────────────────────────────

func TestServer_FetchLyric_FormatsLRCAndAppendsCover(t *testing.T) {
	body := `{"data":{"lrclist":[{"time":"61.91","lineLyric":"line1"},{"time":"120","lineLyric":"line2"}],"songinfo":{"pic":"https://example/cover.jpg"}}}`
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "12345"})
	if !strings.Contains(resp.Lyric, "0:01:01line1") {
		t.Errorf("lyric should contain formatted first line; got %q", resp.Lyric)
	}
	if !strings.Contains(resp.Lyric, "[cover]https://example/cover.jpg") {
		t.Errorf("lyric should append [cover]<pic-url>; got %q", resp.Lyric)
	}
}

func TestServer_FetchLyric_NoCoverWhenMissing(t *testing.T) {
	body := `{"data":{"lrclist":[{"time":"0","lineLyric":"only"}],"songinfo":{}}}`
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "12345"})
	if strings.Contains(resp.Lyric, "[cover]") {
		t.Errorf("should not append [cover] when pic absent; got %q", resp.Lyric)
	}
	if !strings.Contains(resp.Lyric, "only") {
		t.Errorf("line lost; got %q", resp.Lyric)
	}
}

func TestServer_FetchLyric_FallsBackToRawBodyOnParseError(t *testing.T) {
	// Non-JSON body — production code path returns body verbatim.
	srv := NewServer()
	body := "raw text"
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "1"})
	if resp.Lyric != "raw text" {
		t.Errorf("expected raw body fallback; got %q", resp.Lyric)
	}
}
