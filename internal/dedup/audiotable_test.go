package dedup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go-music-tag/internal/audioext"
)

// The bug this file exists for.
//
// yt_dl used to write the download source into music_folder.file_type —
// "youtube", "netease", … — while the folder scanner wrote "music" for the
// same kind of file. Every query that identified library audio by
// `file_type = 'music'` therefore excluded every track that arrived through
// 加入库:
//
//   - the duration indexer skipped it, so it got no duration;
//   - durationCandidates filters `duration > 0`, so it was never a
//     candidate for anyone else's fingerprint comparison;
//   - so a re-encoded twin of a downloaded track was never detected, with
//     nothing logged, because from each query's point of view the row was
//     simply not there.
//
// The fix classifies by path extension (audioext) and by root containment,
// neither of which depends on which writer produced the row.

func TestAudioRowFileTypeClassifiesByExtension(t *testing.T) {
	// A library audio extension is 'music' regardless of where it came
	// from — the download source is recorded in TaskRecord.source, the
	// audit log, and the cache path.
	if got := audioext.FileTypeForRow("/app/media/Artist/track.mp3"); got != "music" {
		t.Errorf("mp3 file_type = %q, want music", got)
	}
	if got := audioext.FileTypeForRow("/app/media/Artist/track.FLAC"); got != "music" {
		t.Errorf("uppercase .FLAC file_type = %q, want music (case-insensitive)", got)
	}
	// A container we serve but would not keep as a library track must not
	// be labelled 'music', or the duration indexer would spend 0.4s per
	// file proving fpcalc cannot use it.
	if got := audioext.FileTypeForRow("/tmp/audio_cache/youtube/clip.webm"); got == "music" {
		t.Errorf("webm file_type = music, want a non-music value")
	}
	if got := audioext.FileTypeForRow("/app/media/Artist/cover.jpg"); got == "music" {
		t.Errorf("cover file_type = music, want a non-music value")
	}
}

func TestUnderMusicRoot(t *testing.T) {
	root := "/app/media"
	for _, tc := range []struct {
		name string
		root string
		path string
		want bool
	}{
		{"directly inside", root, "/app/media/a.mp3", true},
		{"nested inside", root, "/app/media/A/B/a.mp3", true},
		{"the root itself", root, "/app/media", true},
		{"download cache is outside", root, "/tmp/audio_cache/youtube/a.mp3", false},
		{"sibling dir is outside", root, "/app/media-other/a.mp3", false},
		{"parent is outside", root, "/app/a.mp3", false},
		{"traversal is outside", root, "/app/media/../etc/passwd", false},
		// With no configured root there is nothing to be inside of. Every
		// stage's disk-walk fallback has the same blindness, so refusing
		// all rows here would silently disable the index rather than
		// narrow it.
		{"no root accepts anything", "", "/anywhere/a.mp3", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := underMusicRoot(tc.root, tc.path); got != tc.want {
				t.Errorf("underMusicRoot(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
			}
		})
	}
}

// TestDownloadedTrackInLibraryIsACandidate is the regression test for the
// whole bug: a track inside MUSIC_DIR whose file_type was written by the
// downloader must still be found by the index.
//
// The fixture writes the OLD file_type ('youtube') on purpose. Production
// rows written before this fix carry exactly that value, and they are still
// in every existing database — AutoMigrate does not rewrite them. A fix that
// only changed the writer would leave the existing library invisible.
func TestDownloadedTrackInLibraryIsACandidate(t *testing.T) {
	gormDB := indexDB(t)
	root := emptyRoot(t)

	candidate := filepath.Join(root, "downloaded.mp3")
	target := filepath.Join(root, "target.mp3")
	content := []byte("downloaded then re-encoded, byte for byte the same")
	for _, p := range []string{candidate, target} {
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A pre-fix row: the downloader wrote the source, not the kind.
	indexRow(t, gormDB, candidate, "youtube")

	c := New(gormDB, root)
	got := c.Check(context.Background(), target, Options{})

	if got.Verdict != VerdictDuplicate {
		t.Fatalf("Verdict = %v (match %q), want Duplicate — a downloaded track inside "+
			"MUSIC_DIR must be findable regardless of which writer created its index row",
			got.Verdict, got.MatchField)
	}
	if got.MatchField != stageHash {
		t.Errorf("MatchField = %q, want %q", got.MatchField, stageHash)
	}
}

// TestDownloadCacheRowIsNotACandidate is the other direction, and it is the
// one that must keep working: the per-source cache is outside MUSIC_DIR, so
// it is not a library duplicate no matter what its file_type says.
func TestDownloadCacheRowIsNotACandidate(t *testing.T) {
	gormDB := indexDB(t)
	root := emptyRoot(t)

	cacheHit := filepath.Join(t.TempDir(), "audio_cache", "youtube", "vid.mp3")
	target := filepath.Join(root, "target.mp3")
	content := []byte("the cache copy and the library copy are the same bytes")
	for _, p := range []string{cacheHit, target} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	indexRow(t, gormDB, cacheHit, "music")

	c := New(gormDB, root)
	if got := c.Check(context.Background(), target, Options{}); got.Verdict == VerdictDuplicate {
		t.Errorf("Verdict = Duplicate (match %q): a download-cache row is not in "+
			"MUSIC_DIR and must not count as a library duplicate", got.MatchField)
	}
}

// TestLibraryAudioRowsSkipsCoversAndLyrics: the extension half of the
// classification, which the SQL cannot express without a fourth copy of the
// audioext list.
func TestLibraryAudioRowsSkipsCoversAndLyrics(t *testing.T) {
	gormDB := indexDB(t)
	root := emptyRoot(t)

	audio := filepath.Join(root, "song.mp3")
	for _, p := range []string{audio} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// cover.jpg and track.lrc: both "not a folder, not an image, size > 0"
	// for at least one of them, and neither is a duplicate candidate.
	cover := filepath.Join(root, "cover.jpg")
	lyric := filepath.Join(root, "song.lrc")
	for _, p := range []string{cover, lyric} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	indexRow(t, gormDB, audio, "music")
	indexRow(t, gormDB, cover, "image")
	indexRow(t, gormDB, lyric, "youtube")

	c := New(gormDB, root)
	got := c.libraryFiles(10, "1 = 1")

	if len(got) != 1 || got[0] != audio {
		t.Errorf("libraryFiles = %v, want just %q (a .jpg and a .lrc are not audio)",
			got, audio)
	}
}
