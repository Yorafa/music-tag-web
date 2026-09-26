// Wiring for duplicate detection, and keeping the index it reads fresh.
//
// Two things lived outside this file and are what made dedup dead weight:
//
//   - dedup.Checker was never constructed. SetDedupChecker existed with zero
//     callers, so dedupChecker was permanently nil and runDedupCheck returned
//     on its first line. The four-stage funnel in internal/dedup had never run
//     against a real library.
//
//   - The index the funnel queries (music_folder) was only
//     populated by a 全盘扫描 button. With the button gone, the gateway has to
//     maintain it itself, or the meta stage finds nothing and the hash stage
//     falls back to walking the whole library per check.
//
// So: the checker is built from the gateway's own DB handle, and before a
// directory is written into we scan that directory once per process. Dedup
// still works with an empty index — checkHash falls back to a size-filtered
// disk walk — so a scan failure degrades performance, never correctness.

package handler

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"

	"gorm.io/gorm"

	"go-music-tag/internal/dedup"
	"go-music-tag/internal/tasks"
	"go-music-tag/internal/utils"
)

var (
	// dedupScanOnce guards lazy construction of both the checker and the
	// scanner, which need the same DB handle.
	dedupWireOnce sync.Once

	dedupWireChecker *dedup.Checker
	dedupWireScanner *tasks.UpdateScanHandler

	// scannedDirs remembers directories already walked by this process, so
	// a 40-track batch in one album scans once rather than 40 times. It is
	// deliberately not invalidated: a write does not add or remove files,
	// it only changes tags, and the dedup stages that matter (hash,
	// fingerprint) read the filesystem for content regardless.
	//
	// The one thing it will drift on is a file added to the library after
	// the first scan of its directory. That is bounded: the next gateway
	// restart, or the first write into any other directory, picks it up,
	// and until then the hash stage still finds it via its disk fallback.
	scannedDirs sync.Map
)

// ensureDedupWire builds the checker and the scanner once, from the handle
// the gateway already opened. A nil DB is not an error: dedup degrades to
// its filesystem stages, which is slower but still correct, and the gateway
// is documented to run without a DB at all.
func ensureDedupWire() (*dedup.Checker, *tasks.UpdateScanHandler) {
	dedupWireOnce.Do(func() {
		var gdb *gorm.DB = dedupDB
		dedupWireChecker = dedup.New(gdb, "")
		dedupWireScanner = &tasks.UpdateScanHandler{DB: gdb}
	})
	return dedupWireChecker, dedupWireScanner
}

// dedupDB is the handle the gateway hands over at boot. cmd/gateway sets it
// with SetDedupDB once the DB is open; it stays nil in env-only mode.
var dedupDB *gorm.DB

// SetDedupDB supplies the DB handle dedup and the scanner need. Called by
// cmd/gateway after db.Open, and deliberately optional.
func SetDedupDB(d *gorm.DB) { dedupDB = d }

// refreshIndexForDir scans dir's subtree into the music_folder index, at
// most once per directory per process. Errors are logged and swallowed: an
// unwritten index costs dedup some speed, never a verdict — every dedup stage
// falls back to a bounded filesystem walk.
func refreshIndexForDir(ctx context.Context, filePath string) {
	_, scanner := ensureDedupWire()
	if scanner == nil || scanner.DB == nil {
		return
	}
	dir := filepath.Dir(filePath)
	if _, done := scannedDirs.LoadOrStore(dir, true); done {
		return
	}
	if err := scanner.ScanPaths(ctx, [][2]string{{"", dir}}); err != nil {
		// Allow a retry next time: a scan that failed did not index the dir.
		scannedDirs.Delete(dir)
		log.Printf("[dedup] index refresh for %s failed: %v", dir, err)
		return
	}
	log.Printf("[dedup] index refreshed for %s", dir)
}

// dedupCheckFor is the single entry point every write path uses. It refreshes
// the index, runs the funnel, and splits the outcome into "refuse the write"
// (content-identical) and "warn but proceed" (name clash / metadata similar).
func dedupCheckFor(ctx context.Context, filePath string) (*dedup.Result, error) {
	checker, _ := ensureDedupWire()
	if checker == nil {
		return nil, nil
	}
	// Only pay for the scan when there is something to look up, and only
	// for files that actually exist on disk.
	if _, err := os.Stat(filePath); err == nil {
		refreshIndexForDir(ctx, filePath)
	}
	// Re-read the root per call: the Checker is cached for the life of the
	// process, and MUSIC_DIR can differ from what it saw at construction.
	checker.SetMusicRoot(utils.MusicRoot())
	r := checker.Check(ctx, filePath, dedup.Options{MusicRoot: utils.MusicRoot()})
	return &r, nil
}
