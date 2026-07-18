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
