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
	return r, gdb
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
