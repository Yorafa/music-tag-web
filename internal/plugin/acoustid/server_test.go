package acoustid

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pb "go-music-tag/api/proto/tagplugin"
)

// ─── shared fixtures ───────────────────────────────────────────────────────

type acoustidRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *acoustidRT) RoundTrip(req *http.Request) (*http.Response, error) { return r.fn(req) }

func jsonResp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode:    code,
		Status:        http.StatusText(code),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

const acoustidMatchBody = `{
  "results": [
    {
      "recordings": [
        {
          "id": "rec-acoustid-1",
          "title": "Hello",
          "artists": [{"name": "Adele"}],
          "releasegroups": [{"title": "25"}]
        }
      ]
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
	if info.Name != "acoustid" {
		t.Errorf("Name = %q, want acoustid", info.Name)
	}
	if info.DisplayName != "AcoustID 音频指纹" {
		t.Errorf("DisplayName = %q, want 'AcoustID 音频指纹'", info.DisplayName)
	}
	if info.SupportsSearch {
		t.Errorf("SupportsSearch should be false (acoustid is fingerprint-only)")
	}
	if info.SupportsLyric {
		t.Errorf("SupportsLyric should be false")
	}
	if !info.SupportsId3 {
		t.Errorf("SupportsId3 should be true")
	}
}

// ─── Search (always empty — acoustic fingerprint is not a search source) ──

func TestServer_Search_AlwaysEmpty(t *testing.T) {
	srv := NewServer()
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "x", Page: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Songs) != 0 || resp.HasMore {
		t.Errorf("acoustid Search should always return empty; got %+v", resp)
	}
}

// ─── FetchLyric (always empty) ─────────────────────────────────────────────

func TestServer_FetchLyric_AlwaysEmpty(t *testing.T) {
	srv := NewServer()
	resp, _ := srv.FetchLyric(context.Background(), &pb.FetchLyricRequest{SongId: "anything"})
	if resp.Lyric != "" {
		t.Errorf("Lyric should always be empty for acoustid; got %q", resp.Lyric)
	}
}

// ─── NewServer disable path ────────────────────────────────────────────────

func TestNewServer_DisabledWhenFpcalcMissing(t *testing.T) {
	// Use an unreachable absolute path to force exec.LookPath error path.
	// Save+restore PATH to keep the test environment-independent.
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", "")
	srv := NewServer()
	if !srv.disabled {
		t.Errorf("server with empty PATH should have disabled=true; got false")
	}
}

// ─── FetchId3ByTitle: disabled / empty / network errors / happy path ───────

func TestServer_FetchId3ByTitle_DisabledReturnsEmpty(t *testing.T) {
	srv := NewServer()
	srv.disabled = true
	resp, err := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "/any/path.mp3"})
	if err != nil {
		t.Fatalf("FetchId3ByTitle: %v", err)
	}
	if len(resp.Songs) != 0 {
		t.Errorf("disabled server should yield empty; got %+v", resp.Songs)
	}
}

func TestServer_FetchId3ByTitle_EmptyTitleReturnsEmpty(t *testing.T) {
	srv := NewServer()
	// Even with fpcalc present, an empty title shouldn't spawn a subprocess.
	srv.disabled = false
	srv.fpcalc = "/bin/true" // any path; won't actually run since Title empty
	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: ""})
	if len(resp.Songs) != 0 {
		t.Errorf("empty title should yield empty; got %+v", resp.Songs)
	}
}

func TestServer_FetchId3ByTitle_NetworkErrorReturnsEmpty(t *testing.T) {
	srv := newServerWithFakeFpcalc(t, "DURATION=120\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{
		fn: func(*http.Request) (*http.Response, error) { return nil, io.EOF },
	}
	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "/any"})
	if len(resp.Songs) != 0 {
		t.Errorf("transport err should yield empty; got %+v", resp.Songs)
	}
}

func TestServer_FetchId3ByTitle_InvalidJSONReturnsEmpty(t *testing.T) {
	srv := newServerWithFakeFpcalc(t, "DURATION=120\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{
		fn: func(*http.Request) (*http.Response, error) { return jsonResp(200, `not json`), nil },
	}
	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "/any"})
	if len(resp.Songs) != 0 {
		t.Errorf("invalid json should yield empty; got %+v", resp.Songs)
	}
}

func TestServer_FetchId3ByTitle_HappyPath(t *testing.T) {
	srv := newServerWithFakeFpcalc(t, "DURATION=180\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{
		fn: func(*http.Request) (*http.Response, error) { return jsonResp(200, acoustidMatchBody), nil },
	}
	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "/music/x.mp3"})
	if len(resp.Songs) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Songs))
	}
	s := resp.Songs[0]
	if s.Id != "rec-acoustid-1" {
		t.Errorf("Id = %q, want rec-acoustid-1", s.Id)
	}
	if s.Name != "Hello" || s.Artist != "Adele" || s.Album != "25" {
		t.Errorf("song = %+v", s)
	}
}

func TestServer_FetchId3ByTitle_MultiRecordingFlattens(t *testing.T) {
	body := `{"results":[{"recordings":[{"id":"r1","title":"A","artists":[{"name":"x"}],"releasegroups":[{"title":"al"}]},{"id":"r2","title":"B","artists":[{"name":"y"}],"releasegroups":[{"title":"al2"}]}]}]}`
	srv := newServerWithFakeFpcalc(t, "DURATION=180\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{fn: func(*http.Request) (*http.Response, error) { return jsonResp(200, body), nil }}
	resp, _ := srv.FetchId3ByTitle(context.Background(), &pb.FetchId3Request{Title: "/x"})
	if len(resp.Songs) != 2 {
		t.Errorf("expected 2 recordings; got %d", len(resp.Songs))
	}
}

// ─── runFPCalc / runFPCalcText pure helpers via fake binary ───────────────

func TestRunFPCalcText_ParsesDurationAndFingerprint(t *testing.T) {
	dir := t.TempDir()
	fakePath := writeFakeFpcalc(t, dir, "DURATION=240\nFINGERPRINT=AQAAEE\n")
	fp, dur, err := runFPCalcText(fakePath, "/some/audio.mp3")
	if err != nil {
		t.Fatalf("runFPCalcText: %v", err)
	}
	if fp != "AQAAEE" {
		t.Errorf("fp = %q, want AQAAEE", fp)
	}
	if dur != 240 {
		t.Errorf("dur = %d, want 240", dur)
	}
}

func TestRunFPCalcText_NoFingerprintReturnsError(t *testing.T) {
	dir := t.TempDir()
	// Emit only DURATION → parser bails with "no fingerprint".
	fakePath := writeFakeFpcalc(t, dir, "DURATION=42\n")
	if _, _, err := runFPCalcText(fakePath, "/x"); err == nil {
		t.Error("expected error when fingerprint missing; got nil")
	}
}

func TestRunFPCalcText_NonZeroExitReturnsError(t *testing.T) {
	// The script runs `exit 1` directly — no exec redirection trick, which
	// would short-circuit on the inner script's exit code and mask the
	// non-zero exit path we want to exercise.
	bareFail := t.TempDir() + "/fail.sh"
	if err := os.WriteFile(bareFail, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fail script: %v", err)
	}
	if _, _, err := runFPCalcText(bareFail, "/x"); err == nil {
		t.Error("expected error when fpcalc exits non-zero; got nil")
	}
}

func TestRunFPCalc_JsonBranchTakesPrecedence(t *testing.T) {
	// runFPCalc tries -json first; a script that detects -json and emits
	// the JSON shape should win over the text fallback.
	dir := t.TempDir()
	jsonBody := `{"duration":123,"fingerprint":"json-branch-fp"}`
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-json\" ]; then\n" +
		"  echo '" + jsonBody + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo DURATION=999\n" +
		"echo FINGERPRINT=text-branch-fp\n"
	scriptPath := filepath.Join(dir, "fpcalc-json-aware.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	fp, dur, err := runFPCalc(scriptPath, "/any")
	if err != nil {
		t.Fatalf("runFPCalc: %v", err)
	}
	if fp != "json-branch-fp" {
		t.Errorf("fp = %q, want json-branch-fp (json path wins)", fp)
	}
	if dur != 123 {
		t.Errorf("dur = %d, want 123", dur)
	}
}

// ─── match(): the request we actually put on the wire ──────────────────────
//
// The plugin shipped with three faults stacked on top of each other, and
// every one of them looked identical from the outside: no results. That is
// why the tests above only ever assert emptiness — they pass just as well
// when the plugin is completely broken. These assert the request itself.

// The endpoint is /v2/lookup. /v2/match answers 404 with an HTML body, which
// does not even parse as JSON.
func TestMatch_TargetsTheLookupEndpoint(t *testing.T) {
	var gotPath string
	srv := newServerWithFakeFpcalc(t, "DURATION=120\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{fn: func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		return jsonResp(200, acoustidMatchBody), nil
	}}
	if _, err := srv.match(context.Background(), "AQAAAA==", 120); err != nil {
		t.Fatalf("match: %v", err)
	}
	if gotPath != "/v2/lookup" {
		t.Errorf("posted to %q, want /v2/lookup", gotPath)
	}
}

// The client key used to be a hardcoded literal that the server rejects
// outright, so every request came back {"error":{"code":4}}.
func TestMatch_SendsTheConfiguredAPIKey(t *testing.T) {
	t.Setenv(envAPIKey, "test-key-abc123")
	srv := newServerWithFakeFpcalc(t, "DURATION=120\nFINGERPRINT=AQAAAA==\n")

	var gotClient string
	srv.client.Transport = &acoustidRT{fn: func(r *http.Request) (*http.Response, error) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		gotClient = r.FormValue("client")
		return jsonResp(200, acoustidMatchBody), nil
	}}
	if _, err := srv.match(context.Background(), "AQAAAA==", 120); err != nil {
		t.Fatalf("match: %v", err)
	}
	if gotClient != "test-key-abc123" {
		t.Errorf("client = %q, want the configured key", gotClient)
	}
}

// API errors arrive as HTTP 200 with a populated error object, so a status
// check alone passes. Treating one as "no match" is what let an invalid key
// look like a library full of unrecognisable songs.
func TestMatch_APIErrorsAreErrorsNotEmptyResults(t *testing.T) {
	body := `{"status":"error","error":{"code":4,"message":"invalid API key"}}`
	srv := newServerWithFakeFpcalc(t, "DURATION=120\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}

	_, err := srv.match(context.Background(), "AQAAAA==", 120)
	if err == nil {
		t.Fatal("match() returned no error for an API error response")
	}
	if !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("error should carry the API's message; got %v", err)
	}
}

func TestMatch_NonOKStatusIsAnError(t *testing.T) {
	srv := newServerWithFakeFpcalc(t, "DURATION=120\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(503, `{}`), nil
	}}
	if _, err := srv.match(context.Background(), "AQAAAA==", 120); err == nil {
		t.Error("match() should fail on a 503")
	}
}

// Without ACOUSTID_API_KEY the shared public test key is used, and the server
// says it at construction rather than failing silently on every request.
func TestNewServer_FallsBackToTheDocumentedTestKey(t *testing.T) {
	t.Setenv(envAPIKey, "")
	srv := NewServer()
	if srv.apiKey != defaultAPIKey {
		t.Errorf("apiKey = %q, want the documented public test key", srv.apiKey)
	}
}

// One song can carry several AcoustID fingerprints (different pressings,
// different audio tracks), and they all resolve to the same MusicBrainz
// recording. Expanding results verbatim listed the track twice in the UI.
func TestMatch_CollapsesFingerprintsThatResolveToTheSameRecording(t *testing.T) {
	body := `{
	  "results": [
	    {"score": 0.99, "recordings": [{"id": "rec-1", "title": "Same Song", "artists": [{"name": "A"}], "releasegroups": [{"title": "LP"}]}]},
	    {"score": 0.96, "recordings": [{"id": "rec-1", "title": "Same Song", "artists": [{"name": "A"}], "releasegroups": [{"title": "LP"}]}]},
	    {"score": 0.80, "recordings": [{"id": "rec-2", "title": "Other Song"}]}
	  ]
	}`
	srv := newServerWithFakeFpcalc(t, "DURATION=120\nFINGERPRINT=AQAAAA==\n")
	srv.client.Transport = &acoustidRT{fn: func(*http.Request) (*http.Response, error) {
		return jsonResp(200, body), nil
	}}

	got, err := srv.match(context.Background(), "AQAAAA==", 120)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2 (rec-1 twice should collapse): %+v", len(got), got)
	}
	if got[0].RecordingID != "rec-1" || got[1].RecordingID != "rec-2" {
		t.Errorf("unexpected order/content: %+v", got)
	}
}

// ─── internal: build a Server wired to a shell-script-as-fpcalc ───────────
func newServerWithFakeFpcalc(t *testing.T, stdout string) *Server {
	t.Helper()
	dir := t.TempDir()
	scriptPath := writeFakeFpcalc(t, dir, stdout)
	srv := NewServer()
	srv.disabled = false
	srv.fpcalc = scriptPath
	return srv
}

// writeFakeFpcalc writes a portable sh script under dir that prints stdout
// (one echo per non-empty line). Returns the script path.
func writeFakeFpcalc(t *testing.T, dir, stdout string) string {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	if stdout != "" {
		for _, line := range strings.Split(stdout, "\n") {
			if line == "" {
				continue
			}
			sb.WriteString("echo ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	path := filepath.Join(dir, "fpcalc-fake.sh")
	if err := os.WriteFile(path, []byte(sb.String()), 0o755); err != nil {
		t.Fatalf("write fake fpcalc: %v", err)
	}
	// Belt + suspenders: ensure executable bit survived os.WriteFile on every
	// platform (Windows would set 0644 but we don't run there).
	_ = exec.Command("chmod", "+x", path).Run()
	return path
}
