package audiocache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCacheFile drops a file of n bytes into <root>/<source>/<name> with a
// chosen mtime. Returns its full path.
func writeCacheFile(t *testing.T, root, source, name string, n int, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, source)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	if age > 0 {
		ts := time.Now().Add(-age)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}
	return p
}

func names(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Base(f.Path))
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestRootDefaultsAndEnvOverride(t *testing.T) {
	t.Setenv(envRoot, "")
	if got := Root(); got != defaultRoot {
		t.Fatalf("unset env: got %q, want %q", got, defaultRoot)
	}
	t.Setenv(envRoot, "/srv/cache")
	if got := Root(); got != "/srv/cache" {
		t.Fatalf("set env: got %q", got)
	}
	if got := Dir("youtube"); got != filepath.Join("/srv/cache", "youtube") {
		t.Fatalf("Dir: got %q", got)
	}
	// Blank-but-present must not win over the default: an empty env var in
	// a compose file is a wiring mistake, not a request for "".
	t.Setenv(envRoot, "   ")
	if got := Root(); got != defaultRoot {
		t.Fatalf("blank env: got %q, want default", got)
	}
}

func TestInspectMissingRootIsNotAnError(t *testing.T) {
	t.Setenv(envRoot, filepath.Join(t.TempDir(), "absent"))
	st := Inspect()
	if st.Exists {
		t.Fatal("Exists should be false for a missing root")
	}
	if st.Bytes != 0 || st.Files != 0 || len(st.BySource) != 0 {
		t.Fatalf("missing root should read as empty, got %+v", st)
	}
}

func TestInspectBreaksDownBySource(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	writeCacheFile(t, root, "youtube", "a.ogg", 100, time.Hour)
	writeCacheFile(t, root, "youtube", "b.ogg", 150, time.Hour)
	writeCacheFile(t, root, "migu", "c.ogg", 50, time.Hour)

	st := Inspect()
	if st.Bytes != 300 || st.Files != 3 {
		t.Fatalf("totals: got %d bytes / %d files", st.Bytes, st.Files)
	}
	if got := st.BySource["youtube"]; got.Bytes != 250 || got.Files != 2 {
		t.Fatalf("youtube: got %+v", got)
	}
	if got := st.BySource["migu"]; got.Bytes != 50 || got.Files != 1 {
		t.Fatalf("migu: got %+v", got)
	}
}

func TestSelectUnderCapSelectsNothing(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	writeCacheFile(t, root, "youtube", "a.ogg", 100, time.Hour)

	// The cap is a ceiling, not a target: a cache below it must come back
	// untouched, or every 30 minutes the scheduler would delete the whole
	// cache of a small library.
	sel := Select(1000, 0)
	if len(sel.Files) != 0 {
		t.Fatalf("under cap should select nothing, got %v", names(sel.Files))
	}
	if sel.TotalBytes != 100 {
		t.Fatalf("TotalBytes should still report the cache size, got %d", sel.TotalBytes)
	}
}

func TestSelectOverCapTakesOldestFirst(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	writeCacheFile(t, root, "youtube", "old.ogg", 100, 72*time.Hour)
	writeCacheFile(t, root, "youtube", "mid.ogg", 100, 48*time.Hour)
	writeCacheFile(t, root, "youtube", "new.ogg", 100, 24*time.Hour)

	// 300 on disk, cap 250 → the oldest 100 has to go, and only it.
	sel := Select(250, 0)
	eq(t, names(sel.Files), []string{"old.ogg"})
	if sel.FreedBytes != 100 {
		t.Fatalf("FreedBytes: got %d", sel.FreedBytes)
	}
	if sel.TotalBytes != 300 {
		t.Fatalf("TotalBytes: got %d", sel.TotalBytes)
	}
}

func TestSelectTakesMultipleFilesToGetUnderCap(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	writeCacheFile(t, root, "youtube", "a.ogg", 100, 72*time.Hour)
	writeCacheFile(t, root, "youtube", "b.ogg", 100, 48*time.Hour)
	writeCacheFile(t, root, "youtube", "c.ogg", 100, 24*time.Hour)

	sel := Select(150, 0)
	eq(t, names(sel.Files), []string{"a.ogg", "b.ogg"})
	if sel.FreedBytes != 200 {
		t.Fatalf("FreedBytes: got %d", sel.FreedBytes)
	}
}

func TestSelectZeroLimitMeansEverything(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	writeCacheFile(t, root, "youtube", "a.ogg", 100, 72*time.Hour)
	writeCacheFile(t, root, "migu", "b.ogg", 100, 48*time.Hour)

	sel := Select(0, 0)
	if len(sel.Files) != 2 {
		t.Fatalf("limit<=0 should select every eligible file, got %v", names(sel.Files))
	}
}

// The guard that stops the pruner from deleting the track someone is
// listening to, or the download a task is about to copy into the library.
func TestSelectSkipsRecentFilesWhateverTheCap(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	writeCacheFile(t, root, "youtube", "stale.ogg", 100, 72*time.Hour)
	writeCacheFile(t, root, "youtube", "playing.ogg", 100, 2*time.Minute)

	sel := Select(1, 30*time.Minute)
	eq(t, names(sel.Files), []string{"stale.ogg"})
	if sel.RecentFiles != 1 || sel.RecentBytes != 100 {
		t.Fatalf("recent files should be reported, not silently dropped: %+v", sel)
	}
}

// When everything in the cache is recent the honest outcome is "I left it
// alone", not "now under cap" — the cap is allowed to be breached.
func TestSelectAllRecentLeavesCacheOverCap(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	writeCacheFile(t, root, "youtube", "a.ogg", 100, time.Minute)
	writeCacheFile(t, root, "youtube", "b.ogg", 100, 2*time.Minute)

	sel := Select(1, 30*time.Minute)
	if len(sel.Files) != 0 {
		t.Fatalf("expected nothing selected, got %v", names(sel.Files))
	}
	if sel.TotalBytes != 200 {
		t.Fatalf("TotalBytes should still be 200, got %d", sel.TotalBytes)
	}
	if sel.RecentFiles != 2 {
		t.Fatalf("RecentFiles: got %d", sel.RecentFiles)
	}
}

// Two runs over an unchanged directory must produce the same plan, or
// "which files did it keep" is unexplainable.
func TestSelectOrderIsStableForEqualMtimes(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	ts := time.Now().Add(-72 * time.Hour)
	for _, n := range []string{"c.ogg", "a.ogg", "b.ogg"} {
		p := writeCacheFile(t, root, "youtube", n, 100, 0)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	first := names(Select(150, 0).Files)
	second := names(Select(150, 0).Files)
	eq(t, first, second)
	eq(t, first, []string{"a.ogg", "b.ogg"})
}

func TestRemoveDeletesAndReports(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	a := writeCacheFile(t, root, "youtube", "a.ogg", 100, time.Hour)
	b := writeCacheFile(t, root, "youtube", "b.ogg", 200, time.Hour)

	res := Remove([]string{a, b})
	if res.Removed != 2 || res.FreedBytes != 300 || len(res.Failed) != 0 {
		t.Fatalf("unexpected result %+v", res)
	}
	for _, p := range []string{a, b} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone, stat err = %v", p, err)
		}
	}
}

func TestRemoveRefusesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	outside := filepath.Join(t.TempDir(), "precious.ogg")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The escape hatch a caller could otherwise reach by handing Remove a
	// path with a ".." in it. This is the check that keeps that from being
	// an rm -rf with extra steps.
	escape := filepath.Join(root, "youtube", "..", "..", "precious.ogg")

	for _, p := range []string{escape, filepath.Join(root, "loose.ogg"), filepath.Join(root, "youtube", "deep", "x.ogg")} {
		res := Remove([]string{p})
		if res.Removed != 0 {
			t.Fatalf("%s should be refused, got %+v", p, res)
		}
		if _, ok := res.Failed[p]; !ok {
			t.Fatalf("%s should be reported as failed, got %+v", p, res.Failed)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("the file outside the root must survive: %v", err)
	}
}

func TestRemoveRefusesSymlinkAndDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	target := filepath.Join(t.TempDir(), "target.ogg")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "youtube")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.ogg")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	res := Remove([]string{link, dir})
	if res.Removed != 0 || len(res.Failed) != 2 {
		t.Fatalf("expected both refused, got %+v", res)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target must survive: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("source directory must survive: %v", err)
	}
}

func TestRemoveReportsMissingFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envRoot, root)
	if err := os.MkdirAll(filepath.Join(root, "youtube"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "youtube", "gone.ogg")
	res := Remove([]string{missing})
	if res.Removed != 0 || res.Failed[missing] == "" {
		t.Fatalf("missing file should be reported as failed, got %+v", res)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{2 * 1024 * 1024 * 1024, "2.0 GiB"},
		{3 * 1024 * 1024 * 1024 * 1024, "3.0 TiB"},
	}
	for _, c := range cases {
		if got := FormatBytes(c.in); got != c.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
