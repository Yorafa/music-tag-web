package tasks_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"go-music-tag/internal/db"
	"go-music-tag/internal/plugin"
	"go-music-tag/internal/tasks"
)

// The 加入库 path, and the one that was broken.
//
// A download with no DestDir lands in AUDIO_CACHE_DIR and the library copy
// is a separate, later act. With DestDir set, runTagSource copies the file
// into MUSIC_DIR and writes its index row from a *different* code path — one
// that had its own `FileType: payload.Source` and was not covered by the
// download test, so the file_type bug survived there even after being fixed
// in the sibling branch.
//
// That matters because the two rows are what duplicate detection reads. A
// track that reached the library this way is exactly the one that used to be
// invisible to the fingerprint stage.
func TestIntegration_DownloadGeneric_IntoLibraryWritesMusicFileType(t *testing.T) {
	rig := newTestRig(t)
	root := rig.MusicRoot

	cacheRoot := filepath.Join(root, "audio_cache")
	t.Setenv("AUDIO_CACHE_DIR", cacheRoot)
	if err := os.MkdirAll(filepath.Join(cacheRoot, "youtube"), 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	mock := &mockDownloadSource{name: "youtube"}
	plugin.InstallMockDownloadSource("youtube", mock)
	t.Cleanup(func() { plugin.UninstallMockDownloadSource("youtube") })

	const videoID = "ytLib001"

	rig.Start(t, func(mux *asynq.ServeMux) {
		tasks.NewDownloadGenericMux(mux, tasks.NewDownloadHandler(rig.DB, root))
	})

	rig.Enqueue(t, tasks.TypeDownloadGeneric, &tasks.DownloadPayload{
		Source:  "youtube",
		VideoID: videoID,
		// Relative to the music root — the "加入库" case.
		DestDir: "Artist/Album",
	})

	rig.WaitForCompletion(t, 8*time.Second)

	var folder db.Folder
	if err := rig.DB.Where("uid = ?", videoID).First(&folder).Error; err != nil {
		t.Fatalf("Find Folder: %v", err)
	}

	// The file really is in the library, not still in the cache.
	if filepath.Dir(folder.Path) != filepath.Join(root, "Artist", "Album") {
		t.Errorf("Folder.path = %q, want it under %q", folder.Path,
			filepath.Join(root, "Artist", "Album"))
	}
	if _, err := os.Stat(folder.Path); err != nil {
		t.Errorf("the library copy is not on disk: %v", err)
	}

	// file_type describes the file, not where it came from. The download
	// source is in TaskRecord.source and the audit log. A 'youtube' here is
	// what made every downloaded library track invisible to the duration
	// indexer, and therefore to the fingerprint stage's candidate query.
	if folder.FileType != "music" {
		t.Errorf("Folder.file_type = %q, want \"music\" — a downloaded track in the "+
			"library must be classifiable as audio or duplicate detection cannot see it",
			folder.FileType)
	}
}
