package tasks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/db"
	"go-music-tag/internal/testaudio"
)

// withRetryCount makes the next ProcessTask call believe it is asynq attempt
// `n`. asynq's own context key is unexported in an internal package, so this
// swaps the indirection in tidy.go instead.
func withRetryCount(t *testing.T, n int) {
	t.Helper()
	prev := asynqRetryCount
	asynqRetryCount = func(context.Context) int { return n }
	t.Cleanup(func() { asynqRetryCount = prev })
}

// The bug this file exists for: TidyFolder was handed root-relative
// worklist paths and demanded absolute ones, so every file was refused with
// `SafeAbs: "17/song.ogg" is not absolute`, nothing moved, and — the audit
// row being written only on success — the operation history stayed empty
// while the UI reported "已提交目录整理异步任务".
func auditDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(gdb); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	audit.SetDB(gdb)
	t.Cleanup(func() { audit.SetDB(nil) })
	return gdb
}

func tidyRows(t *testing.T, gdb *gorm.DB) []db.OperationLog {
	t.Helper()
	var out []db.OperationLog
	if err := gdb.Model(&db.OperationLog{}).
		Where("action = ?", audit.ActionTidyFolder).
		Order("id asc").Find(&out).Error; err != nil {
		t.Fatalf("query audit rows: %v", err)
	}
	return out
}

// The feature: a root-relative payload — which is what the frontend sends,
// because /app/media is a container path it never learns — actually moves
// the file.
func TestTidy_AcceptsRootRelativeMusicPaths(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	mustMkdir(t, filepath.Join(music, "17"))
	track := testaudio.SeedWAV(t, filepath.Join(music, "17"), "song.wav", 1)
	rel, err := filepath.Rel(music, track)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(rel) {
		t.Fatalf("fixture is not root-relative: %q", rel)
	}

	gdb := auditDB(t)
	h := &TidyFolderHandler{MusicRoot: music, DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{
		Type: TypeTidyFolder,
		Payload: &TidyFolderPayload{
			MusicPaths: []string{rel}, // relative, as the client sends
			RootPath:   music,         // absolute, as the dialog asks for
			FirstDir:   "artist",
		},
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}

	if _, err := os.Stat(track); err == nil {
		t.Errorf("the track did not move out of %s", filepath.Join(music, "17"))
	}
	rows := tidyRows(t, gdb)
	if len(rows) == 0 {
		t.Fatal("a successful tidy wrote no audit row")
	}
	if rows[0].Status != audit.StatusSuccess {
		t.Errorf("status = %q, want %q", rows[0].Status, audit.StatusSuccess)
	}
}

// Mixed forms in one batch: the frontend's rows are relative, but rows
// restored from an older DB can be absolute. Both must work, and an
// absolute one must not be double-prefixed into a path that does not exist.
func TestTidy_AcceptsMixedAbsoluteAndRelativePaths(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	mustMkdir(t, filepath.Join(music, "17"))
	relTrack := testaudio.SeedWAV(t, filepath.Join(music, "17"), "rel.wav", 1)
	absTrack := testaudio.SeedWAV(t, filepath.Join(music, "17"), "abs.wav", 1)

	rel, _ := filepath.Rel(music, relTrack)
	gdb := auditDB(t)
	h := &TidyFolderHandler{MusicRoot: music, DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{
		Type: TypeTidyFolder,
		Payload: &TidyFolderPayload{
			MusicPaths: []string{rel, absTrack},
			RootPath:   music,
			FirstDir:   "album",
		},
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	for _, p := range []string{relTrack, absTrack} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s did not move", filepath.Base(p))
		}
	}
	if rows := tidyRows(t, gdb); len(rows) != 2 {
		t.Errorf("audit rows = %d, want one per moved file (2)", len(rows))
	}
}

// The reported symptom: a tidy that moved nothing must still leave a trace.
// Before this, a fully-refused batch produced NO audit row, which is exactly
// why the path-form bug above could sit unnoticed — the toast said success
// and the operation history stayed empty.
func TestTidy_AFailedBatchIsAudited(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	mustMkdir(t, filepath.Join(music, "Loose"))
	track := testaudio.SeedWAV(t, filepath.Join(music, "Loose"), "song.wav", 1)
	outside := t.TempDir()

	gdb := auditDB(t)
	h := &TidyFolderHandler{MusicRoot: music, DB: gdb}
	err := h.ProcessTask(context.Background(), Task{
		Type: TypeTidyFolder,
		Payload: &TidyFolderPayload{
			MusicPaths: []string{track},
			RootPath:   outside, // refused: outside the library
			FirstDir:   "artist",
		},
	})
	if err == nil {
		t.Fatal("ProcessTask accepted an out-of-library root_path")
	}

	rows := tidyRows(t, gdb)
	if len(rows) == 0 {
		t.Fatal("a refused batch wrote NO audit row — it is invisible in 操作审计")
	}
	if rows[0].Status != audit.StatusFailed {
		t.Errorf("status = %q, want %q", rows[0].Status, audit.StatusFailed)
	}
	if rows[0].ErrorMsg == "" {
		t.Error("error_msg is empty; the operator cannot tell why")
	}
	if rows[0].ItemCount != 1 {
		t.Errorf("item_count = %d, want 1 (the batch size that was refused)", rows[0].ItemCount)
	}
}

// A partially-failing batch summarises into ONE row, not one per failure —
// 500 refused files must not bury the log. The successes still get their own
// per-file rows from tidyOne.
func TestTidy_PartialFailureIsSummarisedIntoOneRow(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	mustMkdir(t, filepath.Join(music, "17"))
	good := testaudio.SeedWAV(t, filepath.Join(music, "17"), "good.wav", 1)
	// A path outside the library: refused per-file, the batch continues.
	outside := filepath.Join(base, "elsewhere", "stray.wav")
	mustMkdir(t, filepath.Dir(outside))
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	goodRel, _ := filepath.Rel(music, good)

	gdb := auditDB(t)
	h := &TidyFolderHandler{MusicRoot: music, DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{
		Type: TypeTidyFolder,
		Payload: &TidyFolderPayload{
			MusicPaths: []string{goodRel, outside},
			RootPath:   music,
			FirstDir:   "artist",
		},
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}

	rows := tidyRows(t, gdb)
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want 2 (one per success + one failure summary)", len(rows))
	}
	// The summary is the LAST row (written after the loop).
	sum := rows[len(rows)-1]
	if sum.Status != audit.StatusPartial {
		t.Errorf("summary status = %q, want %q", sum.Status, audit.StatusPartial)
	}
	if sum.ItemCount != 1 {
		t.Errorf("summary item_count = %d, want 1 (one file failed)", sum.ItemCount)
	}
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(sum.Details), &details); err != nil {
		t.Fatalf("summary details is not JSON: %v (%q)", err, sum.Details)
	}
	if details["requested"] != float64(2) || details["failed"] != float64(1) {
		t.Errorf("summary details = %v, want requested=2 failed=1", details)
	}
	ex, _ := details["examples"].([]interface{})
	if len(ex) == 0 {
		t.Error("summary carries no example reason; the operator still cannot tell why")
	}
}

// A refused root is permanent, so asynq retries the task (MaxRetry(5)). Only
// the FIRST attempt may write a row — otherwise one click becomes six
// identical entries in 操作审计. The error must still be returned on every
// attempt so the task record ends up failed rather than completed.
func TestTidy_RetryAttemptsDoNotDuplicateTheRefusalRow(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	mustMkdir(t, filepath.Join(music, "Loose"))
	track := testaudio.SeedWAV(t, filepath.Join(music, "Loose"), "song.wav", 1)
	outside := t.TempDir()

	gdb := auditDB(t)
	h := &TidyFolderHandler{MusicRoot: music, DB: gdb}
	payload := &TidyFolderPayload{
		MusicPaths: []string{track},
		RootPath:   outside,
		FirstDir:   "artist",
	}

	// asynq carries the retry count in the context; a plain context.Background()
	// reads as 0, i.e. the first attempt.
	for attempt := 0; attempt < 3; attempt++ {
		withRetryCount(t, attempt)
		if err := h.ProcessTask(context.Background(), Task{Type: TypeTidyFolder, Payload: payload}); err == nil {
			t.Fatalf("attempt %d: ProcessTask accepted an out-of-library root_path", attempt)
		}
	}

	rows := tidyRows(t, gdb)
	if len(rows) != 1 {
		t.Errorf("audit rows = %d, want 1 — a single refused batch must not log once per retry", len(rows))
	}
}

// A valid root where EVERY file fails must report "failed", not "partial".
// Without this, a batch that moved nothing at all would read as a partial
// success — the same "it said OK but nothing happened" shape as the original
// bug, one level up.
func TestTidy_AllFilesFailingIsReportedAsFailedNotPartial(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	mustMkdir(t, filepath.Join(music, "Loose"))

	var strays []string
	for _, n := range []string{"a.wav", "b.wav"} {
		p := filepath.Join(base, "elsewhere", n)
		mustMkdir(t, filepath.Dir(p))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		strays = append(strays, p)
	}

	gdb := auditDB(t)
	h := &TidyFolderHandler{MusicRoot: music, DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{
		Type: TypeTidyFolder,
		Payload: &TidyFolderPayload{
			MusicPaths: strays, // all outside the library
			RootPath:   music,  // the root itself is fine
			FirstDir:   "artist",
		},
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}

	rows := tidyRows(t, gdb)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1 summary row", len(rows))
	}
	if rows[0].Status != audit.StatusFailed {
		t.Errorf("status = %q, want %q — nothing moved, so this is not a partial success",
			rows[0].Status, audit.StatusFailed)
	}
	if rows[0].ItemCount != 2 {
		t.Errorf("item_count = %d, want 2", rows[0].ItemCount)
	}
}

// Accepting a relative path must not become a traversal hole: "../" is still
// resolved against the root and must still land inside it.
func TestTidy_StillRefusesTraversalAfterTheWidening(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	mustMkdir(t, filepath.Join(music, "Loose"))
	track := testaudio.SeedWAV(t, filepath.Join(music, "Loose"), "song.wav", 1)
	canary := filepath.Join(base, "canary.wav")
	if err := os.WriteFile(canary, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	gdb := auditDB(t)
	h := &TidyFolderHandler{MusicRoot: music, DB: gdb}
	if err := h.ProcessTask(context.Background(), Task{
		Type: TypeTidyFolder,
		Payload: &TidyFolderPayload{
			MusicPaths: []string{"../canary.wav", track},
			RootPath:   music,
			FirstDir:   "artist",
		},
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Errorf("ESCAPED: the canary outside the library was moved: %v", err)
	}
	rows := tidyRows(t, gdb)
	var sawPartial bool
	for _, r := range rows {
		if strings.Contains(r.Details, "escapes root") {
			sawPartial = true
		}
	}
	if !sawPartial {
		t.Errorf("the refused traversal is not explained in the audit row: %+v", rows)
	}
}
