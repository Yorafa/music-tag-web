package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func runPrune(t *testing.T, p PruneAudioCachePayload) {
	t.Helper()
	h := &PruneAudioCacheHandler{}
	if err := h.ProcessTask(context.Background(), Task{
		Type:    TypePruneAudioCache,
		Payload: &p,
	}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
}

func TestPruneAudioCache_EnforcesTheCap(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AUDIO_CACHE_DIR", root)
	write := func(name string, size int, age time.Duration) string {
		t.Helper()
		p := filepath.Join(root, "youtube", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(-age)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := write("old.ogg", 100, 2*time.Hour)
	mid := write("mid.ogg", 100, time.Hour)

	runPrune(t, PruneAudioCachePayload{MaxBytes: 150})

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("oldest should be pruned, err = %v", err)
	}
	if _, err := os.Stat(mid); err != nil {
		t.Fatalf("the file that brought it under the cap must stay: %v", err)
	}
}

// The scheduler runs this every 30 minutes forever. A run that found
// nothing to do has to be a no-op, not "delete the cache because it is
// under the cap".
func TestPruneAudioCache_UnderCapDeletesNothing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AUDIO_CACHE_DIR", root)
	p := filepath.Join(root, "youtube", "a.ogg")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}

	runPrune(t, PruneAudioCachePayload{MaxBytes: 1024 * 1024})

	if _, err := os.Stat(p); err != nil {
		t.Fatalf("under-cap file must survive: %v", err)
	}
}

// A download that landed a minute ago is what a size cap must not delete:
// /api/stream is serving it, or a task is about to copy it into the
// library. The run is allowed to leave the cache over the cap.
func TestPruneAudioCache_ProtectsRecentFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AUDIO_CACHE_DIR", root)
	p := filepath.Join(root, "youtube", "fresh.ogg")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}

	runPrune(t, PruneAudioCachePayload{MaxBytes: 1, MinAgeMinutes: 30})

	if _, err := os.Stat(p); err != nil {
		t.Fatalf("recent file must survive: %v", err)
	}
}

func TestPruneAudioCache_MissingCacheDirIsFine(t *testing.T) {
	t.Setenv("AUDIO_CACHE_DIR", filepath.Join(t.TempDir(), "absent"))
	runPrune(t, PruneAudioCachePayload{MaxBytes: 1})
}
