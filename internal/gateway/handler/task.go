package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"go-music-tag/internal/taskclient"
	"go-music-tag/internal/tasks"
)

// ClearAsyncTasks handles GET /api/clear_celery/ — cancels every active
// pending task via asynq Inspector. Mirrors Django's `clear_celery()` which
// calls `celery.app.control.purge()`.
func ClearAsyncTasks(c *gin.Context) {
	taskclient.Init()
	n, err := taskclient.CancelAll()
	if err != nil {
		Failure(c, "cancel failed: "+err.Error())
		return
	}
	Success(c, "cleared", gin.H{"cancelled": n})
}

// ActiveQueue handles GET /api/active_queue/ — returns workers, queues,
// active and pending task counts. Mirrors Django's Celery inspect API.
func ActiveQueue(c *gin.Context) {
	taskclient.Init()
	insp := taskclient.Inspector()
	if insp == nil {
		Failure(c, "inspector unavailable")
		return
	}
	servers, _ := insp.Servers()
	queues, _ := insp.Queues()
	pending, _ := insp.ListPendingTasks("default")

	// Flatten pending
	type taskRow struct {
		Type    string `json:"type"`
		Payload string `json:"payload,omitempty"`
		ID      string `json:"id"`
	}
	pendingRows := []taskRow{}
	for _, t := range pending {
		pendingRows = append(pendingRows, taskRow{
			Type: t.Type, Payload: string(t.Payload), ID: t.ID,
		})
	}
	SuccessData(c, gin.H{
		"servers": servers,
		"queues":  queues,
		"pending": pendingRows,
	})
}

// FullScanFolder handles POST /api/full_scan_folder/ — enqueue a full scan.
func FullScanFolder(c *gin.Context) {
	enqueueTypedTask(c, tasks.TypeFullScanFolder, &tasks.FullScanPayload{
		SubPaths: [][2]string{},
	})
}

// TidyFolder handles POST /api/tidy_folder/ — enqueue a tidy_folder task.
// Body: { music_paths?: [], root_path, first_dir, second_dir? }
func TidyFolder(c *gin.Context) {
	var req struct {
		MusicPaths []string `json:"music_paths"`
		RootPath   string   `json:"root_path" binding:"required"`
		FirstDir   string   `json:"first_dir" binding:"required"`
		SecondDir  string   `json:"second_dir"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}
	enqueueTypedTask(c, tasks.TypeTidyFolder, &tasks.TidyFolderPayload{
		MusicPaths: req.MusicPaths,
		RootPath:   req.RootPath,
		FirstDir:   req.FirstDir,
		SecondDir:  req.SecondDir,
	})
}

// PreviewPruneEmptyFolders handles POST /api/prune_empty_folders/preview/ —
// report what the cleanup would remove, without removing it.
//
// The confirmation dialog needs the list, not a count: "16 stale rows" is
// something the user can only nod at, whereas the sixteen paths are
// something they can check against what they remember deleting. A count
// would also be worthless if wrong, which is the argument for showing the
// real thing.
//
// Read-only, so unlike the mutation this does not go through the worker
// queue — a round trip to get a preview would mean the dialog renders
// before the answer arrives, and the list could go stale between the
// preview and the confirmation anyway. It reuses the task handler's own
// dry-run passes, so what is shown is what would be done.
func PreviewPruneEmptyFolders(c *gin.Context) {
	var req struct {
		SubPaths [][2]string `json:"sub_paths"`
	}
	// Same contract as the mutation: an absent body means the whole library.
	_ = c.ShouldBindJSON(&req)

	h := &tasks.PruneEmptyFoldersHandler{DB: dedupDB}
	dirs, rows, err := h.PreviewPrune(c.Request.Context(), req.SubPaths)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	// Empty slices rather than nil, so the JSON carries [] instead of null.
	// A client that maps over this should not have to know that "nothing to
	// do" arrives as a different type than "something to do".
	if dirs == nil {
		dirs = []string{}
	}
	if rows == nil {
		rows = []string{}
	}
	SuccessData(c, gin.H{
		"empty_dirs":    dirs,
		"vanished_rows": rows,
		"total":         len(dirs) + len(rows),
	})
}

// PruneEmptyFolders handles POST /api/prune_empty_folders/ — enqueue a task
// that deletes directories left empty by a tidy, a rename or a delete.
//
// Body: { sub_paths?: [[parent_uid, path], ...] } — omit for the whole
// library, which is what the UI sends. Kept as a task rather than done
// inline because it is filesystem mutation over an unbounded tree, and every
// other mutation in this codebase goes through asynq for the same reason.
func PruneEmptyFolders(c *gin.Context) {
	var req struct {
		SubPaths [][2]string `json:"sub_paths"`
	}
	// A body is optional here: an empty object and no body at all both mean
	// "the whole library", so a bind failure is not worth rejecting.
	_ = c.ShouldBindJSON(&req)
	enqueueTypedTask(c, tasks.TypePruneEmptyFolders, &tasks.PruneEmptyFoldersPayload{
		SubPaths: req.SubPaths,
	})
}

// UpdateScanFolder handles POST /api/update_scan_folder/ — enqueue the
// update scan task (incremental vs full).
func UpdateScanFolder(c *gin.Context) {
	enqueueTypedTask(c, tasks.TypeUpdateScanFolder, &tasks.FullScanPayload{
		SubPaths: [][2]string{},
	})
}

// Pagination bounds shared by every list endpoint (REVIEW.md P2-9).
//
// ListTaskRecords previously passed page_size straight into Limit(), so
// ?page_size=99999999 asked the DB for the whole table; audit.Query had
// its own ad-hoc clamp that ListTaskRecords knew nothing about. One
// helper, both endpoints.
const (
	defaultPageSize = 50
	maxPageSize     = 100
	// maxPage bounds the offset. Without it, page=999999999 overflows
	// (page-1)*pageSize into a negative offset, which GORM turns into
	// "no LIMIT" — silently returning everything.
	maxPage = 1_000_000
)

// clampPaging normalises caller-supplied page/page_size into safe values.
// Never returns a non-positive page or size, so callers can use the result
// directly in Offset()/Limit() without re-checking.
func clampPaging(page, pageSize, defPageSize int) (int, int) {
	if pageSize <= 0 {
		pageSize = defPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	if page <= 0 {
		page = 1
	}
	if page > maxPage {
		page = maxPage
	}
	return page, pageSize
}

func atoiOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	// Bound the digit count: the old loop multiplied without any ceiling,
	// so a long enough input overflowed int and could land on a negative
	// value, which then flowed into Offset().
	const maxDigits = 9
	if len(s) > maxDigits {
		return fallback
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return fallback
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// ─── helpers ───────────────────────────────────────────────────────────────

// enqueueTypedTask marshals payload to JSON + pushes onto asynq default queue.
// 5 retries x exponential backoff via asynq defaults.
func enqueueTypedTask(c *gin.Context, typeName string, payload interface{}) {
	taskclient.Init()
	t, err := tasks.NewTypedTask(typeName, payload,
		asynq.Queue("default"), asynq.MaxRetry(5), asynq.Timeout(6*time.Hour),
	)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	info, err := taskclient.Enqueue(t)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	SuccessData(c, gin.H{
		"task_id": info.ID,
		"type":    info.Type,
		"queue":   info.Queue,
		"state":   info.State,
	})
}
