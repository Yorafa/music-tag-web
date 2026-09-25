package youtube

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "go-music-tag/api/proto/tagplugin"
)

func TestValidYouTubeID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"canonical 11-char", "dQw4w9WgXcQ", true},
		{"with dashes", "abc-def-1234", true},
		{"with underscore", "abc_def_g123", true},
		{"mixed case", "AbCdEfGh123", true},
		{"with dashes (real YouTube charset permits)", "abc-def-1234", true},
		{"min-length 6", "abcdef", true},
		{"max-length 32", "abcdef01234567890123456789012345", true},
		{"empty", "", false},
		{"too short 5", "abcde", false},
		{"too long 33", "abcdef012345678901234567890123456", false},
		{"has space", "abc def", false},
		{"has slash", "abc/def", false},
		{"has colon", "abc:def", false},
		{"has dot", "abc.def", false},
		{"url-looking", "https://evil.com?v=abc", false},
		{"single quote", "abc'def", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validYouTubeID(c.in); got != c.want {
				t.Errorf("validYouTubeID(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestFirstLine(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"single line, no newline", "single line no newline", "single line no newline"},
		{"first line of multi-line string", "first\nsecond\nthird", "first"},
		{"empty string", "", ""},
		{"just a newline", "\n", ""},
		{"three lines", "a\nb\nc", "a"},
		{"trailing newline", "trailing newline\n", "trailing newline"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := firstLine(c.in); got != c.want {
				t.Errorf("firstLine(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestFindLandedFile(t *testing.T) {
	dir := t.TempDir()
	id := "dQw4w9WgXcQ"

	// 0 files → error
	if _, _, err := findLandedFile(dir, id); err == nil {
		t.Error("findLandedFile with no matching files should error; got nil")
	}

	// 1 matching file → found
	want := id + ".mp3"
	mustWrite(t, filepath.Join(dir, want), []byte("fake"))
	gotPath, gotName, err := findLandedFile(dir, id)
	if err != nil {
		t.Fatalf("expected to find %s, got error: %v", want, err)
	}
	if gotName != want {
		t.Errorf("name = %q, want %q", gotName, want)
	}
	if filepath.Base(gotPath) != want {
		t.Errorf("path basename = %q, want %q", filepath.Base(gotPath), want)
	}

	// 2 matching files, latest wins (use os.Chtimes to force ordering —
	// tempdir FS mtime resolution is implementation-defined and may
	// otherwise tie).
	want2 := id + ".m4a"
	mustWrite(t, filepath.Join(dir, want2), []byte("also fake"))
	// Use a fixed future anchor as the second-file mtime so ordering is
	// deterministic across wall-clock skew / coarser-mtime filesystems
	// (FAT/exFAT/HFS+). 2_000_000_000 s = 2033-05-18, always later than
	// any plausible os.WriteFile timestamp from os.WriteFile on tmpfs.
	later := time.Unix(2_000_000_000, 0)
	if err := os.Chtimes(filepath.Join(dir, want2), later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	_, gotName2, err := findLandedFile(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if gotName2 != want2 {
		t.Errorf("latest-of-two name = %q, want %q", gotName2, want2)
	}

	// Unrelated file is ignored.
	mustWrite(t, filepath.Join(dir, "other.mp3"), []byte("irrelevant"))
	_, gotName3, err := findLandedFile(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if gotName3 != want2 {
		t.Errorf("with unrelated file, still found %q (want %q)", gotName3, want2)
	}
}

func TestNewServer_DefaultAndOverride(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(WithYTDLPPath("/abs/yt-dlp"), WithWorkDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if s.ytdlpPath != "/abs/yt-dlp" {
		t.Errorf("ytdlpPath = %q, want %q", s.ytdlpPath, "/abs/yt-dlp")
	}
	if s.workDir != dir {
		t.Errorf("workDir = %q, want %q", s.workDir, dir)
	}
	if s.searchTimeout <= 0 || s.downloadTimeout <= 0 {
		t.Errorf("timeouts should be set to non-zero defaults; got search=%v download=%v",
			s.searchTimeout, s.downloadTimeout)
	}
}

func TestTmpDir_EnvOverride(t *testing.T) {
	// AUDIO_CACHE_DIR is the new UV — backend's audioCacheDir(source)
	// returns <AUDIO_CACHE_DIR>/<source>, so for the youtube plugin we
	// expect <AUDIO_CACHE_DIR>/youtube here. Test pins that lockstep.
	t.Setenv("AUDIO_CACHE_DIR", "/tmp/custom-cache-root")
	if got := tmpDir(); got != "/tmp/custom-cache-root/youtube" {
		t.Errorf("tmpDir() = %q, want %q (AUDIO_CACHE_DIR override)", got, "/tmp/custom-cache-root/youtube")
	}
}

func TestTmpDir_Default(t *testing.T) {
	// Clear both env vars to assert the no-config default. Has to run
	// with t.Setenv so parallel tests don't trip over each other.
	t.Setenv("AUDIO_CACHE_DIR", "")
	if got := tmpDir(); got != "/tmp/audio_cache/youtube" {
		t.Errorf("tmpDir() = %q, want %q (no-env default)", got, "/tmp/audio_cache/youtube")
	}
}

func TestGetPluginInfo_Contract(t *testing.T) {
	s, err := NewServer(WithWorkDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.GetPluginInfo(context.Background(), &pb.DownloadPluginInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Name != "youtube" {
		t.Errorf("Name = %q, want %q", resp.Name, "youtube")
	}
	if resp.DisplayName != "YouTube" {
		t.Errorf("DisplayName = %q, want %q", resp.DisplayName, "YouTube")
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeYtdlpScript returns a self-contained fake yt-dlp that:
//   - dumps its full argv (one arg per line) to $YTDLP_ARGS_FILE, and
//   - resolves the -o output template + the trailing video URL, then
//     writes a fake <id>.mp3 into the template dir, exiting 0.
//
// mode "fail" makes it exit 1 with a stderr message instead, to exercise
// the failure/error-sentinel path.
func fakeYtdlpScript(mode string) string {
	if mode == "fail" {
		return `#!/bin/sh
printf 'ERROR: signed URL expired\n' >&2
exit 1
`
	}
	return `#!/bin/sh
printf '%s\n' "$@" > "$YTDLP_ARGS_FILE"
prev=""
out=""
url=""
for a in "$@"; do
    [ "$prev" = "-o" ] && out="$a"
    url="$a"
    prev="$a"
done
[ -n "$out" ] || exit 1
dir=$(dirname "$out")
mkdir -p "$dir"
vid=$(printf '%s' "$url" | sed -E 's/.*v=([A-Za-z0-9_-]+).*/\1/')
outpath=$(printf '%s' "$out" | sed -E "s/%\(id\)s/${vid}/g; s/%\(ext\)s/mp3/g")
printf 'FAKE-AUDIO' > "$outpath"
exit 0
`
}

// installFakeYtdlp writes the fake script to disk and returns a Server
// pointed at it, with an isolated workDir and a fresh argv capture file.
func installFakeYtdlp(t *testing.T, mode string) (*Server, string, string) {
	t.Helper()
	script := fakeYtdlpScript(mode)
	bin := filepath.Join(t.TempDir(), "fake-yt-dlp")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("YTDLP_ARGS_FILE", argsFile)
	workDir := t.TempDir()
	s, err := NewServer(WithYTDLPPath(bin), WithWorkDir(workDir))
	if err != nil {
		t.Fatal(err)
	}
	return s, argsFile, workDir
}

func readArgsFile(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestDownload_TranscodeArgs pins the full yt-dlp argv the plugin builds
// for a transcode request (output_format=mp3 + quality + format) — the
// contract the worker relies on when it forwards DownloadOptions.
func TestDownload_TranscodeArgs(t *testing.T) {
	s, argsFile, _ := installFakeYtdlp(t, "ok")
	const id = "dQw4w9WgXcQ"
	resp, err := s.Download(context.Background(), &pb.DownloadRequest{
		VideoId:      id,
		Format:       "bestaudio[height<=480]",
		OutputFormat: "mp3",
		Quality:      "320",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("Download failed: %s", resp.Error)
	}
	if filepath.Base(resp.FilePath) != id+".mp3" {
		t.Errorf("FilePath = %q, want basename %q", resp.FilePath, id+".mp3")
	}
	if resp.FileName != id+".mp3" {
		t.Errorf("FileName = %q, want %q", resp.FileName, id+".mp3")
	}

	args := readArgsFile(t, argsFile)
	for _, want := range []string{
		"--no-playlist", "--no-progress", "--newline",
		"--retries", "3", "--extractor-retries", "3",
		"--extractor-args", "youtube:player_client=default",
		"-f", "bestaudio[height<=480]",
		"--extract-audio", "--audio-format", "mp3", "--audio-quality", "320K",
		"--", "https://www.youtube.com/watch?v=" + id,
	} {
		if !hasArg(args, want) {
			t.Errorf("argv missing %q; got %v", want, args)
		}
	}
	if hasArg(args, "--js-runtimes") {
		t.Errorf("argv should not contain --js-runtimes (deno absent in test env)")
	}
}

// TestDownload_DefaultNoTranscode pins that an empty output_format keeps the
// original container — no --extract-audio flag, and -f defaults to
// bestaudio/best.
func TestDownload_DefaultNoTranscode(t *testing.T) {
	s, argsFile, _ := installFakeYtdlp(t, "ok")
	resp, err := s.Download(context.Background(), &pb.DownloadRequest{VideoId: "dQw4w9WgXcQ"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("Download failed: %s", resp.Error)
	}
	args := readArgsFile(t, argsFile)
	if hasArg(args, "--extract-audio") {
		t.Errorf("argv should not contain --extract-audio for empty output_format; got %v", args)
	}
	if !hasArg(args, "-f") || !hasArg(args, "bestaudio/best") {
		t.Errorf("argv should default to -f bestaudio/best; got %v", args)
	}
}

// TestDownload_RejectsUnsafeKnobs pins that format / output_format / quality
// are sanitized at the plugin boundary — an injection attempt is refused
// before any exec, with a clean failure envelope.
func TestDownload_RejectsUnsafeKnobs(t *testing.T) {
	cases := []struct {
		name string
		req  *pb.DownloadRequest
	}{
		{"format flag injection", &pb.DownloadRequest{VideoId: "dQw4w9WgXcQ", Format: "--exec=rm -rf /"}},
		{"output_format not in enum", &pb.DownloadRequest{VideoId: "dQw4w9WgXcQ", OutputFormat: "exe"}},
		{"quality not numeric", &pb.DownloadRequest{VideoId: "dQw4w9WgXcQ", Quality: "1e6;rm"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _, _ := installFakeYtdlp(t, "ok")
			resp, err := s.Download(context.Background(), c.req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.Success {
				t.Errorf("expected sanitize rejection, got Success")
			}
			if resp.Error == "" {
				t.Error("expected a non-empty error message")
			}
		})
	}
}

// TestDownload_FailureWritesErrorMarker pins the <id>.error sentinel
// contract: a failed yt-dlp run writes <id>.error into workDir so the
// gateway's /api/stream long-poll fails closed instead of looping 202.
func TestDownload_FailureWritesErrorMarker(t *testing.T) {
	s, _, workDir := installFakeYtdlp(t, "fail")
	const id = "dQw4w9WgXcQ"
	resp, err := s.Download(context.Background(), &pb.DownloadRequest{VideoId: id})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Error("expected failure, got Success")
	}
	marker := filepath.Join(workDir, id+".error")
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read error marker %q: %v", marker, err)
	}
	if !strings.Contains(string(data), "signed URL expired") {
		t.Errorf("error marker content = %q, want it to carry the yt-dlp reason", string(data))
	}
}

// TestDownload_SuccessClearsStaleErrorMarker pins that a successful
// re-download removes a previously-written <id>.error (mirrors the worker's
// failDownload/clear contract).
func TestDownload_SuccessClearsStaleErrorMarker(t *testing.T) {
	s, _, workDir := installFakeYtdlp(t, "ok")
	const id = "dQw4w9WgXcQ"
	marker := filepath.Join(workDir, id+".error")
	mustWrite(t, marker, []byte("stale failure"))
	resp, err := s.Download(context.Background(), &pb.DownloadRequest{VideoId: id})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("Download failed: %s", resp.Error)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("stale error marker should be removed after success, stat err=%v", err)
	}
}
