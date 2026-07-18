package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSafeJoin_HappyRelative(t *testing.T) {
	got, err := SafeJoin("/app/media", "Artist1/Album1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := filepath.Clean("/app/media/Artist1/Album1")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSafeJoin_AbsolutePathIsTreatedRelative(t *testing.T) {
	// filepath.Join 把前导 "/" 当作路径分隔符，结果仍落在 root 之内。
	got, err := SafeJoin("/app/media", "/etc/passwd")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.HasPrefix(got, "/app/media/") {
		t.Errorf("got %q, want prefix /app/media/", got)
	}
}

func TestSafeJoin_TraversalRejected(t *testing.T) {
	cases := []string{
		"../../etc/passwd",
		"../../../etc/passwd",
		"foo/../../../../etc/passwd",
	}
	for _, c := range cases {
		_, err := SafeJoin("/app/media", c)
		if err == nil {
			t.Errorf("SafeJoin(%q): expected error, got nil", c)
		}
	}
}

func TestSafeJoin_RejectsEmptyRoot(t *testing.T) {
	// root=empty is still a hard error: callers always pass utils.MusicRoot()
	// (一个非空绝对路径) 经过同一 env-var lookup，root 为空只可能是误用。
	if _, err := SafeJoin("", "foo"); err == nil {
		t.Error("empty root: expected error")
	}
}

func TestSafeJoin_EmptyPathReturnsRoot(t *testing.T) {
	// 与 handler/file.go::FileList 的 "operate-on-root" 文档一致
	// 空 p 应返回 cleanedRoot，而非错误。让 FileList 可以直接
	// SafeJoin(MusicRoot, req.FilePath)（req.FilePath 非 required），
	// 由 os.ReadDir(rooted) 列根目录，不必在 handler 里重复一遍
	// empty→root 的 fallback dance。
	got, err := SafeJoin("/app/media", "")
	if err != nil {
		t.Fatalf("empty path: expected nil err (operate-on-root sentinel), got %v", err)
	}
	if want := filepath.Clean("/app/media"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// 安全断言：越界检查没有因空路径分支被跳过。
	if _, err := SafeJoin("/app/media", "../etc"); err == nil {
		t.Error("traversal from root parent: still expected error")
	}
}

func TestSafeJoin_AllowsExactRoot(t *testing.T) {
	// 与 root 自身相等是允许的 (用于 "dir == root" 边界条件)。
	got, err := SafeJoin("/app/media", ".")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "/app/media" {
		t.Errorf("got %q, want /app/media", got)
	}
}

// ─── SafeAbs (P1.5 issue F — H3) ───────────────────────────────────────────

func TestSafeAbs_AcceptsAbsoluteInside(t *testing.T) {
	// Both root and absPath are absolute. absPath must clean to root or
	// a child-of-root. Trailing-separator on root is normalised away.
	cases := []struct{ root, absPath, want string }{
		{"/app/media", "/app/media", "/app/media"},
		{"/app/media", "/app/media/foo.mp3", "/app/media/foo.mp3"},
		{"/app/media/", "/app/media/bar.png", "/app/media/bar.png"},
		{"/app/media", "/app/media/deep/nested/a.png", "/app/media/deep/nested/a.png"},
	}
	for _, c := range cases {
		got, err := SafeAbs(c.root, c.absPath)
		if err != nil {
			t.Errorf("SafeAbs(%q, %q): %v", c.root, c.absPath, err)
			continue
		}
		if got != c.want {
			t.Errorf("SafeAbs(%q, %q) = %q, want %q", c.root, c.absPath, got, c.want)
		}
	}
}

func TestSafeAbs_RejectsEscapeAndRelative(t *testing.T) {
	cases := []struct {
		root, absPath string
	}{
		{"/app/media", "/etc/passwd"},             // absolute outside
		{"/app/media", "/app/mediaX/foo"},         // sibling-prefix trap (must reject)
		{"/app/media", "/app/media-evil/foo.mp3"}, // dash-suffix bypass
		{"/app/media", "relative/foo"},            // not absolute (SafeAbs requires absolute)
		{"/app/media", ""},                        // empty
		{"", "/app/media/foo"},                    // empty root
		{"/app/media", "/app/media/../../etc"},    // collapses to /etc — must reject
	}
	for _, c := range cases {
		_, err := SafeAbs(c.root, c.absPath)
		if err == nil {
			t.Errorf("SafeAbs(%q, %q): expected error, got nil", c.root, c.absPath)
		}
	}
}

func TestSafeAbs_TolerateCwdRelativeRoot(t *testing.T) {
	// Skip on Windows where filepath.Abs behaves differently.
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only relative-root test")
	}
	// A relative root like "." resolves to the cwd absolute path; the
	// candidate next to cwd has to clean to a path under that cwd.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := SafeAbs(".", filepath.Join(cwd, "relative-root-file"))
	if err != nil {
		t.Fatalf("SafeAbs('.', %q): %v", filepath.Join(cwd, "relative-root-file"), err)
	}
	if !strings.HasSuffix(got, "/relative-root-file") {
		t.Errorf("got %q, want suffix /relative-root-file", got)
	}
}
