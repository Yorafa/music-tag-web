package audit_test

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/db"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	audit.SetDB(gdb)
	return gdb
}

func TestAuditLogAndQuery(t *testing.T) {
	ctx := context.Background()
	_ = setupTestDB(t)

	// 1. Write single audit log
	op := audit.Log(ctx, audit.ActionUpdateID3, "test.mp3", "admin", audit.StatusSuccess, 1, map[string]string{
		"title":  "Test Song",
		"artist": "Test Artist",
	}, nil)

	if op.ID == 0 {
		t.Errorf("expected generated ID, got 0")
	}

	// 2. Write another log
	audit.Log(ctx, audit.ActionBatchUpdateID3, "album_folder", "admin", audit.StatusSuccess, 5, "batch info", nil)

	// 3. Query all
	results, total, err := audit.Query(ctx, audit.QueryOptions{
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	// Newer first
	if results[0].Action != audit.ActionBatchUpdateID3 {
		t.Errorf("expected newest log first, got %s", results[0].Action)
	}

	// 4. Query with filter
	results, total, err = audit.Query(ctx, audit.QueryOptions{
		Action: audit.ActionUpdateID3,
	})
	if err != nil {
		t.Fatalf("query with filter failed: %v", err)
	}
	if total != 1 || len(results) != 1 {
		t.Errorf("expected 1 filtered result, got %d", total)
	}
	if results[0].Target != "test.mp3" {
		t.Errorf("expected target 'test.mp3', got %s", results[0].Target)
	}

	// 5. Query with search
	results, total, err = audit.Query(ctx, audit.QueryOptions{
		Search: "album_folder",
	})
	if err != nil {
		t.Fatalf("query with search failed: %v", err)
	}
	if total != 1 || len(results) != 1 {
		t.Errorf("expected 1 search result, got %d", total)
	}

	// 6. Clear logs
	deleted, err := audit.Clear(ctx, 0)
	if err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", deleted)
	}

	// Verify empty
	_, total, _ = audit.Query(ctx, audit.QueryOptions{})
	if total != 0 {
		t.Errorf("expected 0 logs after clear, got %d", total)
	}
}

func TestAuditLogNoDB(t *testing.T) {
	ctx := context.Background()
	audit.SetDB(nil)

	// Should not panic or error
	op := audit.Log(ctx, audit.ActionUpdateID3, "test.mp3", "admin", audit.StatusSuccess, 1, nil, nil)
	if op == nil {
		t.Errorf("expected non-nil OperationLog return")
	}

	results, total, err := audit.Query(ctx, audit.QueryOptions{})
	if err != nil {
		t.Fatalf("query with no DB should not fail, got: %v", err)
	}
	if total != 0 || len(results) != 0 {
		t.Errorf("expected empty results when DB is nil")
	}
}
