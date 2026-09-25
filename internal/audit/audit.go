package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"go-music-tag/internal/db"
)

// Action 常量定义所有受审计的操作类型。
const (
	ActionUpdateID3      = "update_id3"
	ActionBatchUpdateID3 = "batch_update_id3"
	ActionAutoScrape     = "auto_scrape"
	ActionFilenameParse  = "filename_parse"
	ActionTidyFolder     = "tidy_folder"
	ActionDownload       = "download"
	ActionUploadCover    = "upload_cover"
)

// Status 常量定义操作结果状态。
const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusPartial = "partial"
	StatusSkipped = "skipped"
)

var (
	globalDB *gorm.DB
	dbMu     sync.RWMutex
)

// SetDB 设置全局审计日志数据库句柄。
func SetDB(d *gorm.DB) {
	dbMu.Lock()
	defer dbMu.Unlock()
	globalDB = d
}

// GetDB 获取全局审计日志数据库句柄。
func GetDB() *gorm.DB {
	dbMu.RLock()
	defer dbMu.RUnlock()
	return globalDB
}

// Record 直接写入一条 OperationLog 记录。
func Record(ctx context.Context, opLog *db.OperationLog) error {
	if opLog == nil {
		return nil
	}
	d := GetDB()
	if d == nil {
		log.Printf("[audit] [no-db] action=%s target=%s status=%s operator=%s details=%s",
			opLog.Action, opLog.Target, opLog.Status, opLog.Operator, opLog.Details)
		return nil
	}
	if opLog.CreatedAt.IsZero() {
		opLog.CreatedAt = time.Now()
	}
	if opLog.ItemCount <= 0 {
		opLog.ItemCount = 1
	}
	if opLog.Operator == "" {
		opLog.Operator = "admin"
	}
	if err := d.WithContext(ctx).Create(opLog).Error; err != nil {
		log.Printf("[audit] save log failed: %v", err)
		return err
	}
	return nil
}

// Log 快速记录一条操作审计日志。
func Log(ctx context.Context, action string, target string, operator string, status string, count int, details interface{}, opErr error) *db.OperationLog {
	if status == "" {
		if opErr != nil {
			status = StatusFailed
		} else {
			status = StatusSuccess
		}
	}
	if operator == "" {
		operator = "admin"
	}
	if count <= 0 {
		count = 1
	}

	var detailsJSON string
	if details != nil {
		switch v := details.(type) {
		case string:
			detailsJSON = v
		case []byte:
			detailsJSON = string(v)
		default:
			if b, err := json.Marshal(v); err == nil {
				detailsJSON = string(b)
			} else {
				detailsJSON = fmt.Sprintf("%v", v)
			}
		}
	}

	var errorMsg string
	if opErr != nil {
		errorMsg = opErr.Error()
	}

	opLog := db.OperationLog{
		Action:    action,
		Target:    target,
		Operator:  operator,
		Status:    status,
		ItemCount: count,
		Details:   detailsJSON,
		ErrorMsg:  errorMsg,
		CreatedAt: time.Now(),
	}

	_ = Record(ctx, &opLog)
	return &opLog
}

// QueryOptions 审计日志查询选项。
type QueryOptions struct {
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Action   string `json:"action"`
	Status   string `json:"status"`
	Search   string `json:"search"`
}

// Query 分页并按条件检索审计日志。
func Query(ctx context.Context, opts QueryOptions) ([]db.OperationLog, int64, error) {
	d := GetDB()
	if d == nil {
		return []db.OperationLog{}, 0, nil
	}

	if opts.Page <= 0 {
		opts.Page = 1
	}
	if opts.PageSize <= 0 {
		opts.PageSize = 20
	}
	if opts.PageSize > 100 {
		opts.PageSize = 100
	}

	tx := d.WithContext(ctx).Model(&db.OperationLog{})

	if opts.Action != "" && opts.Action != "all" {
		tx = tx.Where("action = ?", opts.Action)
	}
	if opts.Status != "" && opts.Status != "all" {
		tx = tx.Where("status = ?", opts.Status)
	}
	if opts.Search != "" {
		// REVIEW.md P3-5: the search term is user input, so `%` and `_` in
		// it are LIKE metacharacters — searching for `100%` matched every
		// row, and `_` matched any character. Not an injection (the value
		// is still a bound parameter) but a filter the caller cannot
		// express. Escape the metacharacters and name the escape
		// character explicitly.
		//
		// The escape character is `!` rather than `\` because the two
		// supported dialects disagree about it: MySQL processes backslash
		// escapes inside string literals (so ESCAPE '\' is needed there)
		// while SQLite does not (so the same literal arrives as two
		// characters and the statement is rejected). `!` means nothing
		// special in either.
		s := "%" + escapeLikeWildcards(strings.TrimSpace(opts.Search)) + "%"
		// Parenthesised: an unparenthesised OR chain silently changes
		// meaning the moment another filter is added to this query.
		tx = tx.Where(`(target LIKE ? ESCAPE '!'
			OR details LIKE ? ESCAPE '!'
			OR operator LIKE ? ESCAPE '!'
			OR error_msg LIKE ? ESCAPE '!')`, s, s, s, s)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var results []db.OperationLog
	offset := (opts.Page - 1) * opts.PageSize
	if err := tx.Order("id DESC").Offset(offset).Limit(opts.PageSize).Find(&results).Error; err != nil {
		return nil, 0, err
	}

	if results == nil {
		results = []db.OperationLog{}
	}
	return results, total, nil
}

// Clear 清理审计日志。olderThanDays <= 0 时清理全部日志。
func Clear(ctx context.Context, olderThanDays int) (int64, error) {
	d := GetDB()
	if d == nil {
		return 0, nil
	}

	tx := d.WithContext(ctx).Model(&db.OperationLog{})
	if olderThanDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -olderThanDays)
		tx = tx.Where("created_at < ?", cutoff)
	} else {
		tx = tx.Where("1 = 1")
	}

	res := tx.Delete(&db.OperationLog{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// likeEscapeChar is the escape character used by Query's LIKE predicates.
// It must not be `%` or `_`, and must be one byte in both MySQL and SQLite.
const likeEscapeChar = '!'

// escapeLikeWildcards doubles every LIKE metacharacter in s, including the
// escape character itself — otherwise a search for a literal `!` would be
// read as the start of an escape sequence.
func escapeLikeWildcards(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '%' || r == '_' || r == likeEscapeChar {
			b.WriteRune(likeEscapeChar)
		}
		b.WriteRune(r)
	}
	return b.String()
}
