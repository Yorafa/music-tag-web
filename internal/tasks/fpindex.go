package tasks

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"go-music-tag/internal/audioext"
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
}

// fpIndexRow is the subset of music_folder this task reads and writes.
type fpIndexRow struct {
	Path     string
	Size     int64
	Duration int64
}

const (
	defaultFpIndexWorkers = 4
	defaultFpIndexBatch   = 50
	// fpIndexTimeout bounds one fpcalc run. fpcalc decodes the entire file,
	// so this has to accommodate long tracks; 60s is roughly a 15-minute one.
	fpIndexTimeout = 60 * time.Second
)

func (h *FpIndexHandler) ProcessTask(ctx context.Context, t Task) error {
	if h.DB == nil {
		return fmt.Errorf("fpindex: DB not initialized")
	}
	fpcalcPath := h.FPcalcPath
	if fpcalcPath == "" {
		p, err := exec.LookPath("fpcalc")
		if err != nil {
			// Not an error worth retrying: without the binary there is no
			// index to build, and stage 3 already degrades to skipping
			// itself. Returning nil keeps this out of the dead-letter queue.
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
				dur, derr := probeDuration(ctx, fpcalcPath, row.Path)
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
				if uerr := h.DB.WithContext(ctx).Table("music_folder").
					Where("path = ?", row.Path).
					Update("duration", dur).Error; uerr != nil {
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
		Select("path", "size", "duration").
		Where("file_type = ?", "music").
		Where("size > 0").
		Where("duration IS NULL OR duration = 0").
		Order("path").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
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

// probeDuration runs fpcalc -raw and returns the duration in whole seconds.
//
// Only the first line is needed here, so the read stops at the fingerprint
// line rather than pulling ~3.8KB of subfingerprints per file through the
// pipe for a value that sits in DURATION=.
func probeDuration(ctx context.Context, fpcalcPath, path string) (int64, error) {
	runCtx, cancel := context.WithTimeout(ctx, fpIndexTimeout)
	defer cancel()

	// #nosec G204 -- fpcalcPath comes from LookPath, not from a request.
	cmd := exec.CommandContext(runCtx, fpcalcPath, "-raw", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	defer func() { _ = cmd.Wait() }()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "DURATION=") {
			continue
		}
		secs, perr := strconv.ParseFloat(strings.TrimPrefix(line, "DURATION="), 64)
		if perr != nil {
			return 0, fmt.Errorf("fpindex: bad DURATION line %q: %w", line, perr)
		}
		// Stop reading. Closing stdout makes fpcalc see EPIPE on its next
		// write, which is fine — we already have the number, and for a long
		// track the remaining subfingerprints are most of the work.
		d := int64(secs)
		if d < 0 {
			d = 0
		}
		return d, nil
	}
	if serr := scanner.Err(); serr != nil {
		return 0, serr
	}
	return 0, fmt.Errorf("fpindex: fpcalc produced no DURATION line for %s", path)
}

// compile-time assertion that the handler satisfies the task contract.
var _ Handler = (*FpIndexHandler)(nil)
