package handler

import (
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

// TaskScan handles GET /api/task1/ — enqueue a full_scan_folder task.
// Matches Django's task1 get-handler that kicks off `full_scan_folder_task`.
func TaskScan(c *gin.Context) {
	enqueueTypedTask(c, tasks.TypeFullScanFolder, &tasks.FullScanPayload{
		SubPaths: [][2]string{},
	})
}

// TaskClear handles GET /api/task2/ — enqueue a clear_music task.
func TaskClear(c *gin.Context) {
	taskclient.Init()
	_, err := taskclient.Enqueue(
		asynq.NewTask(tasks.TypeClearMusic, nil,
			asynq.Queue("default"), asynq.MaxRetry(1),
		),
	)
	if err != nil {
		Failure(c, err.Error())
		return
	}
	Success(c, "success", nil)
}

// FullScanFolder handles GET /api/full_scan_folder/ — enqueue a full scan.
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

// BatchAutoUpdateID3 handles POST /api/batch_auto_update_id3/ — enqueue the
// batch-auto-tag task. Body: { batch, source_list: [], select_mode? }
func BatchAutoUpdateID3(c *gin.Context) {
	var req struct {
		Batch      string   `json:"batch" binding:"required"`
		SourceList []string `json:"source_list" binding:"required"`
		SelectMode string   `json:"select_mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid request: "+err.Error())
		return
	}
	enqueueTypedTask(c, tasks.TypeBatchAutoTag, &tasks.BatchAutoTagPayload{
		Batch:      req.Batch,
		SourceList: req.SourceList,
		SelectMode: req.SelectMode,
	})
}

// UpdateScanFolder handles POST /api/update_scan_folder/ — enqueue the
// update scan task (incremental vs full).
func UpdateScanFolder(c *gin.Context) {
	enqueueTypedTask(c, tasks.TypeUpdateScanFolder, &tasks.FullScanPayload{
		SubPaths: [][2]string{},
	})
}

// ListTaskRecords handles GET /api/record/ — paginated TaskRecord list.
// Mirrors Django's `applications.task.views.TaskModelViewSets.list`.
// Query params: ?batch=&state=&page=&page_size=
//
// Note: the actual DB query is left to the P1.5 followup (depends on
// initializing the gateway's DB handle). For now we return an empty list so
// the endpoint stays callable.
func ListTaskRecords(c *gin.Context) {
	page := atoiOr(c.Query("page"), 1)
	pageSize := atoiOr(c.Query("page_size"), 50)
	SuccessData(c, gin.H{
		"results":   []interface{}{},
		"page":      page,
		"page_size": pageSize,
		"count":     0,
		"note":      "P1: record query wired in P1.5",
	})
}

func atoiOr(s string, fallback int) int {
	if s == "" {
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
		asynq.Queue("default"), asynq.MaxRetry(5), asynq.Timeout(6*60*60*1e9 /* 6h */),
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
		"task_id":  info.ID,
		"type":     info.Type,
		"queue":    info.Queue,
		"state":    info.State,
	})
}
