package tag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandleSidecarsRejectsTraversalAlbum covers P1.5 issue F — H1:
// HandleSidecars must refuse any sc.Album value whose sanitised form is
// "." / ".." / leading-dot, and never produce a cover file outside the
// audio file's parent dir. We supply a tiny GIF body in upd.AlbumImg so
// the cover-write branch runs without depending on real ID3 parsing.
func TestHandleSidecarsRejectsTraversalAlbum(t *testing.T) {
	tmp := t.TempDir()
	audioPath := filepath.Join(tmp, "song.mp3")
	if err := os.WriteFile(audioPath, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Tiny PNG-like bytes — http.DetectContentType classifies unknown
	// bytes as application/octet-stream, which MimeToExt maps to "jpeg".
	// Either way, the write MUST fail before it reaches os.WriteFile.
	coverBytes := []byte("\x89PNG\r\n\x1a\ndummy-cover-bytes")
	upd := &TagUpdate{AlbumImg: coverBytes}

	cases := []struct{ album string }{
		{"../../etc/passwd"},
		{"../../../tmp/x"},
		{".."},
		{".hidden"},
	}
	for _, c := range cases {
		sc := &WriteSidecar{SaveAlbumCover: true, Album: c.album}
		_, err := HandleSidecars(audioPath, upd, sc)
		if err == nil {
			t.Errorf("album=%q must produce error", c.album)
			continue
		}
		if !strings.Contains(err.Error(), "unsafe album name") {
			t.Errorf("album=%q: error %v must mention 'unsafe album name'", c.album, err)
		}
	}
	// No cover file should have been written anywhere in tmp.
	entries, _ := os.ReadDir(tmp)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cover-") {
			t.Fatalf("album traversal leaked file: %s/%s", tmp, e.Name())
		}
	}
}

func TestHandleSidecars_LegitAlbumStillWorks(t *testing.T) {
	// Sanity: a clean album name with a working cover image MUST write
	// the sidecar inside dir (this proves the rejection logic isn't
	// overzealous).
	tmp := t.TempDir()
	audioPath := filepath.Join(tmp, "song.mp3")
	if err := os.WriteFile(audioPath, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	upd := &TagUpdate{AlbumImg: []byte("\x89PNG\r\n\x1a\ncover-bytes")}
	sc := &WriteSidecar{SaveAlbumCover: true, Album: "My Album 2026"}
	res, err := HandleSidecars(audioPath, upd, sc)
	if err != nil {
		t.Fatalf("legit album should succeed, got %v", err)
	}
	if res.SidecarCover == "" {
		t.Fatal("legit album should report SidecarCover")
	}
	if !strings.HasPrefix(res.SidecarCover, tmp) {
		t.Fatalf("cover path %q should sit under tmp %q", res.SidecarCover, tmp)
	}
}
