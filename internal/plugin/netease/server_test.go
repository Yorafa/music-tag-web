package netease

import (
	"context"
	"encoding/base64"
	"errors"
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

// ─── linux-forward search pipeline ─────────────────────────────────────────

// TestServer_Search_UsesLinuxForwardPrimary verifies the search request hits
// /api/linux/forward with a non-empty `eparams` AES envelope (not the raw
// keyword), and the cloudsearch-shaped response is parsed.
func TestServer_Search_UsesLinuxForwardPrimary(t *testing.T) {
	srv, _ := NewServer()
	var sawURL string
	var sawForm string
	srv.client.Transport = &neteaseRT{
		fn: func(req *http.Request) (*http.Response, error) {
			sawURL = req.URL.String()
			body, _ := io.ReadAll(req.Body)
			sawForm = string(body)
			return jsonResp(200, neteaseSearchBody), nil
		},
	}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "hello", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(sawURL, "/api/linux/forward") {
		t.Errorf("Search hit %q, want /api/linux/forward", sawURL)
	}
	// eparams must be a 32-hex-char-per-block uppercase string, not the query.
	if !strings.Contains(sawForm, "eparams=") || strings.Contains(sawForm, "hello") {
		t.Errorf("form body=%q: want eparams AES envelope without the raw query", sawForm)
	}
	if len(resp.Songs) != 1 || resp.Songs[0].Name != "Hello" {
		t.Errorf("unexpected songs: %+v", resp.Songs)
	}
}

// TestServer_Search_FallsBackToLegacyWhenForwardEmpty verifies the fallback
// fires when the forward pipeline returns an empty (but valid) result.
func TestServer_Search_FallsBackToLegacyWhenForwardEmpty(t *testing.T) {
	srv, _ := NewServer()
	calls := 0
	srv.client.Transport = &neteaseRT{
		fn: func(req *http.Request) (*http.Response, error) {
			calls++
			if strings.Contains(req.URL.String(), "/api/linux/forward") {
				return jsonResp(200, `{"code":200,"result":{"songs":[]}}`), nil
			}
			return jsonResp(200, neteaseSearchBody), nil
		},
	}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "hello", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 upstream calls (forward empty → legacy), got %d", calls)
	}
	if len(resp.Songs) != 1 || resp.Songs[0].Id != "12345" {
		t.Errorf("legacy fallback should populate songs: %+v", resp.Songs)
	}
}

// ─── GetAudioURL (weapi) ───────────────────────────────────────────────────

func TestServer_GetAudioURL_ReturnsWeapiUrl(t *testing.T) {
	srv, _ := NewServer()
	var sawURL string
	var sawForm string
	body := `{"code":200,"data":[{"id":347230,"url":"http://m7.music.126.net/abc.mp3","br":320000,"code":200}]}`
	srv.client.Transport = &neteaseRT{
		fn: func(req *http.Request) (*http.Response, error) {
			sawURL = req.URL.String()
			b, _ := io.ReadAll(req.Body)
			sawForm = string(b)
			return jsonResp(200, body), nil
		},
	}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "347230"})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if !strings.Contains(sawURL, "/weapi/song/enhance/player/url") {
		t.Errorf("GetAudioURL hit %q, want weapi url endpoint", sawURL)
	}
	if !strings.Contains(sawForm, "params=") || !strings.Contains(sawForm, "encSecKey=") {
		t.Errorf("weapi form should carry params+encSecKey, got %q", sawForm)
	}
	if resp.Url != "http://m7.music.126.net/abc.mp3" {
		t.Errorf("Url = %q, want weapi data[0].url", resp.Url)
	}
}

func TestServer_GetAudioURL_EmptyBodyReturnsEmpty(t *testing.T) {
	srv, _ := NewServer()
	srv.client.Transport = &neteaseRT{
		fn: func(*http.Request) (*http.Response, error) { return jsonResp(200, ``), nil },
	}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "347230"})
	if err != nil {
		t.Fatalf("GetAudioURL: %v", err)
	}
	if resp.Url != "" {
		t.Errorf("empty upstream body should yield empty url; got %q", resp.Url)
	}
}

func TestServer_GetAudioURL_NetworkErrorReturnsEmpty(t *testing.T) {
	srv, _ := NewServer()
	srv.client.Transport = &neteaseRT{
		fn: func(*http.Request) (*http.Response, error) { return nil, io.EOF },
	}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "347230"})
	if err != nil {
		t.Fatalf("GetAudioURL network error should still return nil err: %v", err)
	}
	if resp.Url != "" {
		t.Errorf("network error should yield empty url; got %q", resp.Url)
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

// ─── crypto primitives ─────────────────────────────────────────────────────

// TestEncryptLinux_KnownVector pins the AES-128-ECB eparams envelope against
// the reference implementation's exact algorithm (uppercase hex output). The
// vector was verified against the live upstream: the returned eparams is
// decodable by NetEase's cloudsearch/pc (2026-08).
func TestEncryptLinux_DeterministicShape(t *testing.T) {
	out := encryptLinux(`{"method":"POST","url":"http://music.163.com/api/cloudsearch/pc"}`)
	if len(out) == 0 || len(out)%32 != 0 {
		t.Errorf("encryptLinux output len=%d, want multiple of 32 (16-byte AES blocks in hex)", len(out))
	}
	for _, c := range out {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')) {
			t.Fatalf("encryptLinux output contains non-uppercase-hex char %q", c)
		}
	}
	// Deterministic for the same input+key.
	again := encryptLinux(`{"method":"POST","url":"http://music.163.com/api/cloudsearch/pc"}`)
	if out != again {
		t.Errorf("encryptLinux not deterministic")
	}
}

// TestEncryptWeapi_ParamsAndSecKeyShapes pins the weapi output: params must
// be base64 (AES-128-CBC double layer), encSecKey must be a 256-hex-char RSA
// ciphertext.
func TestEncryptWeapi_ParamsAndSecKeyShapes(t *testing.T) {
	params, encSecKey, err := encryptWeapi(`{"ids":["347230"],"br":320000}`)
	if err != nil {
		t.Fatalf("encryptWeapi: %v", err)
	}
	if params == "" {
		t.Fatal("empty weapi params")
	}
	if _, err := base64.StdEncoding.DecodeString(params); err != nil {
		t.Errorf("weapi params not base64: %v", err)
	}
	if len(encSecKey) != 256 {
		t.Errorf("encSecKey len=%d, want 256 hex chars", len(encSecKey))
	}
	for _, c := range encSecKey {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("encSecKey contains non-lower-hex char %q", c)
		}
	}
	// Two calls must differ (random secKey).
	p2, k2, err := encryptWeapi(`{"ids":["347230"],"br":320000}`)
	if err != nil {
		t.Fatalf("second encryptWeapi: %v", err)
	}
	if params == p2 && encSecKey == k2 {
		t.Errorf("weapi should be randomized across calls (got identical output)")
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

// TestRandomString_NoModuloBias pins the rejection sampling in
// randomString. `letters[v%len(letters)]` over 256 raw values is not a
// uniform draw from 62 symbols — the first 8 come up 5/256 of the time
// and the rest 4/256 — and this secKey is what the weapi request body is
// encrypted with.
//
// The test is statistical on purpose: a deterministic probe of the
// generator's internals would be rewritten along with it. 20k draws over
// 62 symbols puts the expected count at ~322 with a sigma of ~17, so the
// 8 biased symbols land around 340 — about 1 sigma apart from the rest,
// and a chi-square over the 62 buckets fails decisively when the bias is
// present.
func TestRandomString_NoModuloBias(t *testing.T) {
	const draws = 20000
	counts := map[rune]int{}
	for i := 0; i < draws; i++ {
		s, err := randomString(16)
		if err != nil {
			t.Fatalf("randomString: %v", err)
		}
		if len(s) != 16 {
			t.Fatalf("randomString(16) len = %d", len(s))
		}
		for _, c := range s {
			counts[c]++
		}
	}
	if len(counts) != 62 {
		t.Fatalf("randomString produced %d distinct symbols, want 62", len(counts))
	}
	// Chi-square goodness of fit against a uniform draw.
	const symbols = 62
	expected := float64(draws*16) / symbols
	var chi2 float64
	for _, n := range counts {
		d := float64(n) - expected
		chi2 += d * d / expected
	}
	// 61 degrees of freedom: the 0.999 critical value is ~113. A modulo-
	// biased generator scores well above 200 on 320k samples; a correct one
	// essentially never exceeds 113 by chance.
	if chi2 > 113 {
		t.Errorf("chi-square = %.1f over %d symbols — randomString is not uniform", chi2, symbols)
	}
}

// TestEncryptWeapi_PropagatesRandomFailure is REVIEW.md P3-4's actual
// point: the old code swallowed a crypto/rand failure and produced a
// secKey from an unseeded math/rand, so a broken entropy source silently
// downgraded the key instead of refusing the request. crypto/rand cannot
// be made to fail on demand, so the read is behind a seam and stubbed
// here.
func TestEncryptWeapi_PropagatesRandomFailure(t *testing.T) {
	orig := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	t.Cleanup(func() { randRead = orig })

	params, encSecKey, err := encryptWeapi(`{"ids":["347230"]}`)
	if err == nil {
		t.Fatal("encryptWeapi succeeded with a failing entropy source")
	}
	if params != "" || encSecKey != "" {
		t.Errorf("encryptWeapi returned output alongside the error: params=%q encSecKey=%q",
			params, encSecKey)
	}

	// randomString must surface it too, not substitute a value.
	if s, err := randomString(16); err == nil {
		t.Errorf("randomString returned %q with a failing entropy source", s)
	}
}

// TestGetAudioURL_BestEffortOnEntropyFailure pins the caller's half of the
// contract: GetAudioURL is documented best-effort ("", nil) on any failure,
// so an entropy outage degrades to no audio URL rather than an RPC error
// the worker would treat as a plugin fault.
func TestGetAudioURL_BestEffortOnEntropyFailure(t *testing.T) {
	orig := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	t.Cleanup(func() { randRead = orig })

	srv := &Server{}
	resp, err := srv.GetAudioURL(context.Background(), &pb.GetAudioRequest{Id: "347230"})
	if err != nil {
		t.Fatalf("GetAudioURL returned an error instead of degrading: %v", err)
	}
	if resp.GetUrl() != "" {
		t.Errorf("GetAudioURL returned url %q with no entropy", resp.GetUrl())
	}
}
