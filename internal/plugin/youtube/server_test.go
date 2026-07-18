package youtube

import (
	"context"
	"os"
	"path/filepath"
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
	t.Setenv("YT_TMP_DIR", "/tmp/custom-ytmp")
	if got := tmpDir(); got != "/tmp/custom-ytmp" {
		t.Errorf("tmpDir() = %q, want %q (env override)", got, "/tmp/custom-ytmp")
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
