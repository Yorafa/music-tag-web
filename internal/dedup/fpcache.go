package dedup

import (
	"context"
	"os"

	"gorm.io/gorm"

	"go-music-tag/internal/db"
	"go-music-tag/internal/fingerprint"
)

// The fingerprint cache.
//
// Duplicate detection was re-decoding the same files on every check. One
// fpcalc run costs ~0.4s for a 120s track, and a check decodes the file
// under test *plus* every candidate whose duration lands in the ±5s window.
// Tagging a 40-track album re-decoded each track, and a re-run of the same
// check paid the whole bill again to reach the same verdict.
//
// So the subfingerprints are stored alongside the duration, keyed on the
// file's identity at the time they were computed. A cache with no
// invalidation is worse than no cache — a re-encoded file keeps its path and
// its row, so a stale entry would outlive the audio it describes and go on
// reporting the *old* track's duplicates forever. Size and mtime are what
// makes a hit trustworthy; mtime alone would miss an edit that preserves it,
// and size alone would miss a same-length re-encode.

// fpCacheEntry is the stored form, mirroring music_folder's fp columns.
type fpCacheEntry struct {
	Blob  []byte
	Size  int64
	MTime int64
}

// readFpCache returns the cached fingerprint for path when it is still
// trustworthy, and (zero, false) otherwise.
//
// A stat failure is a miss, not an error: the caller is about to hand the
// path to fpcalc, which will produce the real diagnosis.
func readFpCache(gdb *gorm.DB, path string) (fingerprint.Fingerprint, bool) {
	if gdb == nil || path == "" {
		return fingerprint.Fingerprint{}, false
	}
	// Scanned into db.Folder rather than a local struct on purpose.
	// db.Folder carries explicit `gorm:"column:..."` tags, and GORM's
	// naming strategy maps the field FPMTime to "fpm_time" — not the
	// "fp_mtime" the column is actually called. A local anonymous struct
	// therefore reads a column that does not exist, which SQLite answers
	// with NULL, so FPMTime silently arrived as 0 and the validity check
	// below never matched: the cache never hit, and every file looked
	// stale. A wrong-but-plausible column name is worse than a missing
	// one, because nothing errors.
	var row db.Folder
	err := gdb.Table("music_folder").
		Select("fingerprint", "fp_size", "fp_mtime").
		Where("path = ?", path).
		Scan(&row).Error
	// Scan into a struct yields no error for "no such row", so an empty
	// blob is the real "nothing cached" signal.
	if err != nil || len(row.Fingerprint) == 0 {
		return fingerprint.Fingerprint{}, false
	}

	fi, statErr := os.Stat(path)
	if statErr != nil {
		return fingerprint.Fingerprint{}, false
	}
	// The identity check. A file whose size or mtime moved is not the file
	// the blob describes.
	if fi.Size() != row.FPSize || fi.ModTime().UnixNano() != row.FPMTime {
		return fingerprint.Fingerprint{}, false
	}

	fp, decErr := fingerprint.Decode(row.Fingerprint)
	if decErr != nil {
		// A blob we cannot read is a miss, and the next successful
		// fingerprint write overwrites it. Surfacing this as an error
		// would abort a check that can still be answered by decoding.
		return fingerprint.Fingerprint{}, false
	}
	return fp, true
}

// writeFpCache stores fp for path, recording the file's size and mtime as
// the validity key.
//
// Failures are silent by design: a cache that cannot be written costs speed,
// not correctness, and the caller is mid-check. Returning an error would
// mean deciding what a duplicate verdict is worth when a disk is full.
func writeFpCache(gdb *gorm.DB, path string, fp fingerprint.Fingerprint) {
	if gdb == nil || path == "" || len(fp.Encode()) == 0 {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	err = gdb.Table("music_folder").
		Where("path = ?", path).
		Updates(map[string]interface{}{
			"fingerprint": fp.Encode(),
			"fp_size":     fi.Size(),
			"fp_mtime":    fi.ModTime().UnixNano(),
		}).Error
	_ = err
}

// cachedFingerprint is the dedup stage's entry point: cache first, decode
// only on a miss, and write back whatever it decoded.
//
// The write is unconditional on a miss, so the second check of the same file
// is free even if the fingerprint index task never runs.
func (c *Checker) cachedFingerprint(ctx context.Context, path string) (fingerprint.Fingerprint, error) {
	if fp, ok := readFpCache(c.db, path); ok {
		return fp, nil
	}
	fp, err := fingerprint.Raw(ctx, c.fpcalcPath, path)
	if err != nil {
		return fingerprint.Fingerprint{}, err
	}
	writeFpCache(c.db, path, fp)
	return fp, nil
}
