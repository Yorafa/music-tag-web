package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"go-music-tag/internal/db"
	"go-music-tag/internal/testaudio"
)

// dupRoute drives CheckDuplicate and decodes the envelope's `data` payload.
func dupRoute(t *testing.T, relPaths []string) duplicateReport {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/check_duplicate/", CheckDuplicate)

	raw, _ := json.Marshal(map[string]interface{}{"file_full_paths": relPaths})
	req := httptest.NewRequest(http.MethodPost, "/check_duplicate/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var env struct {
		Result bool            `json:"result"`
		Data   duplicateReport `json:"data"`
		Msg    string          `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if !env.Result {
		t.Fatalf("request failed: %s", w.Body.String())
	}
	return env.Data
}

func rowFor(t *testing.T, rep duplicateReport, rel string) duplicateRow {
	t.Helper()
	for _, r := range rep.Results {
		if r.FileFullPath == rel {
			return r
		}
	}
	t.Fatalf("no result row for %q in %#v", rel, rep.Results)
	return duplicateRow{}
}

// TestCheckDuplicate_FindsByteIdenticalCopy is the feature's reason to
// exist. Before this endpoint the funnel only ran on the write path, so a
// library that already contained a duplicate never said so — the user had
// to attempt a write to learn what a read could have told them.
func TestCheckDuplicate_FindsByteIdenticalCopy(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, leaf, "one.mp3")
	b, err := os.ReadFile(filepath.Join(leaf, "one.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "copy.mp3"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	rep := dupRoute(t, []string{"Artist/Album/copy.mp3"})

	row := rowFor(t, rep, "Artist/Album/copy.mp3")
	if row.Verdict != "duplicate" {
		t.Fatalf("verdict = %q, want duplicate (reason %q, run %v)",
			row.Verdict, row.Reason, row.Run)
	}
	// The verdict has to name the other file, or "it's a duplicate" is
	// not actionable — the user cannot tell which of the two to keep.
	if row.DuplicatePath != "Artist/Album/one.mp3" {
		t.Errorf("duplicate_path = %q, want Artist/Album/one.mp3", row.DuplicatePath)
	}
	if rep.Summary.Duplicate != 1 {
		t.Errorf("summary.duplicate = %d, want 1 (%+v)", rep.Summary.Duplicate, rep.Summary)
	}
}

// TestCheckDuplicate_CleanFileIsUnique is the other half, and the one that
// keeps the feature from being useless noise: a file with no twin must come
// back clean, or the user learns to ignore the badge.
func TestCheckDuplicate_CleanFileIsUnique(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "Artist", "Album")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, leaf, "one.mp3")

	rep := dupRoute(t, []string{"Artist/Album/one.mp3"})

	row := rowFor(t, rep, "Artist/Album/one.mp3")
	if row.Verdict != "unique" {
		t.Errorf("verdict = %q, want unique (reason %q)", row.Verdict, row.Reason)
	}
	if rep.Summary.Unique != 1 {
		t.Errorf("summary.unique = %d, want 1 (%+v)", rep.Summary.Unique, rep.Summary)
	}
}

// TestCheckDuplicate_ReportsPerRowNotRequestFailure: one missing file must
// not hide the verdict on the other forty. A request-level Failure would
// force the UI to show "check failed" and lose every real result with it.
func TestCheckDuplicate_ReportsPerRowNotRequestFailure(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, leaf, "present.mp3")

	rep := dupRoute(t, []string{"A/present.mp3", "A/nope.mp3"})

	if got := len(rep.Results); got != 2 {
		t.Fatalf("results = %d, want 2 (both rows, one good one bad)", got)
	}
	if v := rowFor(t, rep, "A/present.mp3").Verdict; v == "error" {
		t.Errorf("present file verdict = %q, want a real verdict", v)
	}
	if v := rowFor(t, rep, "A/nope.mp3").Verdict; v != "skipped" {
		t.Errorf("missing file verdict = %q, want skipped", v)
	}
	if rep.Summary.Skipped != 1 {
		t.Errorf("summary.skipped = %d, want 1", rep.Summary.Skipped)
	}
}

// TestCheckDuplicate_RefusesPathOutsideRoot: the endpoint takes a
// user-supplied path list, so SafeJoin has to be load-bearing. A traversal
// must be refused as an error row, not silently rooted.
func TestCheckDuplicate_RefusesPathOutsideRoot(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	rep := dupRoute(t, []string{"../../etc/passwd"})

	row := rowFor(t, rep, "../../etc/passwd")
	if row.Verdict != "error" {
		t.Fatalf("verdict = %q, want error for a path outside the root", row.Verdict)
	}
	if rep.Summary.Errors != 1 {
		t.Errorf("summary.error = %d, want 1", rep.Summary.Errors)
	}
}

// TestCheckDuplicate_RejectsOversizedBatch: each row is a full four-stage
// check with an fpcalc spawn, so the batch needs a ceiling.
func TestCheckDuplicate_RejectsOversizedBatch(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/check_duplicate/", CheckDuplicate)

	big := make([]string, maxDuplicateCheckPaths+1)
	for i := range big {
		big[i] = "f.mp3"
	}
	raw, _ := json.Marshal(map[string]interface{}{"file_full_paths": big})
	req := httptest.NewRequest(http.MethodPost, "/check_duplicate/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var env struct {
		Result bool `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Result {
		t.Error("an oversized batch should be refused, not processed")
	}
}

// ─── delete ───────────────────────────────────────────────────────────────

func delRoute(t *testing.T, relPaths []string) deleteReport {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/delete_files/", DeleteFiles)

	raw, _ := json.Marshal(map[string]interface{}{"file_full_paths": relPaths})
	req := httptest.NewRequest(http.MethodPost, "/delete_files/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var env struct {
		Result bool         `json:"result"`
		Data   deleteReport `json:"data"`
		Msg    string       `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if !env.Result {
		t.Fatalf("request failed: %s", w.Body.String())
	}
	return env.Data
}

// TestDeleteFiles_QuarantinesInsteadOfUnlinking is the safety property that
// makes this endpoint acceptable at all. The file must leave the library —
// that is the feature — but it must still exist on disk under the trash, so
// a mistaken click is one `mv` from being undone.
func TestDeleteFiles_QuarantinesInsteadOfUnlinking(t *testing.T) {
	music := t.TempDir()
	data := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)

	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, leaf, "gone.mp3")

	rep := delRoute(t, []string{"A/gone.mp3"})

	if rep.Deleted != 1 || rep.Failed != 0 {
		t.Fatalf("deleted=%d failed=%d, want 1/0 (%+v)", rep.Deleted, rep.Failed, rep.Results)
	}
	if _, err := os.Stat(filepath.Join(leaf, "gone.mp3")); !os.IsNotExist(err) {
		t.Error("the file should be gone from the library")
	}
	row := rep.Results[0]
	if row.TrashPath == "" {
		t.Fatal("no trash_path reported — the caller cannot recover the file")
	}
	if _, err := os.Stat(row.TrashPath); err != nil {
		t.Errorf("file was unlinked rather than quarantined: %v", err)
	}
	// The trash must sit outside MUSIC_DIR, or the scanner and
	// http.Dir(MUSIC_DIR) would keep serving a file the user deleted.
	// Containment is a prefix test, not filepath.Rel — Rel succeeds for
	// any two paths on one system and so proves nothing.
	if absTrash, err := filepath.Abs(row.TrashPath); err == nil {
		absMusic, _ := filepath.Abs(music)
		if absTrash == absMusic ||
			strings.HasPrefix(absTrash, absMusic+string(filepath.Separator)) {
			t.Errorf("trash path %q is inside MUSIC_DIR %q", absTrash, absMusic)
		}
	}
}

// TestDeleteFiles_RefusesDirectoryAndSymlink: a planted symlink must not be
// a way to move an arbitrary file out of the root, and a directory must not
// be recursed into (this endpoint is for files the dedup scan flagged).
func TestDeleteFiles_RefusesDirectoryAndSymlink(t *testing.T) {
	music := t.TempDir()
	data := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)

	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "precious.txt")
	if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(leaf, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	rep := delRoute(t, []string{"A", "A/link.txt", "../../etc/passwd"})

	if rep.Deleted != 0 {
		t.Errorf("deleted = %d, want 0 — every one of those is refused", rep.Deleted)
	}
	for _, row := range rep.Results {
		if row.Status != "refused" {
			t.Errorf("%s status = %q, want refused (%s)", row.FileFullPath, row.Status, row.Reason)
		}
	}
	// The symlink target must be untouched, which is the whole point.
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("symlink target was moved out of the way: %v", err)
	}
	if _, err := os.Stat(leaf); err != nil {
		t.Errorf("directory was removed: %v", err)
	}
}

// TestDeleteFiles_ReportsMissingInsteadOfFailingWhole: same per-row
// contract as the check — a file already gone by hand is a normal outcome
// of a cleanup pass, not a reason to discard the rows that did work.
func TestDeleteFiles_ReportsMissingInsteadOfFailingWhole(t *testing.T) {
	music := t.TempDir()
	data := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)

	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	testaudio.SeedMP3(t, leaf, "here.mp3")

	rep := delRoute(t, []string{"A/here.mp3", "A/never-existed.mp3"})

	if rep.Deleted != 1 {
		t.Errorf("deleted = %d, want 1 — the good row must still land", rep.Deleted)
	}
	if rep.Failed != 1 {
		t.Errorf("failed = %d, want 1", rep.Failed)
	}
	if rep.Results[1].Status != "missing" {
		t.Errorf("missing row status = %q, want missing", rep.Results[1].Status)
	}
}

// TestMoveAside_FallsBackWhenRenameCannotWork covers the cross-device path.
//
// In the default compose layout MUSIC_DIR and DATA_DIR are two separate
// bind mounts, so os.Rename returns EXDEV and a rename-only implementation
// works in `go test` (one temp dir) and fails in every real deployment.
// The rename is injected because a single temp dir can never produce a
// real EXDEV.
func TestMoveAside_FallsBackWhenRenameCannotWork(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp3")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "nested", "trash", "src.mp3")

	exdev := func(string, string) error {
		return &os.LinkError{Op: "rename", Old: src, New: dest, Err: syscall.EXDEV}
	}
	if err := moveAsideWith(src, dest, exdev); err != nil {
		t.Fatalf("moveAsideWith: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be gone")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("dest content = %q, want payload — the copy must be byte-exact", got)
	}
	// The fallback creates intermediate dirs; a rename-only version would
	// have left the copy impossible to place.
	if _, err := os.Stat(filepath.Dir(dest)); err != nil {
		t.Errorf("trash subdir not created: %v", err)
	}
}

// TestDeleteFiles_DropsIndexRow: a deleted file must not keep a
// music_folder row behind.
//
// This is the invisible half of the delete. internal/dedup picks its hash
// and fingerprint candidates by querying that index, and a row whose file
// is gone is a candidate that cannot be opened — worse, its `duration`
// still matches, so a deleted song stays in the candidate set for every
// future check. The file is already unlinked at this point, so only the
// row count can catch a regression here.
func TestDeleteFiles_DropsIndexRow(t *testing.T) {
	music := t.TempDir()
	data := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	t.Setenv("DATA_DIR", data)

	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(&db.Folder{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	leaf := filepath.Join(music, "A")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(leaf, "gone.mp3")
	testaudio.SeedMP3(t, leaf, "gone.mp3")
	keeper := filepath.Join(leaf, "stays.mp3")
	testaudio.SeedMP3(t, leaf, "stays.mp3")

	// uid carries a UNIQUE index, so it has to be set explicitly.
	for i, p := range []string{target, keeper} {
		if err := gdb.Create(&db.Folder{
			Name: filepath.Base(p), Path: p, Size: 1, FileType: "music",
			Duration: 120, UID: fmt.Sprintf("uid-%d", i),
		}).Error; err != nil {
			t.Fatalf("seed index: %v", err)
		}
	}

	// Swap in the test DB for the duration, then restore, so a nil
	// dedupDB in another test is not masked by ordering.
	prev := dedupDB
	dedupDB = gdb
	defer func() { dedupDB = prev }()

	rep := delRoute(t, []string{"A/gone.mp3"})
	if rep.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (%+v)", rep.Deleted, rep.Results)
	}

	var gone, left int64
	gdb.Model(&db.Folder{}).Where("path = ?", target).Count(&gone)
	gdb.Model(&db.Folder{}).Where("path = ?", keeper).Count(&left)
	if gone != 0 {
		t.Errorf("index row for the deleted file survived (%d rows) — a stale row keeps feeding dedup candidates", gone)
	}
	if left != 1 {
		t.Errorf("index row for the untouched file = %d, want 1 — cleanup is too broad", left)
	}
}
