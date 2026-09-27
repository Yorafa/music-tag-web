package handler_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/db"
	"go-music-tag/internal/gateway/handler"
)

// deleteRouter wires the endpoint with an in-memory audit DB.
func deleteRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	audit.SetDB(gdb)
	t.Cleanup(func() { audit.SetDB(nil) })
	r := gin.New()
	r.POST("/api/delete_files/", handler.DeleteFiles)
	return r, gdb
}

func seedFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func postDelete(t *testing.T, r *gin.Engine, body string) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/delete_files/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

func lastAuditRow(t *testing.T, gdb *gorm.DB) db.OperationLog {
	t.Helper()
	var rows []db.OperationLog
	if err := gdb.Model(&db.OperationLog{}).Order("id desc").Limit(1).Find(&rows).Error; err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no audit row was written for a delete")
	}
	return rows[0]
}

// The endpoint serves two callers now: the duplicate cleanup and the user's own
// 「删除选中」. The audit row used to claim "duplicate_cleanup" unconditionally,
// so a manual delete would have been logged as a duplicate cleanup — a log
// that is confidently wrong is worse than one that is vague.
func TestDeleteFiles_RecordsTheCallersStatedReason(t *testing.T) {
	music := t.TempDir()
	data := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	seedFile(t, music, "manual.ogg")
	r, gdb := deleteRouter(t)

	postDelete(t, r, `{"file_full_paths":["manual.ogg"],"requested_by":"worklist"}`)

	var details map[string]interface{}
	if err := json.Unmarshal([]byte(lastAuditRow(t, gdb).Details), &details); err != nil {
		t.Fatalf("details is not JSON: %v (%q)", err, lastAuditRow(t, gdb).Details)
	}
	if details["requested_by"] != "worklist" {
		t.Errorf("requested_by = %v, want %q — the row must not claim a reason the caller did not give",
			details["requested_by"], "worklist")
	}
}

// A client that predates the field must not have a reason invented for it.
func TestDeleteFiles_DefaultsAnAbsentReasonToUnspecified(t *testing.T) {
	music := t.TempDir()
	data := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	seedFile(t, music, "legacy.ogg")
	r, gdb := deleteRouter(t)

	postDelete(t, r, `{"file_full_paths":["legacy.ogg"]}`)

	var details map[string]interface{}
	if err := json.Unmarshal([]byte(lastAuditRow(t, gdb).Details), &details); err != nil {
		t.Fatalf("details is not JSON: %v", err)
	}
	if details["requested_by"] != "unspecified" {
		t.Errorf("requested_by = %v, want %q", details["requested_by"], "unspecified")
	}
}

// It is a client-supplied string written verbatim into the audit log, so it
// has to be bounded — an unbounded field is a log-forging surface.
func TestDeleteFiles_TruncatesAnOverlongReason(t *testing.T) {
	music := t.TempDir()
	data := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)
	seedFile(t, music, "long.ogg")
	r, gdb := deleteRouter(t)

	huge := strings.Repeat("A", 5000)
	body, _ := json.Marshal(map[string]interface{}{
		"file_full_paths": []string{"long.ogg"},
		"requested_by":    huge,
	})
	postDelete(t, r, string(body))

	got := lastAuditRow(t, gdb)
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(got.Details), &details); err != nil {
		t.Fatalf("details is not JSON: %v", err)
	}
	rb, _ := details["requested_by"].(string)
	if len(rb) > 64 {
		t.Errorf("requested_by is %d chars, want <= 64 — it is written into the audit log verbatim", len(rb))
	}
	if len(got.Details) > 1000 {
		t.Errorf("details is %d bytes; a 5000-char reason leaked in", len(got.Details))
	}
}
