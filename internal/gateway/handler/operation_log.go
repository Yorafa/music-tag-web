package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/audit"
)

// ListOperationLogs handles GET /api/operation_logs/ — paginated query of operation logs.
// Query params:
//   - page: int (default 1)
//   - page_size: int (default 20, max 100)
//   - action: string (optional, e.g. update_id3, batch_update_id3, auto_scrape, filename_parse, tidy_folder, download, upload_cover)
//   - status: string (optional, e.g. success, failed, partial)
//   - search: string (keyword search on target/details/operator/error_msg)
func ListOperationLogs(c *gin.Context) {
	page, pageSize := clampPaging(
		atoiOr(c.Query("page"), 1),
		atoiOr(c.Query("page_size"), 20),
		20,
	)
	action := c.Query("action")
	status := c.Query("status")
	search := c.Query("search")

	results, total, err := audit.Query(c.Request.Context(), audit.QueryOptions{
		Page:     page,
		PageSize: pageSize,
		Action:   action,
		Status:   status,
		Search:   search,
	})
	if err != nil {
		Failure(c, "query logs failed: "+err.Error())
		return
	}

	SuccessData(c, gin.H{
		"results":   results,
		"count":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// clientRecordableActions whitelists the actions a browser client is allowed
// to write via RecordOperationLog. Server-side operations (update_id3,
// auto_scrape, …) are recorded by their own handlers and must never be
// forgeable from the client, so only client-observable events that no
// server handler can see live here. Playback preflight/stream failures are
// the motivating case: the file 404s on the /media static route (which has
// no per-request audit hook), so the only place that knows the play attempt
// failed is the browser.
var clientRecordableActions = map[string]bool{
	audit.ActionPlaybackFailed: true,
}

// RecordOperationLog handles POST /api/operation_logs/record/ — lets the
// frontend persist a client-observed event into the operation audit log so
// it is inspectable alongside server-side operations. The action is
// whitelisted (see clientRecordableActions) to keep this from becoming an
// arbitrary log-injection endpoint.
func RecordOperationLog(c *gin.Context) {
	var req struct {
		Action   string `json:"action"`
		Target   string `json:"target"`
		Status   string `json:"status"`
		Details  string `json:"details"`
		ErrorMsg string `json:"error_msg"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Failure(c, "invalid body: "+err.Error())
		return
	}
	if !clientRecordableActions[req.Action] {
		Failure(c, "action not recordable: "+req.Action)
		return
	}
	status := req.Status
	if status == "" {
		status = audit.StatusFailed
	}

	var opErr error
	if req.ErrorMsg != "" {
		opErr = errors.New(req.ErrorMsg)
	}

	entry := audit.Log(c.Request.Context(), req.Action, req.Target, "", status, 1, req.Details, opErr)
	SuccessData(c, gin.H{"id": entry.ID})
}

// ClearOperationLogs handles POST /api/operation_logs/clear/ — clears audit logs.
// Body: { days?: int } (if days > 0, deletes logs older than N days; otherwise deletes all).
func ClearOperationLogs(c *gin.Context) {
	var req struct {
		Days int `json:"days"`
	}
	_ = c.ShouldBindJSON(&req)

	deleted, err := audit.Clear(c.Request.Context(), req.Days)
	if err != nil {
		Failure(c, "clear logs failed: "+err.Error())
		return
	}

	Success(c, "logs cleared", gin.H{
		"cleared": deleted,
	})
}
