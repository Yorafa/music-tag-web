package handler

import (
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
