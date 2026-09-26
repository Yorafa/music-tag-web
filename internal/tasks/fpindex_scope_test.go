package tasks

import (
	"context"
	"path/filepath"
	"testing"
)

// The download cache is not the library.
//
// The indexer's filter was widened from `file_type = 'music'` to
// `file_type NOT IN ('folder','image')` because the downloader writes the
// source name there, and that change is what brought 加入库 tracks back into
// the index. It also swept in /tmp/audio_cache, where the downloader parks a
// copy of the file it just fetched, and the live table duly filled up with
// cache rows carrying durations and full 3.8KB fingerprints.
//
// Nothing reads those. Duplicate detection excludes the cache by testing
// root containment — internal/dedup/audiotable.go::underMusicRoot — so an
// indexed cache fingerprint is 0.4s of decode per file, paid on a file the
// index was never going to be asked about.
//
// Worse than the wasted decode: the cache is scratch space that changes as
// downloads happen, so every cache file reads as "indexed but stale" on the
// next run. A run that indexes anything re-arms the chain, which means the
// re-arm would wake up on cache activity forever. The whole design rests on
// the index converging and stopping, and a directory the app treats as
// disposable is the one thing guaranteed not to be stable.
//
// Both rows below carry the SAME file_type ('youtube') and the same
// extension. The only thing that separates them is whether the path is under
// the music root — which is exactly the predicate under test, and exactly
// why no file_type value could stand in for it.

// The feature: an in-library track that arrived via 加入库 is indexed, and a
// cache file with an identical row is not.
func TestFpIndex_IndexesTheLibraryAndSkipsTheDownloadCache(t *testing.T) {
	requireFpcalcForIndex(t)
	gdb := newFpIndexDB(t)

	// Two temp roots, so the cache is genuinely outside the library rather
	// than a subdirectory of it.
	library := t.TempDir()
	cache := t.TempDir()

	inLibrary := testaudioSeed(t, library, "downloaded.wav", 5)
	inCache := testaudioSeed(t, cache, "downloaded.wav", 5)
	seedFolderRow(t, gdb, inLibrary, "youtube", fileSizeOf(t, inLibrary))
	seedFolderRow(t, gdb, inCache, "youtube", fileSizeOf(t, inCache))

	h := &FpIndexHandler{DB: gdb, MusicRoot: library}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}

	if d := durationOf(t, gdb, inLibrary); d <= 0 {
		t.Errorf("library track duration = %d, want > 0", d)
	}
	if d := durationOf(t, gdb, inCache); d != 0 {
		t.Errorf("download-cache row got duration %d: it is outside the music root, "+
			"so nothing will ever query its fingerprint", d)
	}
}

// A cache file that has already been indexed must stop being re-read.
//
// This is the half that costs. staleIndexedFiles runs over every indexed
// row on every re-arm, so a cache row that keeps changing hands the chain a
// reason to wake up 30 seconds later, forever. The containment test has to
// happen before the stat, not after it.
func TestFpIndex_StopsReReadingAChangedCacheFile(t *testing.T) {
	requireFpcalcForIndex(t)
	gdb := newFpIndexDB(t)

	library := t.TempDir()
	cache := t.TempDir()
	inCache := testaudioSeed(t, cache, "cached.wav", 5)
	seedFolderRow(t, gdb, inCache, "youtube", fileSizeOf(t, inCache))

	h := &FpIndexHandler{DB: gdb, MusicRoot: library}
	// First run: skipped outright, so nothing is written and the row is
	// still "never indexed".
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("first ProcessTask: %v", err)
	}
	// A second run must reach the same conclusion without decoding, or the
	// re-arm chain never terminates.
	pending, err := h.pendingFiles()
	if err != nil {
		t.Fatalf("pendingFiles: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pendingFiles = %v, want none: a cache row is not the library's work", pending)
	}
}

// The default when no root is configured must not silently narrow to
// nothing. A test with no MusicRoot, and any deployment that has not set
// MUSIC_DIR, would otherwise index zero files and report success.
func TestFpIndex_NoRootConfiguredIndexesEverything(t *testing.T) {
	requireFpcalcForIndex(t)
	gdb := newFpIndexDB(t)
	dir := t.TempDir()
	p := testaudioSeed(t, dir, "song.wav", 5)
	seedFolderRow(t, gdb, p, "music", fileSizeOf(t, p))

	if err := (&FpIndexHandler{DB: gdb}).ProcessTask(context.Background(), Task{Type: TypeFpIndex}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if d := durationOf(t, gdb, p); d <= 0 {
		t.Errorf("duration = %d, want > 0: an unset root must not disable the index", d)
	}
}

// A path that merely shares a prefix with the root is not inside it:
// /app/media-backup is a different directory from /app/media.
func TestFpIndex_RootPrefixAloneIsNotContainment(t *testing.T) {
	gdb := newFpIndexDB(t)
	base := t.TempDir()
	library := filepath.Join(base, "media")
	sibling := filepath.Join(base, "media-backup")
	p := filepath.Join(sibling, "song.wav")
	seedFolderRow(t, gdb, p, "music", 1024)

	pending, err := (&FpIndexHandler{DB: gdb, MusicRoot: library}).pendingFiles()
	if err != nil {
		t.Fatalf("pendingFiles: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pendingFiles = %v, want none: %s is not under %s", pending, sibling, library)
	}
}
