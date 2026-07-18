package kuwo

import (
	"context"
	"encoding/json"
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

// kuwoSearchBody: search.kuwo.cn/r.s returns JS-style JSON. After parseKuwoJSON
// transforms, we get cleanly-structured abslist entries.
const kuwoSearchJSBody = `{
  abslist: [
    {
      MUSICRID: 'MUSIC_12345',
      NAME: 'Hello',
      ARTIST: 'Adele',
      ALBUM: '25',
      ALBUMID: '200'
    },
  ],
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

// ─── parseKuwoJSON pure helper ─────────────────────────────────────────────

func TestParseKuwoJSON_ConvertsSingleQuotesToDouble(t *testing.T) {
	in := []byte(`{abslist:[{MUSICRID:'MUSIC_1',NAME:'hello',},]}`)
	out := string(parseKuwoJSON(in))
	// Should: quote-key + strip-',]' + strip-trailing-comma-before-'}'. So
	// these tokens MUST appear, and `,]` / `,}` MUST NOT survive.
	for _, must := range []string{`"abslist"`, `"MUSICRID"`, `"MUSIC_1"`, `"NAME"`, `"hello"`, `}]`} {
		if !strings.Contains(out, must) {
			t.Errorf("output missing %q; got %q", must, out)
		}
	}
	for _, forbidden := range []string{`,]`, `,}`, `'`} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output still contains %q (should be stripped); got %q", forbidden, out)
		}
	}
}

func TestParseKuwoJSON_RemovesTrailingCommas(t *testing.T) {
	in := []byte("[1,2,3,]")
	out := string(parseKuwoJSON(in))
	if strings.Contains(out, ",]") {
		t.Errorf("trailing comma not stripped; got %q", out)
	}
}

func TestParseKuwoJSON_HandlesAlreadyValidJSON(t *testing.T) {
	// passthrough: don't mangle standard JSON twice.
	in := []byte(`{"abslist":[{"MUSICRID":"MUSIC_1"}]}`)
	out := string(parseKuwoJSON(in))
	var probe struct {
		Abslist []map[string]interface{} `json:"abslist"`
	}
	if err := json.Unmarshal([]byte(out), &probe); err != nil {
		t.Fatalf("output not valid json: %v (out=%q)", err, out)
	}
	if len(probe.Abslist) != 1 || probe.Abslist[0]["MUSICRID"] != "MUSIC_1" {
		t.Errorf("passthrough lost data: %+v", probe)
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
		return jsonResp(200, kuwoSearchJSBody), nil
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
}

func TestServer_Search_FallsBackToMusicListWhenAbslistEmpty(t *testing.T) {
	// ABSLIST empty; MUSICLIST present with one entry.
	body := `{musiclist:[{MUSICRID:'MUSIC_99',SONGNAME:'fallback',SINGER:'x',ALBUM:'y',ALBUMID:'5'}]}`
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Query: "x", Page: 1, Limit: 10})
	if len(resp.Songs) != 1 || resp.Songs[0].Name != "fallback" {
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

// ─── FetchId3ByTitle ───────────────────────────────────────────────────────

func TestServer_FetchId3ByTitle_Happy(t *testing.T) {
	srv := NewServer()
	srv.client.Transport = &kuwoRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, kuwoSearchJSBody), nil
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
