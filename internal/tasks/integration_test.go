// Integration tests for the P1 asynq worker.
//
// Covers producer → consumer end-to-end for two task types:
//
//   • TypeFullScanFolder  ("scan:full")
//   • TypeDownloadGeneric ("download:generic") — youtube branch via fake yt-dlp
//
// Per test, a *testRig bootstraps:
//   1. miniredis (in-process Redis, no Docker required for CI)
//   2. sqlite (in-memory database via gorm.io/driver/sqlite)
//   3. asynq producer (Client) and consumer (Server + Inspector)
//
// Skips testcontainers-go/Redis for portability: miniredis speaks the same
// go-redis wire protocol asynq uses; tests run anywhere `go test` does.
//
// DownloadGeneric (youtube branch) uses a fake shell script to inject
// yt-dlp behavior — no real binary required.

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
	"go-music-tag/internal/utils"

	"go-music-tag/internal/testaudio"
)

// ─── TagSource mock ──────────────────────────────────────────────────────────

type mockTagSource struct {
	name        string
	fetchResult []plugin.Song
}

func (m *mockTagSource) Name() string           { return m.name }
func (m *mockTagSource) DisplayName() string    { return "Mock " + m.name }
func (m *mockTagSource) SupportsSearch() bool   { return false }
func (m *mockTagSource) SupportsLyric() bool    { return false }
func (m *mockTagSource) SupportsId3() bool      { return true }
func (m *mockTagSource) SupportsAudioURL() bool { return false } // mock is metadata-only; integration test doesn't exercise playback
func (m *mockTagSource) Search(_ context.Context, _ string, _, _ int) (*plugin.SearchResult, error) {
	return &plugin.SearchResult{}, nil
}
func (m *mockTagSource) FetchID3ByTitle(_ context.Context, _ string) ([]plugin.Song, error) {
	out := make([]plugin.Song, 0, len(m.fetchResult))
	for _, s := range m.fetchResult {
		s := s
		s.Source = m.name
		out = append(out, s)
	}
	return out, nil
}
func (m *mockTagSource) FetchLyric(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *mockTagSource) GetAudioURL(_ context.Context, _ string) (string, error) {
	return "", nil // mock is metadata-only; integration test doesn't probe the stream path
}

// mockDownloadSource is the DownloadSource double for the download:generic
// integration test. Since the 2026 refactor the worker's youtube branch
// delegates the actual exec to the plugin over the DownloadSource interface
// (the plugin runs yt-dlp); this mock mimics that contract: it records the
// DownloadOptions the worker forwarded, then lands a fake <id>.mp3 into
// AUDIO_CACHE_DIR/youtube (the shared cache dir the gateway globs) and
// reports Success with the written path — exactly what the youtube plugin's
// gRPC Download returns.
type mockDownloadSource struct {
	name    string
	gotOpts plugin.DownloadOptions
}

func (m *mockDownloadSource) Name() string        { return m.name }
func (m *mockDownloadSource) DisplayName() string { return "Mock " + m.name }
func (m *mockDownloadSource) Search(_ context.Context, _ string, _ int) ([]plugin.DownloadItem, error) {
	return nil, nil
}
func (m *mockDownloadSource) Download(_ context.Context, videoID, _ string, opts plugin.DownloadOptions) (*plugin.DownloadResult, error) {
	m.gotOpts = opts
	dir := filepath.Join(os.Getenv("AUDIO_CACHE_DIR"), m.name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &plugin.DownloadResult{Success: false, Error: err.Error()}, nil
	}
	path := filepath.Join(dir, videoID+".mp3")
	if err := os.WriteFile(path, []byte("ID3\x04\x00\x00\x00\x00\x00\x00FAKE-AUDIO"), 0o644); err != nil {
		return &plugin.DownloadResult{Success: false, Error: err.Error()}, nil
	}
	return &plugin.DownloadResult{Success: true, FilePath: path}, nil
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
	// A real decodable MP3. The old "ID3 header + FAKE-FRAMES" stub worked
	// only because dhowden dispatched on the extension; tag.Read now
	// requires a real audio stream; a synthetic blob would be rejected
	// by the scanner rather than exercise the bookkeeping under test.
	mp3Path := testaudio.SeedMP3(t, musicDir, "track1.mp3")

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

// ─── Test 2: DownloadGeneric (youtube branch, plugin delegation) ────────────

func TestIntegration_DownloadGeneric_YouTube_MockPlugin(t *testing.T) {
	rig := newTestRig(t)

	root := rig.MusicRoot
	// The worker delegates the exec to the registered DownloadSource, which
	// lands files in audioCacheDir("youtube") = $AUDIO_CACHE_DIR/youtube —
	// the same shared cache dir the gateway /api/stream globs.
	cacheRoot := filepath.Join(root, "audio_cache")
	t.Setenv("AUDIO_CACHE_DIR", cacheRoot)
	if err := os.MkdirAll(filepath.Join(cacheRoot, "youtube"), 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	// Install the DownloadSource mock under the production registry key so
	// DownloadHandler's plugin.GetDownloadSource("youtube") dispatch hits it.
	mock := &mockDownloadSource{name: "youtube"}
	plugin.InstallMockDownloadSource("youtube", mock)
	t.Cleanup(func() { plugin.UninstallMockDownloadSource("youtube") })

	const videoID = "ytFake001"

	// Consumer registers the unified DownloadHandler (no yt-dlp anywhere —
	// the exec lives in the plugin, which this mock stands in for).
	rig.Start(t, func(mux *asynq.ServeMux) {
		tasks.NewDownloadGenericMux(mux, tasks.NewDownloadHandler(rig.DB, root))
	})

	// Producer enqueues the unified download:generic task with explicit
	// tuning knobs so we can assert they survive the worker → plugin hop.
	rig.Enqueue(t, tasks.TypeDownloadGeneric, &tasks.DownloadPayload{
		Source:    "youtube",
		VideoID:   videoID,
		ExtraJSON: `{"format":"bestaudio[height<=480]","output_format":"mp3","quality":"320"}`,
	})

	rig.WaitForCompletion(t, 8*time.Second)

	// The worker sanitized ExtraJSON and forwarded the knobs to the plugin.
	wantOpts := plugin.DownloadOptions{Format: "bestaudio[height<=480]", OutputFormat: "mp3", Quality: "320"}
	if mock.gotOpts != wantOpts {
		t.Errorf("DownloadOptions forwarded to plugin = %+v, want %+v", mock.gotOpts, wantOpts)
	}

	// Assert db.Folder row written
	var folder db.Folder
	if err := rig.DB.Where("uid = ?", videoID).First(&folder).Error; err != nil {
		t.Fatalf("Find Folder: %v", err)
	}
	// file_type describes WHAT the file is, and an .mp3 is 'music' whoever
	// fetched it. It used to be the download source ('youtube'), which put
	// two vocabularies in one column: the folder scanner writes
	// 'music'/'image'/'folder', the downloader wrote a source name. Every
	// query that identified library audio by `file_type = 'music'` then
	// excluded every downloaded track — so it got no duration, was never a
	// candidate for anyone else's fingerprint comparison, and a re-encoded
	// twin of it went undetected. The download source is not lost: it is in
	// TaskRecord.source, in the audit log, and in the cache path itself.
	//
	// Whether a row is in the *library* (as opposed to the per-source
	// download cache) is decided by root containment, not by this column —
	// see internal/dedup/audiotable.go.
	if folder.FileType != "music" {
		t.Errorf("Folder.file_type=%q (want 'music')", folder.FileType)
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
	if rec.FileType != "music" {
		t.Errorf("TaskRecord.file_type=%q (want 'music')", rec.FileType)
	}
	if rec.FullPath == "" {
		t.Error("TaskRecord.full_path should be populated")
	}
}

// ─── Test 4: ApplyParsedFilenames (C.2 bulk-apply worker) ───────────────────

func TestIntegration_ApplyParsedFilenames_WritesTags(t *testing.T) {
	rig := newTestRig(t)

	// The handler re-roots every row.Path under utils.MusicRoot()
	// (= $MUSIC_DIR), so the fixture must live under the rig root and the
	// env var must point at it.
	t.Setenv("MUSIC_DIR", rig.MusicRoot)

	// A real decodable MP3 carrying real ID3v2 tags. tag.Write now
	// requires an actual audio stream (an "ID3" magic with no frames is
	// rejected), so the old header-only stub could not exercise the
	// overwrite path at all.
	srcPath := testaudio.SeedMP3(t, rig.MusicRoot, "Song - Original.mp3")
	if err := tag.Write(srcPath, &tag.TagUpdate{
		Title:  ptr("Original"),
		Artist: []string{"Old Artist"},
	}); err != nil {
		t.Fatalf("seed tags: %v", err)
	}

	// Consumer registers the C.2 bulk-apply worker — the exact wiring that
	// was missing before round-11 (TypeApplyParsedFilenames had no
	// consumer, so the gateway's apply flow was dead).
	rig.Start(t, func(mux *asynq.ServeMux) {
		tasks.NewApplyParsedFilenamesMux(mux, tasks.HandlerFunc(tasks.HandleApplyParsedFilenames))
	})

	// Producer enqueues one parsed row: new artist + title for the file
	// (mirrors handler.ApplyParsedFilenames's enqueue shape).
	rig.Enqueue(t, tasks.TypeApplyParsedFilenames, &tasks.ApplyParsedFilenamesPayload{
		Results: []cache.ParsedResult{{
			Path:   srcPath,
			Artist: "New Artist",
			Title:  "New Title",
			Status: utils.StatusOK,
		}},
	})

	rig.WaitForCompletion(t, 8*time.Second)

	// The tag must have been overwritten — this is the real proof the task
	// was consumed (queue-empty alone would also pass on retry/archive).
	parsed, rerr := tag.Read(srcPath)
	if rerr != nil {
		t.Fatalf("tag.Read after apply: %v", rerr)
	}
	if parsed.Title != "New Title" {
		t.Errorf("title after apply = %q, want %q", parsed.Title, "New Title")
	}
	if parsed.Artist != "New Artist" {
		t.Errorf("artist after apply = %q, want %q", parsed.Artist, "New Artist")
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
		Segments:   []string{"${album}"},
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

// ptr is a local helper for building *string TagUpdate fields in fixtures.
func ptr[T any](v T) *T { return &v }
