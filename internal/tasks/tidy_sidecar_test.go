package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
)

// TestTidyOne_CarriesSidecars pins that reorganising the library moves
// each track's lyrics and album cover into the new folder with it.
//
// TidyFolder renames the audio into <first>/<second>/ while keeping the
// base name. The .lrc and the cover are separate files filed next to the
// audio, so without an explicit carry they stay in the old directory and
// the album ends up split across two folders — audio in one, artwork and
// lyrics in the other. Nothing in the library joins them back up, and the
// lyrics of a track become invisible because the lookup is by name
// relative to the audio.
func TestTidyOne_CarriesSidecars(t *testing.T) {
	root := t.TempDir()
	src := testaudio.SeedMP3(t, root, "song.mp3")
	if err := tag.Write(src, &tag.TagUpdate{
		Title:  strptr("Song"),
		Album:  strptr("MyAlbum"),
		Artist: []string{"MyArtist"},
	}); err != nil {
		t.Fatalf("seed tags: %v", err)
	}
	// The sidecars a previous save would have left beside it.
	for name, body := range map[string]string{
		"song.lrc":          "words",
		"cover-MyAlbum.jpg": "JPEGDATA",
		"cover-MyAlbum.png": "PNGDATA",
		"cover-OtherAl.jpg": "JPEGDATA",
		"album.nfo":         "album metadata",
		"notacover-Xyz.jpg": "unrelated",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	h := &TidyFolderHandler{MusicRoot: root} // DB and Bus nil: no rows, no events
	p := TidyFolderPayload{RootPath: root, FirstDir: "album"}
	if err := h.tidyOne(context.Background(), src, p); err != nil {
		t.Fatalf("tidyOne: %v", err)
	}

	dstDir := filepath.Join(root, "MyAlbum")
	if _, err := os.Stat(filepath.Join(dstDir, "song.mp3")); err != nil {
		t.Fatalf("the audio file did not move: %v", err)
	}

	// album.nfo is album-scoped metadata and belongs with the audio, so it
	// travels too. It used to be asserted as "unrelated" and left behind —
	// which is what stranded it in the old directory and stopped the pruner
	// from ever removing that directory. See internal/tag/sidecar_move.go.
	for _, name := range []string{
		"song.lrc", "cover-MyAlbum.jpg", "cover-MyAlbum.png", "cover-OtherAl.jpg", "album.nfo",
	} {
		if _, err := os.Stat(filepath.Join(dstDir, name)); err != nil {
			t.Errorf("%s did not follow the track into the new folder: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("%s was abandoned in the old folder", name)
		}
	}

	// Files that are not album metadata must be left alone.
	for _, name := range []string{"notacover-Xyz.jpg"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s was swept up by the carry: %v", name, err)
		}
	}
}

func strptr(s string) *string { return &s }
