package dedup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/db"
	"go-music-tag/internal/tag"
	"go-music-tag/internal/testaudio"
)

// indexDB builds a DB holding only the music_folder table, which is the only
// table the scanners ever write to.
func indexDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gormDB.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatal(err)
	}
	return gormDB
}

// indexRow records path in music_folder the way a scan would: absolute path,
// basename as name, real byte size. fileType is a parameter because the
// file_type filter is itself under test.
func indexRow(t *testing.T, gormDB *gorm.DB, path, fileType string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	row := db.Folder{
		Name:     filepath.Base(path),
		Path:     path,
		Size:     fi.Size(),
		FileType: fileType,
		UID:      "uid-" + filepath.Base(path),
	}
	if err := gormDB.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

// emptyRoot returns a library root directory that exists but holds no files,
// so every disk-walking fallback in dedup comes back empty. It is what makes
// an index-only fixture possible: if a test passes while the candidate is
// only reachable through the index, the index is genuinely doing the work.
func emptyRoot(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "music")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestIndexSuppliesCandidatesTheDiskWalkCannotReach is the core of the fix.
//
// The four stages each prefer a DB query and fall back to walking the music
// root. The queries all read `music_track`, which nothing has ever written:
// the scanners record every path — files and folders alike — into
// `music_folder`, distinguished by file_type, so music_track has been
// permanently empty. The walk fallback is what has actually been producing
// every verdict, which hides the dead queries rather than failing them.
//
// This fixture makes the index the only route to the candidate: the Checker's
// music root is empty, so findFilesByName / findFilesBySize /
// findFilesBySizeBetween all return nothing. Only a working index finds it.
//
// It also pins path handling. music_folder.path is ABSOLUTE, while the code
// did `filepath.Join(root, rel)` on query results — producing
// <root>/tmp/.../candidate.mp3, which fails every os.Stat and reports "no
// duplicates" against a table full of them. The path is stored absolute and
// used as-is, so that mistake cannot pass unnoticed.
//
// The candidate lives INSIDE the root. It used to sit outside, which read as
// a stronger test but asserted something production never does: the scanner
// only ever indexes MUSIC_DIR, and the download cache is filtered out by
// root containment (see TestIndexIgnoresNonMusicRows). An out-of-root
// candidate is why an earlier version of the library-audio filter had to key
// on file_type, and keying on file_type is what hid downloaded tracks from
// duplicate detection.
func TestIndexSuppliesCandidatesTheDiskWalkCannotReach(t *testing.T) {
	gormDB := indexDB(t)
	root := emptyRoot(t)

	candidate := filepath.Join(root, "nested", "candidate.mp3")
	target := filepath.Join(root, "target.mp3")
	content := []byte("identical audio bytes, byte for byte")
	for _, p := range []string{candidate, target} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	indexRow(t, gormDB, candidate, "music")

	c := New(gormDB, root)
	got := c.Check(context.Background(), target, Options{})

	// Different basenames, so stage 1 cannot produce this verdict; it has to
	// be the SHA-256 stage, which is the one that blocks a write.
	if got.Verdict != VerdictDuplicate {
		t.Fatalf("Verdict = %v, want VerdictDuplicate (index not consulted)", got.Verdict)
	}
	if got.MatchField != stageHash {
		t.Errorf("MatchField = %q, want %q", got.MatchField, stageHash)
	}
	// DuplicatePath is documented as relative to MUSIC_DIR, so an in-root
	// candidate comes back relative. The absolute value is what the index
	// stores; the relative one is what the API promises.
	if want := "nested/candidate.mp3"; got.DuplicatePath != want {
		t.Errorf("DuplicatePath = %q, want %q (root-relative)", got.DuplicatePath, want)
	}
}

// TestIndexResolvesRelativeRowsToo guards the other direction: a row whose
// path is stored relative to the music root must still resolve, because the
// finder helpers report paths that way and a future index writer may store
// them so.
//
// This is a direct spec of resolveUnderRoot rather than a Check() test. A
// relative row necessarily points inside the root, which is exactly where the
// disk walk also looks — so a behavioural test would pass whether or not the
// index was consulted, and would prove nothing.
func TestIndexResolvesRelativeRowsToo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "music")
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"relative joins onto root", "Album/01.mp3", filepath.Join(root, "Album/01.mp3")},
		{"absolute is left alone", "/elsewhere/01.mp3", "/elsewhere/01.mp3"},
		{"absolute under root is not re-joined", filepath.Join(root, "a.mp3"), filepath.Join(root, "a.mp3")},
		{"empty stays empty", "", ""},
		{"dot segments are cleaned", "Album/../b.mp3", filepath.Join(root, "b.mp3")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveUnderRoot(root, tc.in); got != tc.want {
				t.Errorf("resolveUnderRoot(%q, %q) = %q, want %q", root, tc.in, got, tc.want)
			}
		})
	}

	t.Run("no root", func(t *testing.T) {
		if got := resolveUnderRoot("", "rel/a.mp3"); got != filepath.Clean("rel/a.mp3") {
			t.Errorf("with an empty root, got %q, want the input cleaned", got)
		}
	})
}

// TestIndexIgnoresNonMusicRows pins the download-cache exclusion.
//
// music_folder also holds folder rows, cover-image rows, and — this is the
// hazard — the per-source download cache, whose file is byte-identical to the
// track the user is about to tag: a copy they just downloaded on purpose.
// Flagging that as a library duplicate would report "already in your
// library" about a file that is not in the library.
//
// The exclusion is by ROOT CONTAINMENT, not by file_type. It used to be
// file_type, and that is what made the filter wrong in the other direction:
// yt_dl wrote the download source ('youtube', 'netease', …) into the same
// column the scanner fills with 'music', so filtering on 'music' also
// excluded every track that had been downloaded INTO the library.
func TestIndexIgnoresNonMusicRows(t *testing.T) {
	gormDB := indexDB(t)
	root := emptyRoot(t)

	// Identical bytes, so only the file_type filter can keep these apart.
	content := []byte("downloaded then re-tagged, byte for byte the same")
	cacheHit := filepath.Join(t.TempDir(), "audio_cache", "video.mp3")
	target := filepath.Join(t.TempDir(), "target.mp3")
	for _, p := range []string{cacheHit, target} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	indexRow(t, gormDB, cacheHit, "youtube")

	c := New(gormDB, root)
	if got := c.Check(context.Background(), target, Options{}); got.Verdict == VerdictDuplicate {
		t.Errorf("Verdict = Duplicate (MatchField %q): a youtube cache row must not "+
			"count as a library duplicate", got.MatchField)
	}
}

// TestCheckMetaFallsBackToDiskWhenIndexHasNothing covers the one stage that
// had no fallback at all.
//
// checkFilename, checkHash and checkFingerprint each degrade to a bounded
// filesystem walk when their query returns nothing. checkMeta did not: with a
// DB handle it ran its music_track query, took the empty result as the answer,
// and returned. Since music_track is always empty, the metadata stage could
// never produce a verdict — the one weak-signal stage that exists to catch
// re-tagged copies was inert.
//
// The fixture is the shape that stage exists for: the same song under two
// filenames and two album tags, so stage 1 cannot fire (names differ), stage 2
// cannot fire (bytes differ), and only the metadata comparison can pair them.
// The differing album is what makes the bytes differ; title (0.5) + artist
// (0.3) still clears the 0.7 threshold on its own.
func TestCheckMetaFallsBackToDiskWhenIndexHasNothing(t *testing.T) {
	root := t.TempDir()
	first := writeTagged(t, root, "Artist/Album One/01 - Song.mp3", "Song", "Artist", "Album One")
	second := writeTagged(t, root, "loose/Song copy.mp3", "Song", "Artist", "Album Two")

	// No index rows at all: a present-but-empty index, which is the state
	// the production library is in.
	c := New(indexDB(t), root)
	got := c.Check(context.Background(), second, Options{})

	if got.MatchField != stageMeta {
		t.Fatalf("MatchField = %q (verdict %v), want %q — the metadata stage never ran",
			got.MatchField, got.Verdict, stageMeta)
	}
	if got.Verdict != VerdictLikelyDuplicate {
		t.Errorf("Verdict = %v, want VerdictLikelyDuplicate (weak signal, must not block a write)",
			got.Verdict)
	}
	if got.DuplicatePath == "" {
		t.Error("DuplicatePath is empty; the matched candidate was not reported")
	}
	_ = first
}

// writeTagged seeds a real decodable MP3 and writes tags into it, so the
// metadata stage has something real to read back. Synthetic bytes would be
// rejected by tag.Read and the stage would see empty metadata.
func writeTagged(t *testing.T, root, rel, title, artist, album string) string {
	t.Helper()
	p := testaudio.SeedMP3(t, filepath.Join(root, filepath.FromSlash(rel)), filepath.Base(rel))
	titleCopy, artistCopy, albumCopy := title, artist, album
	err := tag.Write(p, &tag.TagUpdate{
		Title:  &titleCopy,
		Artist: []string{artistCopy},
		Album:  &albumCopy,
	})
	if err != nil {
		t.Fatalf("tag.Write %s: %v", p, err)
	}
	return p
}
