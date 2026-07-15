// Integration tests for the P1 asynq worker.
//
// Covers producer → consumer end-to-end for three task types:
//
//   • TypeFullScanFolder  ("scan:full")
//   • TypeBatchAutoTag    ("tag:batch_auto")
//   • TypeYouTubeDownload ("download:youtube")
//
// Per test, a *testRig bootstraps:
//   1. miniredis (in-process Redis, no Docker required for CI)
//   2. sqlite (in-memory database via gorm.io/driver/sqlite)
//   3. asynq producer (Client) and consumer (Server + Inspector)
//
// Skips testcontainers-go/Redis for portability: miniredis speaks the same
// go-redis wire protocol asynq uses; tests run anywhere `go test` does.
//
// YouTubeDownload uses a fake shell script to inject yt-dlp behavior — no
// real binary required.

package tasks_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bogem/id3v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"go-music-tag/internal/cache"
	"go-music-tag/internal/db"
	"go-music-tag/internal/events"
	"go-music-tag/internal/plugin"
	"go-music-tag/internal/tag"
	"go-music-tag/internal/tasks"
)

// ─── TagSource mock for BatchAutoTag ────────────────────────────────────────

type mockTagSource struct {
	name        string
	fetchResult []plugin.Song
}

func (m *mockTagSource) Name() string        { return m.name }
func (m *mockTagSource) DisplayName() string { return "Mock " + m.name }
func (m *mockTagSource) SupportsSearch() bool { return false }
func (m *mockTagSource) SupportsLyric() bool  { return false }
func (m *mockTagSource) SupportsId3() bool   { return true } // mock is used by BatchAutoTag, which writes ID3
func (m *mockTagSource) Search(_ context.Context, _ string, _, _ int) (*plugin.SearchResult, error) {
	return &plugin.SearchResult{}, nil
}
func (m *mockTagSource) FetchID3ByTitle(_ context.Context, title string) ([]plugin.Song, error) {
	out := make([]plugin.Song, 0, len(m.fetchResult))
	for _, s := range m.fetchResult {
		// echo the requested title back as the song's name so matchScore
		// produces exact match (t=2) when SelectMode=simple.
		s := s
		s.Name = title
		s.Source = m.name
		out = append(out, s)
	}
	return out, nil
}
func (m *mockTagSource) FetchLyric(_ context.Context, _ string) (string, error) {
	return "", nil
}

// mockSourceName returns the per-test mock-source key. The name embeds
// t.Name() so tests are isolated even when run in the same process (by
// go test -parallel). Every installMockSource call pairs with a t.Cleanup
// UninstallMockTagSource so registry state never leaks across tests.
func mockSourceName(t *testing.T) string {
	return "test-mock-" + t.Name()
}

func installMockSource(t *testing.T) {
	t.Helper()
	name := mockSourceName(t)
	plugin.InstallMockTagSource(name, &mockTagSource{
		name: name,
		fetchResult: []plugin.Song{{
			ID:       "mock-1",
			Name:     "",
			Artist:   "Various",
			ArtistID: "a1",
			Album:    "Greatest Hits",
			AlbumID:  "ab1",
			Year:     "2020",
			Genre:    "Pop",
		}},
	})
	t.Cleanup(func() { plugin.UninstallMockTagSource(name) })
}

// ─── testRig bootstrap ─────────────────────────────────────────────────────

type testRig struct {
	Redis     *miniredis.Miniredis
	DB        *gorm.DB
	DBPath    string
	MusicRoot string
	Client    *asynq.Client
	Server    *asynq.Server
	Inspector *asynq.Inspector
	serverCh  chan error
}

func newTestRig(t *testing.T) *testRig {
	t.Helper()
	// Single TempDir for both db and music — each subsequent t.TempDir()
	// call returns a NEW unique directory, so sharing two paths would
	// desynchronise the scanner's view from the test's expectations.
	base := t.TempDir()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	dbPath := filepath.Join(base, "test.db")
	gormDB, err := db.Open(db.Config{
		Driver:   "sqlite",
		DSN:      dbPath,
		LogLevel: logger.Silent,
	})
	if err != nil {
		mr.Close()
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.AutoMigrate(gormDB); err != nil {
		mr.Close()
		t.Fatalf("AutoMigrate: %v", err)
	}

	opts := asynq.RedisClientOpt{Addr: mr.Addr()}
	client := asynq.NewClient(opts)
	inspector := asynq.NewInspector(opts)

	rig := &testRig{
		Redis:     mr,
		DB:        gormDB,
		DBPath:    dbPath,
		MusicRoot: base,
		Client:    client,
		Inspector: inspector,
	}
	t.Cleanup(rig.Shutdown)
	return rig
}

// Start launches asynq server with the handler bundle registration callback.
func (r *testRig) Start(t *testing.T, register func(mux *asynq.ServeMux)) {
	t.Helper()
	mux := asynq.NewServeMux()
	register(mux)
	srv := asynq.NewServer(asynq.RedisClientOpt{Addr: r.Redis.Addr()}, asynq.Config{
		Concurrency: 1,
		Queues:      map[string]int{"default": 1},
	})
	r.Server = srv
	r.serverCh = make(chan error, 1)
	go func() { r.serverCh <- srv.Run(mux) }()
	time.Sleep(150 * time.Millisecond) // let worker settle
}

// Enqueue mirrors what gateway handler does: tasks.NewTypedTask + asynq.Client.Enqueue.
func (r *testRig) Enqueue(t *testing.T, typeName string, payload interface{}, opts ...asynq.Option) *asynq.TaskInfo {
	t.Helper()
	tk, err := tasks.NewTypedTask(typeName, payload, opts...)
	if err != nil {
		t.Fatalf("NewTypedTask: %v", err)
	}
	info, err := r.Client.Enqueue(tk, opts...)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return info
}

// WaitForCompletion polls the default queue until pending+active drop to 0.
func (r *testRig) WaitForCompletion(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := r.Inspector.GetQueueInfo("default")
		if err == nil && info.Pending == 0 && info.Active == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.dumpState(t)
	t.Fatalf("WaitForCompletion timed out after %v", timeout)
}

func (r *testRig) dumpState(t *testing.T) {
	t.Helper()
	info, err := r.Inspector.GetQueueInfo("default")
	if err != nil {
		t.Logf("inspector GetQueueInfo err: %v", err)
		return
	}
	t.Logf("queue[default].pending=%d active=%d", info.Pending, info.Active)
	pending, _ := r.Inspector.ListPendingTasks("default")
	for _, p := range pending {
		t.Logf("  PENDING: type=%q payload=%q", p.Type, string(p.Payload))
	}
	active, _ := r.Inspector.ListActiveTasks("default")
	for _, a := range active {
		t.Logf("  ACTIVE:  type=%q", a.Type)
	}
}

func (r *testRig) Shutdown() {
	if r.Server != nil {
		r.Server.Shutdown()
	}
	if r.Redis != nil {
		r.Redis.Close()
	}
}

// ─── Test 1: FullScanFolder ─────────────────────────────────────────────────

func TestIntegration_FullScanFolder_ProducerToConsumer(t *testing.T) {
	installMockSource(t)
	rig := newTestRig(t)

	// Set up a music tree: MusicDir/Artist/Album/track1.mp3
	root := rig.MusicRoot
	musicDir := filepath.Join(root, "Artist", "Album")
	if err := os.MkdirAll(musicDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a minimal "ID3v2 header" file. dhowden/tag.ReadFrom sniffs the
	// ID3 magic and parses frames; we don't need actual MPEG audio frames —
	// the file type is decided by extension during scanning.
	mp3Path := filepath.Join(musicDir, "track1.mp3")
	if err := os.WriteFile(mp3Path, []byte("ID3\x04\x00\x00\x00\x00\x00\x00FAKE-FRAMES"), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}

	// Consumer (worker side)
	rig.Start(t, func(mux *asynq.ServeMux) {
		tasks.NewFullScanMux(mux, &tasks.FullScanHandler{
			DB:        rig.DB,
			MusicRoot: root,
		})
	})

	// Producer (gateway side): enqueue a full scan — matches what
	// handler.FullScanFolder does when the operator hits "Refresh".
	rig.Enqueue(t, tasks.TypeFullScanFolder, &tasks.FullScanPayload{
		SubPaths: [][2]string{},
	})

	rig.WaitForCompletion(t, 8*time.Second)

	// Verify db.Folder rows.
	var folders []db.Folder
	if err := rig.DB.Find(&folders).Error; err != nil {
		t.Fatalf("Find folders: %v", err)
	}
	t.Logf("scan wrote %d Folder rows: %+v", len(folders), summarize(folders))

	var mp3Row *db.Folder
	for i := range folders {
		if folders[i].Path == mp3Path {
			mp3Row = &folders[i]
			break
		}
	}
	if mp3Row == nil {
		t.Fatalf("expected mp3 %q to appear in db.Folder; got %d rows", mp3Path, len(folders))
	}
	if mp3Row.FileType != "music" {
		t.Errorf("mp3 row: file_type=%q (want 'music')", mp3Row.FileType)
	}
	if mp3Row.UID == "" {
		t.Error("mp3 row: expected non-empty UID")
	}
	// parent dir should be recorded as a folder-type row
	var albumRow *db.Folder
	for i := range folders {
		if folders[i].Path == musicDir {
			albumRow = &folders[i]
			break
		}
	}
	if albumRow == nil {
		t.Fatalf("expected album dir %q in db.Folder", musicDir)
	}
	if albumRow.FileType != "folder" {
		t.Errorf("album dir row: file_type=%q (want 'folder')", albumRow.FileType)
	}
	if albumRow.UID != mp3Row.ParentID {
		t.Errorf("mp3.ParentID=%q should match album.UID=%q", mp3Row.ParentID, albumRow.UID)
	}
}

// ─── Test 2: BatchAutoTag ───────────────────────────────────────────────────

func TestIntegration_BatchAutoTag_StateTransitions(t *testing.T) {
	installMockSource(t)
	rig := newTestRig(t)

	// Music file
	mp3Path := filepath.Join(rig.MusicRoot, "song.mp3")
	if err := os.WriteFile(mp3Path, []byte("ID3\x04\x00\x00\x00\x00\x00\x00FAKE"), 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}

	// Seed one TaskRecord in state=wait with batch id
	const batch = "batch-int-001"
	rec := db.TaskRecord{
		SongName:  "",
		FullPath:  mp3Path,
		State:     "wait",
		Batch:     batch,
		CreatedAt: time.Now(),
	}
	if err := rig.DB.Create(&rec).Error; err != nil {
		t.Fatalf("seed TaskRecord: %v", err)
	}

	// Consumer registers the BatchAutoTagHandler.
	rig.Start(t, func(mux *asynq.ServeMux) {
		tasks.NewBatchAutoTagMux(mux, &tasks.BatchAutoTagHandler{
			DB: rig.DB,
		})
	})

	// Producer enqueues the batch task.
	srcName := mockSourceName(t)
	rig.Enqueue(t, tasks.TypeBatchAutoTag, &tasks.BatchAutoTagPayload{
		Batch:      batch,
		SourceList: []string{srcName},
		SelectMode: "simple",
	})

	rig.WaitForCompletion(t, 8*time.Second)

	// Assert TaskRecord state transitioned wait → success.
	var got db.TaskRecord
	if err := rig.DB.First(&got, "id = ?", rec.ID).Error; err != nil {
		t.Fatalf("Find TaskRecord: %v", err)
	}
	if got.State != "success" {
		t.Errorf("TaskRecord.state=%q (want 'success')", got.State)
	}
	if got.TagSource != srcName {
		t.Errorf("TaskRecord.tag_source=%q (want %q)", got.TagSource, srcName)
	}
	if got.SongName == "" {
		t.Error("TaskRecord.song_name should be populated after a successful match")
	}

	// Assert task_task row got synced.
	var syncTask db.Task
	if err := rig.DB.Where("full_path = ?", mp3Path).First(&syncTask).Error; err != nil {
		t.Fatalf("Find db.Task by full_path: %v", err)
	}
	if syncTask.State != "success" {
		t.Errorf("db.Task.state=%q (want 'success')", syncTask.State)
	}
	if syncTask.SongName == "" {
		t.Error("db.Task.song_name should be populated")
	}

	// Note: we don't read back the file's tags here; that would require
	// parsing a fake ID3v2 blob, which bogem/id3v2 doesn't accept unless
	// frames are valid UTF-8 strings. The end-to-end proof is db-side:
	// TaskRecord.state='success' (proves handler ran to completion),
	// db.Task.song_name populated (proves tag.Write emitted TIT2), and
	// TaskRecord.tag_source (proves plugin → candidate → write path).
}

// ─── Test 3: YouTubeDownload ───────────────────────────────────────────────

func TestIntegration_YouTubeDownload_FakeYtdlp(t *testing.T) {
	rig := newTestRig(t)

	root := rig.MusicRoot
	downloadsDir := filepath.Join(root, "downloads")
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}

	// Fake yt-dlp: emit a small mp3 file when invoked, derived from the URL.
	// Honours the standard yt-dlp arg layout used by yt_dl.go:
	//   args := [..., "-f", format, "-o", outTpl, "--no-part", ...]
	// The URL is the last positional arg.
	fakeBin := filepath.Join(root, "fake-yt-dlp.sh")
	script := `#!/bin/bash
set -e
prev=""
out=""
url="https://www.youtube.com/watch?v=fallback"
for arg in "$@"; do
    if [ "$prev" = "-o" ]; then
        out="$arg"
    fi
    case "$arg" in
        http*|https*) url="$arg" ;;
    esac
    prev="$arg"
done
vid="$(echo "$url" | sed -E 's/.*v=([A-Za-z0-9_\-]+).*/\1/')"
dir="$(dirname "$out")"
mkdir -p "$dir"
# Replace %(id)s and %(ext)s placeholders.
out_path="$(echo "$out" | sed -E "s/%\\(id\\)s/${vid}/g; s/%\\(ext\\)s/mp3/g")"
printf 'ID3\x04\x00\x00\x00\x00\x00\x00FAKE-AUDIO' > "$out_path"
echo "[fake-yt-dlp] wrote $out_path"
exit 0
`
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}

	const videoID = "ytFake001"

	// Consumer registers YouTubeDownloadHandler pointed at fake binary.
	rig.Start(t, func(mux *asynq.ServeMux) {
		tasks.NewYouTubeDownloadMux(mux, &tasks.YouTubeDownloadHandler{
			DB:        rig.DB,
			MusicRoot: root,
			YTDLPPath: fakeBin,
		})
	})

	// Producer enqueues the YouTubeDownload task.
	rig.Enqueue(t, tasks.TypeYouTubeDownload, &tasks.YouTubeDownloadPayload{
		VideoID: videoID,
	})

	rig.WaitForCompletion(t, 8*time.Second)

	// Assert db.Folder row written
	var folder db.Folder
	if err := rig.DB.Where("uid = ?", videoID).First(&folder).Error; err != nil {
		t.Fatalf("Find Folder: %v", err)
	}
	if folder.FileType != "youtube" {
		t.Errorf("Folder.file_type=%q (want 'youtube')", folder.FileType)
	}
	if filepath.Base(folder.Path) != videoID+".mp3" {
		t.Errorf("Folder.path=%q (want basename %q)", folder.Path, videoID+".mp3")
	}

	// Assert db.TaskRecord row written
	var rec db.TaskRecord
	if err := rig.DB.Where("uid = ?", videoID).First(&rec).Error; err != nil {
		t.Fatalf("Find TaskRecord: %v", err)
	}
	if rec.Status != "completed" {
		t.Errorf("TaskRecord.status=%q (want 'completed')", rec.Status)
	}
	if rec.FileType != "youtube" {
		t.Errorf("TaskRecord.file_type=%q (want 'youtube')", rec.FileType)
	}
	if rec.FullPath == "" {
		t.Error("TaskRecord.full_path should be populated")
	}
}

// ─── helpers ───────────────────────────────────────────────────────────────

func summarize(rows []db.Folder) string {
	out := "["
	for i, r := range rows {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("{path=%q type=%q}", r.Path, r.FileType)
	}
	return out + "]"
}

// ─── Test 4: ClearMusic ─────────────────────────────────────────────────────

func TestIntegration_ClearMusic_TruncatesAllTables(t *testing.T) {
	rig := newTestRig(t)

	type seed struct {
		name  string
		model interface{}
	}
	seeds := []seed{
		{"Folder", &db.Folder{UID: uuid.New().String(), Path: filepath.Join(rig.MusicRoot, "a.mp3"), FileType: "music", Name: "a.mp3"}},
		{"Task", &db.Task{FullPath: filepath.Join(rig.MusicRoot, "a.mp3"), State: "success", SongName: "A", ParentPath: rig.MusicRoot, Filename: "a.mp3"}},
		{"TaskRecord", &db.TaskRecord{FullPath: filepath.Join(rig.MusicRoot, "a.mp3"), State: "wait", Batch: "clear-batch"}},
		{"Track", &db.Track{Path: filepath.Join(rig.MusicRoot, "a.mp3"), Name: "A", Year: 2020}},
		{"Album", &db.Album{Name: "Clr Album", FullText: "Clr Album"}},
		{"Artist", &db.Artist{Name: "Clr Artist"}},
		{"Genre", &db.Genre{Name: "Clr Genre"}},
		{"Attachment", &db.Attachment{URL: "http://example/cover.jpg", Mime: "image/jpeg"}},
	}
	for _, s := range seeds {
		if err := rig.DB.Create(s.model).Error; err != nil {
			t.Fatalf("seed %s: %v", s.name, err)
		}
	}

	// Pre-condition: each table has ≥1 row.
	for _, s := range seeds {
		var n int64
		if err := rig.DB.Model(s.model).Count(&n).Error; err != nil {
			t.Fatalf("count %s: %v", s.name, err)
		}
		if n == 0 {
			t.Fatalf("seed %s inserted 0 rows", s.name)
		}
	}

	rig.Start(t, func(mux *asynq.ServeMux) {
		tasks.NewClearMusicMux(mux, &tasks.ClearMusicHandler{
			DB:       rig.DB,
			DBDriver: "sqlite",
		})
	})

	// ClearMusicPayload is empty; pass nil and the asynq adapter will
	// default to &ClearMusicPayload{} (handler ignores fields).
	rig.Enqueue(t, tasks.TypeClearMusic, nil)

	rig.WaitForCompletion(t, 8*time.Second)

	// Post-condition: every table is empty.
	for _, s := range seeds {
		var n int64
		if err := rig.DB.Model(s.model).Count(&n).Error; err != nil {
			t.Fatalf("count %s after clear: %v", s.name, err)
		}
		if n != 0 {
			t.Errorf("clear: %s still has %d rows (want 0)", s.name, n)
		}
	}
}

// ─── Test 5: TidyFolder ─────────────────────────────────────────────────────

func TestIntegration_TidyFolder_RenamesFile(t *testing.T) {
	rig := newTestRig(t)

	// Set up an mp3 file with REAL ID3v2 frames (bogem/id3v2 writes a
	// complete, parseable file — dhowden/tag.ReadFrom can then return a
	// fully-populated TagInfo so TidyFolderHandler.pickAttr resolves).
	srcDir := filepath.Join(rig.MusicRoot, "old_dir")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	srcPath := filepath.Join(srcDir, "song.mp3")
	if err := os.WriteFile(srcPath, []byte("ID3\x04\x00\x00\x00\x00\x00\x00"), 0o644); err != nil {
		t.Fatalf("init mp3: %v", err)
	}
	tag0, openErr := id3v2.Open(srcPath, id3v2.Options{Parse: true})
	if openErr != nil {
		t.Fatalf("id3v2.Open: %v", openErr)
	}
	tag0.AddTextFrame("TIT2", id3v2.EncodingUTF8, "Song")
	tag0.AddTextFrame("TALB", id3v2.EncodingUTF8, "Greatest Hits")
	tag0.AddTextFrame("TPE1", id3v2.EncodingUTF8, "Test Artist")
	tag0.AddTextFrame("TRCK", id3v2.EncodingUTF8, "3")
	if err := tag0.Save(); err != nil {
		t.Fatalf("id3v2.Save: %v", err)
	}
	if err := tag0.Close(); err != nil {
		t.Logf("id3v2.Close warning: %v", err)
	}
	// Sanity: confirm dhowden/tag reads our just-written frames back. If
	// this fails, the test fails with a clear hint rather than a misleading
	// "file went to 未知/" assertion downstream.
	if parsed, perr := tag.Read(srcPath); perr != nil {
		t.Fatalf("tag.Read after id3v2.Save: %v", perr)
	} else {
		t.Logf("parsed tags for tidy input: %+v", parsed)
		if parsed.Album != "Greatest Hits" {
			t.Fatalf("parsed.Album=%q (want %q) — TidyFolder test cannot proceed", parsed.Album, "Greatest Hits")
		}
	}

	// Seed db.Folder + db.Track row pointing at the old path.
	folderUID := uuid.New().String()
	folderBefore := db.Folder{
		UID: folderUID, ParentID: "",
		Name: "song.mp3", Path: srcPath,
		FileType: "music", State: "none",
	}
	if err := rig.DB.Create(&folderBefore).Error; err != nil {
		t.Fatalf("seed Folder: %v", err)
	}
	trackBefore := db.Track{
		Path: srcPath, Name: "Song",
		Year: 2020, Suffix: "mp3", Mime: "audio/mpeg", Size: 8,
		FullText: "Song",
	}
	if err := rig.DB.Create(&trackBefore).Error; err != nil {
		t.Fatalf("seed Track: %v", err)
	}

	// ── Pub/Sub flow under test ──
	//   handler publish  ─→  NullBus records  ─→  cache subscriber invalidates
	//
	// We pre-seed PathCache with both old and new entries to prove the
	// subscriber actually clears them (a no-op subscriber wouldn't change
	// the Size, so the size=0 assertion is the real proof).
	nullBus := events.NewNullBus()
	t.Cleanup(nullBus.Reset)
	pathCache := cache.NewPathCache()
	pathCache.Set(cache.TrackEntry{Path: srcPath, Name: "song.mp3"})
	// Pre-populate the new path too — proves HandleFileMoved tombstones
	// stale entries that may have leaked from a prior test run.
	pathCache.Set(cache.TrackEntry{Path: filepath.Join(rig.MusicRoot, "Greatest Hits", "song.mp3"), Name: "song.mp3"})
	if pathCache.Size() != 2 {
		t.Fatalf("test rig: expected 2 pre-seeded cache entries, got %d", pathCache.Size())
	}

	// Start a subscriber (mirroring cmd/gateway/main.go's StartSubscriber).
	subCtx, subCancel := context.WithCancel(context.Background())
	t.Cleanup(subCancel)
	go func() {
		ch, cancelSub, err := nullBus.Subscribe(subCtx, events.TopicFileMoved)
		if err != nil {
			return
		}
		defer cancelSub()
		for data := range ch {
			var e events.FileMovedEvent
			if err := json.Unmarshal(data, &e); err != nil {
				continue
			}
			pathCache.HandleFileMoved(e.OldPath, e.NewPath)
		}
	}()

	rig.Start(t, func(mux *asynq.ServeMux) {
		tasks.NewTidyFolderMux(mux, &tasks.TidyFolderHandler{
			DB:        rig.DB,
			MusicRoot: rig.MusicRoot,
			Bus:       nullBus,
		})
	})

	// Producer enqueues a tidy: group by album → root/{Album}/file.
	rig.Enqueue(t, tasks.TypeTidyFolder, &tasks.TidyFolderPayload{
		MusicPaths: []string{srcPath},
		RootPath:   rig.MusicRoot,
		FirstDir:   "album",
	})

	rig.WaitForCompletion(t, 8*time.Second)

	expectedDst := filepath.Join(rig.MusicRoot, "Greatest Hits", "song.mp3")

	// fs: file is at the new path; old path is gone
	if _, err := os.Stat(expectedDst); err != nil {
		t.Errorf("expected dst %q does not exist (rename failed): %v", expectedDst, err)
	}
	if _, err := os.Stat(srcPath); err == nil {
		t.Errorf("original src %q still exists (rename incomplete)", srcPath)
	}

	// db: Folder row's path is updated
	var folderAfter db.Folder
	if err := rig.DB.Where("uid = ?", folderUID).First(&folderAfter).Error; err != nil {
		t.Fatalf("Find Folder by uid: %v", err)
	}
	if folderAfter.Path != expectedDst {
		t.Errorf("db.Folder.path=%q (want %q)", folderAfter.Path, expectedDst)
	}
	if filepath.Base(folderAfter.Path) != "song.mp3" {
		t.Errorf("db.Folder.name basename=%q (want %q)", filepath.Base(folderAfter.Path), "song.mp3")
	}

	// db: Track row's path is updated
	var trackAfter db.Track
	if err := rig.DB.First(&trackAfter, "id = ?", trackBefore.ID).Error; err != nil {
		t.Fatalf("Find Track after rename: %v", err)
	}
	if trackAfter.Path != expectedDst {
		t.Errorf("db.Track.path=%q (want %q)", trackAfter.Path, expectedDst)
	}

	// ── Pub/Sub assertions ──
	// 1. handler published exactly one FileMoved event with the expected
	//    old/new paths.
	got := nullBus.Snapshot()
	var fileMoved []events.FileMovedEvent
	for _, r := range got {
		if r.Topic != events.TopicFileMoved {
			continue
		}
		var e events.FileMovedEvent
		if err := json.Unmarshal(r.Payload, &e); err == nil {
			fileMoved = append(fileMoved, e)
		}
	}
	if len(fileMoved) != 1 {
		t.Fatalf("expected 1 FileMoved event, got %d (full snapshot=%v)", len(fileMoved), summarizeRecorded(got))
	}
	ev := fileMoved[0]
	if ev.OldPath != srcPath {
		t.Errorf("FileMoved.OldPath=%q (want %q)", ev.OldPath, srcPath)
	}
	if ev.NewPath != expectedDst {
		t.Errorf("FileMoved.NewPath=%q (want %q)", ev.NewPath, expectedDst)
	}
	if ev.Action != "renamed" {
		t.Errorf("FileMoved.Action=%q (want 'renamed')", ev.Action)
	}

	// 2. cache subscriber invalidated both seeded entries. Allow up to 500ms
	//    for the goroutine to drain the in-process channel; in practice it's
	//    a synchronous fan-out but the goroutine boundary makes this
	//    technically async.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if pathCache.Size() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if size := pathCache.Size(); size != 0 {
		t.Errorf("PathCache.Size=%d (want 0 after FileMoved invalidation)", size)
	}
	if _, ok := pathCache.Get(srcPath); ok {
		t.Error("PathCache still has entry for old path after FileMoved")
	}
	if _, ok := pathCache.Get(expectedDst); ok {
		t.Error("PathCache still has entry for new path after FileMoved (should be tombstoned)")
	}
}

func summarizeRecorded(rs []events.Recorded) string {
	out := "["
	for i, r := range rs {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("{topic=%q payload=%s}", r.Topic, string(r.Payload))
	}
	return out + "]"
}
