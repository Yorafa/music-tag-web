package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/db"
	"go-music-tag/internal/gateway/handler"
)

func setupTestRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	audit.SetDB(gdb)

	r.GET("/api/operation_logs/", handler.ListOperationLogs)
	r.POST("/api/operation_logs/clear/", handler.ClearOperationLogs)
	r.POST("/api/operation_logs/record/", handler.RecordOperationLog)
	return r, gdb
}

// A whitelisted client action (playback_failed) is persisted and then shows up
// in the same query the audit view uses, giving the 404 toast a durable home.
func TestRecordOperationLog_Whitelisted(t *testing.T) {
	r, _ := setupTestRouter(t)

	body, _ := json.Marshal(map[string]string{
		"action":    audit.ActionPlaybackFailed,
		"target":    "foo/bar.flac",
		"status":    audit.StatusFailed,
		"error_msg": "HTTP 404",
	})
	req, _ := http.NewRequest("POST", "/api/operation_logs/record/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on record, got %d: %s", w.Code, w.Body.String())
	}

	// The recorded entry must be queryable via the same endpoint the audit
	// view reads, filtered by action.
	reqList, _ := http.NewRequest("GET", "/api/operation_logs/?action="+audit.ActionPlaybackFailed, nil)
	wList := httptest.NewRecorder()
	r.ServeHTTP(wList, reqList)

	var resp struct {
		Data struct {
			Results []struct {
				Action   string `json:"action"`
				Target   string `json:"target"`
				Status   string `json:"status"`
				ErrorMsg string `json:"error_msg"`
			} `json:"results"`
			Count int64 `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Data.Count != 1 {
		t.Fatalf("expected 1 recorded log, got %d", resp.Data.Count)
	}
	got := resp.Data.Results[0]
	if got.Action != audit.ActionPlaybackFailed {
		t.Errorf("action: got %q want %q", got.Action, audit.ActionPlaybackFailed)
	}
	if got.Target != "foo/bar.flac" {
		t.Errorf("target: got %q want %q", got.Target, "foo/bar.flac")
	}
	if got.Status != audit.StatusFailed {
		t.Errorf("status: got %q want %q", got.Status, audit.StatusFailed)
	}
	if got.ErrorMsg != "HTTP 404" {
		t.Errorf("error_msg: got %q want %q", got.ErrorMsg, "HTTP 404")
	}
}

// Omitting status must land as "failed", never "success". Every action a client
// may record is a failure by definition (playback_failed), and a browser
// forgetting a field must not produce a green row in the audit log — that would
// be a lie the audit page cannot distinguish from a real success.
func TestRecordOperationLog_DefaultsToFailed(t *testing.T) {
	r, _ := setupTestRouter(t)

	body, _ := json.Marshal(map[string]string{
		"action": audit.ActionPlaybackFailed,
		"target": "foo/bar.flac",
	})
	req, _ := http.NewRequest("POST", "/api/operation_logs/record/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	reqList, _ := http.NewRequest("GET", "/api/operation_logs/?action="+audit.ActionPlaybackFailed, nil)
	wList := httptest.NewRecorder()
	r.ServeHTTP(wList, reqList)

	var resp struct {
		Data struct {
			Results []struct {
				Status string `json:"status"`
			} `json:"results"`
			Count int64 `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Data.Count != 1 {
		t.Fatalf("expected 1 recorded log, got %d", resp.Data.Count)
	}
	if got := resp.Data.Results[0].Status; got != audit.StatusFailed {
		t.Errorf("status: got %q, want %q — a client-omitted status must never read as a success", got, audit.StatusFailed)
	}
}

// A non-whitelisted action must be refused so this endpoint can't be used to
// forge server-side operations (update_id3, trash_purge, …) from the client.
func TestRecordOperationLog_RejectsNonWhitelisted(t *testing.T) {
	r, _ := setupTestRouter(t)

	body, _ := json.Marshal(map[string]string{
		"action": audit.ActionTrashPurge,
		"target": "foo/bar.flac",
	})
	req, _ := http.NewRequest("POST", "/api/operation_logs/record/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Failure() returns 200 with result=false in this codebase's envelope, so
	// assert on the envelope, not the HTTP status.
	var resp struct {
		Result bool `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Result {
		t.Fatalf("expected result=false for non-whitelisted action")
	}

	// And nothing was written.
	reqList, _ := http.NewRequest("GET", "/api/operation_logs/", nil)
	wList := httptest.NewRecorder()
	r.ServeHTTP(wList, reqList)
	var listResp struct {
		Data struct {
			Count int64 `json:"count"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wList.Body.Bytes(), &listResp)
	if listResp.Data.Count != 0 {
		t.Errorf("expected 0 logs after rejected record, got %d", listResp.Data.Count)
	}
}

func TestListAndClearOperationLogs(t *testing.T) {
	r, _ := setupTestRouter(t)

	// Seed logs
	audit.Log(nil, audit.ActionUpdateID3, "song1.mp3", "admin", audit.StatusSuccess, 1, "update details", nil)
	audit.Log(nil, audit.ActionBatchUpdateID3, "folder1", "admin", audit.StatusSuccess, 3, "batch details", nil)

	// Test GET /api/operation_logs/
	req, _ := http.NewRequest("GET", "/api/operation_logs/?page=1&page_size=10", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Result bool `json:"result"`
		Data   struct {
			Results []map[string]interface{} `json:"results"`
			Count   int64                    `json:"count"`
			Page    int                      `json:"page"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !resp.Result {
		t.Errorf("expected result=true")
	}
	if resp.Data.Count != 2 {
		t.Errorf("expected count=2, got %d", resp.Data.Count)
	}
	if len(resp.Data.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(resp.Data.Results))
	}

	// Test POST /api/operation_logs/clear/
	body, _ := json.Marshal(map[string]int{"days": 0})
	reqClear, _ := http.NewRequest("POST", "/api/operation_logs/clear/", bytes.NewReader(body))
	reqClear.Header.Set("Content-Type", "application/json")
	wClear := httptest.NewRecorder()
	r.ServeHTTP(wClear, reqClear)

	if wClear.Code != http.StatusOK {
		t.Fatalf("expected 200 on clear, got %d: %s", wClear.Code, wClear.Body.String())
	}

	// Verify empty list
	reqAfter, _ := http.NewRequest("GET", "/api/operation_logs/", nil)
	wAfter := httptest.NewRecorder()
	r.ServeHTTP(wAfter, reqAfter)

	var respAfter struct {
		Data struct {
			Count int64 `json:"count"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAfter.Body.Bytes(), &respAfter)
	if respAfter.Data.Count != 0 {
		t.Errorf("expected count=0 after clear, got %d", respAfter.Data.Count)
	}
}
