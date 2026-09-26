package tasks

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"gorm.io/gorm"

	"go-music-tag/internal/audioext"
	"go-music-tag/internal/db"
	"go-music-tag/internal/fingerprint"
)

// FpIndexHandler fills in music_folder.duration for library audio.
//
// Why this exists: duplicate detection picks its stage-3 (fingerprint)
// candidates by track length, and reading a length means decoding the audio.
// The folder scanner only stats files, so it cannot know it. Populating the
// column costs one fpcalc run per track — about 0.4s for a 120s file, so a
// 2000-track library takes roughly 13 minutes, once — and afterwards the
// dedup check only has to fingerprint the handful of files whose duration is
// close, instead of sweeping the whole library.
//
// Running it is safe to repeat: rows are keyed by path and the task skips
// anything already carrying a duration, so a second run only picks up files
// added since.
type FpIndexHandler struct {
	DB *gorm.DB
	// FPcalcPath overrides binary discovery; empty means look it up on PATH.
	FPcalcPath string
	// Workers is the number of concurrent fpcalc processes. Zero means 4.
	// fpcalc is CPU-bound, so this is the knob that trades indexing time for
	// load; the default is deliberately conservative because the worker also
	// serves tagging and download tasks.
	Workers int
	// Batch commits rows every N files so a cancelled run keeps its progress.
	Batch int
	// Progress, when set, is called after each batch with (indexed, failed).
	Progress func(indexed, failed int)
	// Rearm, when set, is called at the end of a run that indexed at least
	// one file, to schedule the next one.
	//
	// This is what keeps the index following the library. It used to run
	// once at worker boot, so anything that added a file afterwards — a
	// download with 加入库, a scan of a folder the user just dropped in —
	// kept duration=NULL. durationCandidates filters `duration > 0`, so
	// the fingerprint stage stopped seeing new tracks as candidates and
	// degraded to its size-window fallback, which misses exactly the
	// re-encodes the stage exists to catch (one re-encode changes a file's
	// size by up to 25x while barely moving its length). Nothing logged,
	// because from the query's point of view the row was not there.
	//
	// Enqueuing from each producer instead would mean three call sites
	// that each have to remember, and the next thing that writes library
	// files will not. A timer would cover hand-copied files too, at the
	// cost of waking up forever on an unchanged library. Re-arming on
	// progress converges and then stops on its own.
	Rearm func()
}

// fpIndexRow is the subset of music_folder this task reads and writes.
type fpIndexRow struct {
	Path     string
	Size     int64
	Duration int64
}

// staleIndexedFiles returns rows that carry a duration but still need work:
// either their cached fingerprint no longer describes the file on disk, or
// they never had one.
//
// A file re-encoded in place keeps its path, its row, and its non-zero
// duration, so nothing in the SQL-visible state says it needs re-reading —
// yet its fingerprint now describes audio that is no longer there. The
// validity key is the file's size and mtime at the time the blob was
// written; either moving means the blob is stale.
//
// This costs one os.Stat per already-indexed track (nanoseconds, no decode),
// which is why it can run on every re-arm rather than needing its own
// schedule.
func (h *FpIndexHandler) staleIndexedFiles() ([]fpIndexRow, error) {
	// db.Folder, not a local struct: it carries explicit
	// `gorm:"column:..."` tags, and GORM's naming strategy maps a field
	// named FPMTime to "fpm_time" rather than the "fp_mtime" the column
	// actually has. A local struct reads a nonexistent column, SQLite
	// answers NULL, and FPMTime arrives as 0 — which would make every
	// indexed file look stale and re-decode the whole library on every
	// re-arm. Silently wrong, never an error.
	var rows []db.Folder
	err := h.DB.Table("music_folder").
		Select("path", "size", "duration", "fingerprint", "fp_size", "fp_mtime").
		Where("file_type NOT IN (?, ?)", "folder", "image").
		Where("size > 0").
		Where("duration > 0").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]fpIndexRow, 0, len(rows))
	for _, r := range rows {
		fi, serr := os.Stat(r.Path)
		if serr != nil {
			// Gone from disk. Not this task's business to delete the
			// row, and re-decoding a missing file cannot succeed.
			continue
		}
		// No cached fingerprint at all is itself work to do, not a reason
		// to skip. A library indexed before the fp columns existed has a
		// duration on every row and no fingerprint on any of them, and
		// the duration query above will never look at them again — so
		// treating "no cache yet" as "nothing to do" leaves every
		// pre-existing library permanently uncached, which is the whole
		// cost the cache was added to remove.
		if len(r.Fingerprint) == 0 {
			out = append(out, fpIndexRow{Path: r.Path, Size: fi.Size(), Duration: r.Duration})
			continue
		}
		if fi.Size() == r.FPSize && fi.ModTime().UnixNano() == r.FPMTime {
			continue
		}
		out = append(out, fpIndexRow{Path: r.Path, Size: fi.Size(), Duration: r.Duration})
	}
	return out, nil
}

const (
	defaultFpIndexWorkers = 4
	defaultFpIndexBatch   = 50
)

func (h *FpIndexHandler) ProcessTask(ctx context.Context, t Task) error {
	if h.DB == nil {
		return fmt.Errorf("fpindex: DB not initialized")
	}
	fpcalcPath := h.FPcalcPath
	if fpcalcPath == "" {
		p, err := fingerprint.LookPath()
		if err != nil {
			// Not an error worth retrying: without the binary there is no
			// index to build, and stage 3 already degrades to skipping
			// itself. Returning nil keeps this out of the dead-letter queue.
			log.Printf("[fpindex] no fpcalc on PATH; music_folder.duration stays empty "+
				"and the fingerprint stage falls back to a size-filtered disk walk: %v", err)
			return nil
		}
		fpcalcPath = p
	}

	workers := h.Workers
	if workers <= 0 {
		workers = defaultFpIndexWorkers
	}
	batch := h.Batch
	if batch <= 0 {
		batch = defaultFpIndexBatch
	}

	pending, err := h.pendingFiles()
	if err != nil {
		return fmt.Errorf("fpindex: list pending: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	jobs := make(chan fpIndexRow)
	var (
		mu      sync.Mutex
		indexed int
		failed  int
		wg      sync.WaitGroup
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for row := range jobs {
				// One decode, both columns. fpcalc is decode-bound — it
				// costs the same ~0.4s for a 120s track whether the caller
				// reads only the DURATION line or the whole fingerprint —
				// so reading the subfingerprints here is close to free, and
				// it is what lets the fingerprint stage skip decoding on
				// every later check.
				//
				// DurationOf's early exit saved the pipe transfer of the
				// fingerprint payload, not the decode, so it was the wrong
				// thing to optimise here.
				fp, derr := fingerprint.Raw(ctx, fpcalcPath, row.Path)
				mu.Lock()
				if derr != nil {
					failed++
				} else {
					indexed++
				}
				mu.Unlock()
				// A file that will not decode is not an error worth failing
				// the run over, but it also must not be recorded as a
				// duration: duration=0 is what marks "not yet indexed", and
				// writing 0 here would make the row look indexed while
				// remaining invisible to duration queries.
				if derr != nil {
					continue
				}
				updates := map[string]interface{}{
					"duration": int64(fp.Duration()),
				}
				// The cache's validity key. Stored with the blob so a
				// reader can tell whether it still describes this file.
				if fi, serr := os.Stat(row.Path); serr == nil {
					updates["fingerprint"] = fp.Encode()
					updates["fp_size"] = fi.Size()
					updates["fp_mtime"] = fi.ModTime().UnixNano()
				}
				if uerr := h.DB.WithContext(ctx).Table("music_folder").
					Where("path = ?", row.Path).
					Updates(updates).Error; uerr != nil {
					mu.Lock()
					failed++
					mu.Unlock()
				}
			}
		}()
	}

	feedCtx, cancelFeed := context.WithCancel(ctx)
	defer cancelFeed()
	var feederWG sync.WaitGroup
	feederWG.Add(1)
	go func() {
		defer feederWG.Done()
		defer close(jobs)
		for _, row := range pending {
			select {
			case <-feedCtx.Done():
				return
			case jobs <- row:
			}
		}
	}()

	wg.Wait()
	feederWG.Wait()

	mu.Lock()
	idx, fail := indexed, failed
	mu.Unlock()
	if h.Progress != nil {
		h.Progress(idx, fail)
	}
	// Only re-arm on real progress. A run that indexed nothing has caught
	// up with the library, and re-arming anyway would leave the index
	// waking itself up indefinitely. Indexed is the right signal rather
	// than "there was pending work": a run over files that all fail to
	// decode would otherwise re-arm forever, retrying the same
	// undecodable files at fpIndex cadence.
	if idx > 0 && h.Rearm != nil {
		h.Rearm()
	}
	return nil
}

// pendingFiles returns indexed library audio with no duration yet.
//
// size > 0 excludes directory rows that the scanner also files under
// music_folder: a folder's duration is meaningless and a zero one would make
// it match every short file's duration window.
func (h *FpIndexHandler) pendingFiles() ([]fpIndexRow, error) {
	var rows []fpIndexRow
	err := h.DB.Table("music_folder").
		Select("path", "size", "duration", "fingerprint", "fp_size", "fp_mtime").
		// Not `file_type = 'music'`. Three writers spell that column three
		// ways — the scanner writes "music", yt_dl writes the download
		// source ("youtube", "netease", …) — so filtering on it skipped
		// every track that arrived via 加入库: no duration, therefore never
		// a candidate for anyone else's fingerprint comparison, therefore
		// invisible duplicate detection. See internal/dedup/audiotable.go.
		Where("file_type NOT IN (?, ?)", "folder", "image").
		Where("size > 0").
		// The SQL narrows to "never indexed" — the cheap, indexable case.
		// "Indexed but stale" is decided in Go below, because it needs an
		// os.Stat and SQL has no way to ask the filesystem.
		Where("duration IS NULL OR duration = 0").
		Order("path").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	// Plus the rows that were indexed once and have since changed on disk.
	//
	// This is what repairs a file that was re-encoded in place: it keeps its
	// path and its non-zero duration, so the duration query above would
	// never look at it again, and its stale fingerprint would go on
	// reporting the OLD track's duplicates for as long as the row lived.
	stale, err := h.staleIndexedFiles()
	if err != nil {
		return nil, err
	}
	rows = append(rows, stale...)
	// The scanner decides file_type by extension, but the two lists have
	// drifted before (see internal/audioext), so the extension is checked
	// again here. Indexing a .jpg would spend 0.4s proving fpcalc cannot
	// decode it.
	out := rows[:0]
	for _, r := range rows {
		if audioext.IsLibraryPath(r.Path) {
			out = append(out, r)
		}
	}
	return out, nil
}

// compile-time assertion that the handler satisfies the task contract.
var _ Handler = (*FpIndexHandler)(nil)
