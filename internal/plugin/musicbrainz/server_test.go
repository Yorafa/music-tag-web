package musicbrainz

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

// ─── shared fixtures ───────────────────────────────────────────────────────

type mbRT struct {
	calls  int
	bodies []string
	codes  []int
	fn     func(*http.Request) (*http.Response, error)
}

func (r *mbRT) RoundTrip(req *http.Request) (*http.Response, error) {
	idx := r.calls
	r.calls++
	if r.fn != nil {
		return r.fn(req)
	}
	if len(r.bodies) == 0 {
		return nil, io.EOF
	}
	if idx >= len(r.bodies) {
		// Don't silently re-emit the last body when the test author only
		// expected a fixed number of round-trips; that masks call-count
		// regressions (e.g. a mistakenly-activated retry path). Surface as
		// an explicit error instead.
		return nil, errors.New("mbRT: out of fixtures (call " + strconv.Itoa(idx+1) + ")")
	}
	code := 200
	if idx < len(r.codes) {
		code = r.codes[idx]
	}
	return jsonResp(code, r.bodies[idx]), nil
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

// resetThrottle zeroes the cross-goroutine throttle so a test doesn't have
// to wait 1.1s between calls. Same-package access to lastCallMu/lastCall.
func resetThrottle() {
	lastCallMu.Lock()
	lastCall = time.Time{}
	lastCallMu.Unlock()
}

const mbSearchBody = `{
  "recordings": [
    {
      "id": "rec-1",
      "title": "Hello",
      "artist-credit": [
        {"name": "Adele ", "joinphrase": "", "artist": {"id": "100"}}
      ],
      "releases": [{"title": "25"}],
      "tags": [{"name": "pop"}]
    }
  ],
  "count": 1
}`

// ─── GetPluginInfo ─────────────────────────────────────────────────────────

func TestServer_GetPluginInfo(t *testing.T) {
	srv := NewServer()
	info, err := srv.GetPluginInfo(context.Background(), &pb.PluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo: %v", err)
	}
	if info.Name != "musicbrainz" {
		t.Errorf("Name = %q, want musicbrainz", info.Name)
	}
	if info.DisplayName != "MusicBrainz" {
		t.Errorf("DisplayName = %q, want MusicBrainz", info.DisplayName)
	}
	if !info.SupportsSearch || !info.SupportsId3 {
		t.Errorf("SupportsSearch/Id3 should be true; got %+v", info)
	}
	if info.SupportsLyric {
		t.Errorf("SupportsLyric should be false (MusicBrainz has no lyric source)")
	}
}

// ─── FetchLyric (always empty) ────────────────────────────────────────────

func TestServer_FetchLyric_AlwaysEmpty(t *testing.T) {
	srv := NewServer()
	resp, err := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "rec-1"})
	if err != nil {
		t.Fatalf("FetchLyric: %v", err)
	}
	if resp.Lyric != "" {
		t.Errorf("Lyric should always be empty; got %q", resp.Lyric)
	}
}

// ─── Search ────────────────────────────────────────────────────────────────

func TestServer_Search_HappyPath(t *testing.T) {
	resetThrottle()
	srv := NewServer()
	srv.client.Transport = &mbRT{
		bodies: []string{mbSearchBody},
		codes:  []int{200},
	}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "hello", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	s := resp.Songs[0]
	if s.Id != "rec-1" {
		t.Errorf("Id = %q, want rec-1 (recording id)", s.Id)
	}
	if s.Name != "Hello" {
		t.Errorf("Name = %q, want Hello", s.Name)
	}
	if s.Artist != "Adele " {
		t.Errorf("Artist = %q, want 'Adele ' (joinphrase '' 拼接)", s.Artist)
	}
	if s.ArtistId != "100" {
		t.Errorf("ArtistId = %q, want 100", s.ArtistId)
	}
	if s.Album != "25" {
		t.Errorf("Album = %q, want 25", s.Album)
	}
	if s.Genre != "pop" {
		t.Errorf("Genre = %q, want pop (first tag)", s.Genre)
	}
	// count=1, offset=0, len=1 → 1 > 0+1 = false → HasMore=false.
	if resp.HasMore {
		t.Errorf("HasMore=true; want false (count exhausted)")
	}
}

func TestServer_Search_HasMoreWhenCountBeyondPage(t *testing.T) {
	// count=20, offset=0, returned 10 → 20 > 0+10 = true.
	body := strings.Replace(mbSearchBody, `"count": 1`, `"count": 20`, 1)
	resetThrottle()
	srv := NewServer()
	srv.client.Transport = &mbRT{bodies: []string{body}}
	resp, _ := srv.Search(context.Background(), &pb.SearchRequest{Query: "x", Page: 1, Limit: 10})
	if !resp.HasMore {
		t.Errorf("HasMore=false; want true (count>offset+len)")
	}
}

func TestServer_Search_NetworkErrorReturnsErr(t *testing.T) {
	resetThrottle()
	srv := NewServer()
	srv.client.Transport = &mbRT{fn: func(*http.Request) (*http.Response, error) { return nil, io.EOF }}
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Page: 1, Limit: 10})
	if err == nil {
		t.Errorf("transport err should propagate as gRPC err; got nil err")
	}
	if resp != nil {
		t.Errorf("transport err should leave resp nil; got %+v", resp)
	}
}

func TestServer_Search_InvalidJSONReturnsErr(t *testing.T) {
	resetThrottle()
	srv := NewServer()
	srv.client.Transport = &mbRT{fn: func(*http.Request) (*http.Response, error) {
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

func TestServer_Search_503TriggersRetryThenSucceeds(t *testing.T) {
	resetThrottle()
	// First call returns 503 (rate-limited); retry path in doSearch fires
	// once more with sleep+throttle(2s); the second call's body wins.
	srv := NewServer()
	srv.client.Transport = &mbRT{
		bodies: []string{``, mbSearchBody},
		codes:  []int{503, 200},
	}
	// Use a generous timeout — the retry sleeps 2s on 503.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	resp, err := srv.Search(ctx, &pb.SearchRequest{Query: "x", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 1 {
		t.Errorf("retry should surface 2nd-call body; got %+v", resp.Songs)
	}
}

// ─── throttle() unit ───────────────────────────────────────────────────────

func TestThrottle_AllowsAfterReset(t *testing.T) {
	resetThrottle()
	if err := throttle(context.Background()); err != nil {
		t.Errorf("throttle on fresh lastCall should be 0-wait; err=%v", err)
	}
}

func TestThrottle_CtxCancelDuringWait(t *testing.T) {
	// Saturate lastCall, then ask for a second throttle on a tiny ctx:
	// should return ctx.Err() instead of waiting 1.1s.
	resetThrottle()
	if err := throttle(context.Background()); err != nil {
		t.Fatalf("first throttle: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := throttle(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("ctx cancel during wait should propagate DeadlineExceeded; got %v", err)
	}
}
