package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The hook that makes the index prompt.
//
// The indexer's re-arm converges on a library, but a chain can only pick up
// work that arrives while it is running: once it catches up it stops, so a
// file added afterwards waits for the next worker restart. On the first
// deployment of this, a track added a minute after the chain went idle still
// had no duration an hour later.
//
// So the producers — the two things that actually add library files — notify
// the index directly. These tests pin that they do.

func TestFullScan_NotifiesTheIndex(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	seedScannableFile(t, music, "a.mp3")

	calls := 0
	h := &FullScanHandler{DB: newFpIndexDB(t), MusicRoot: music, OnLibraryChanged: func() { calls++ }}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFullScanFolder}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if calls != 1 {
		t.Errorf("hook called %d times after a full scan, want 1 — without it a file "+
			"the scan just indexed sits with no duration until the next restart", calls)
	}
}

func TestUpdateScan_NotifiesTheIndex(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	seedScannableFile(t, music, "a.mp3")

	calls := 0
	h := &UpdateScanHandler{DB: newFpIndexDB(t), MusicRoot: music, OnLibraryChanged: func() { calls++ }}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeUpdateScanFolder}); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if calls != 1 {
		t.Errorf("hook called %d times after an update scan, want 1", calls)
	}
}

// A nil hook is the normal case outside the worker's wiring, so it must not
// panic — the scan has to work in tests and in any embedder that does not
// care about the index.
func TestScan_NilHookIsSafe(t *testing.T) {
	music := t.TempDir()
	t.Setenv("MUSIC_DIR", music)
	seedScannableFile(t, music, "a.mp3")

	h := &FullScanHandler{DB: newFpIndexDB(t), MusicRoot: music}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeFullScanFolder}); err != nil {
		t.Errorf("ProcessTask with a nil hook returned %v, want nil", err)
	}
}

// A panicking hook must not fail the scan. The files are already on disk and
// already in the scanner's index; the duration index is a layer on top of
// that, and it is not worth losing a scan over.
func TestNotifyLibraryChanged_SwallowsPanics(t *testing.T) {
	notifyLibraryChanged(func() { panic("queue is down") })
	// Reaching here without a test failure is the assertion.
}

// The hook fires even when the task fails partway, because the file may
// already be on disk: a download that copied the bytes and then failed to
// write its row still needs the index to hear about it.
func TestDownload_NotifiesEvenOnFailure(t *testing.T) {
	calls := 0
	h := &DownloadHandler{OnLibraryChanged: func() { calls++ }}
	// No plugins registered and an empty root, so this fails in the
	// dispatch — but after the payload validation the hook is armed.
	err := h.ProcessTask(context.Background(), Task{
		Type:    TypeDownloadGeneric,
		Payload: &DownloadPayload{Source: "no-such-source", VideoID: "x"},
	})
	if err == nil {
		t.Skip("download unexpectedly succeeded; the failure path was not exercised")
	}
	if calls != 1 {
		t.Errorf("hook called %d times on a failed download, want 1 — the bytes may "+
			"already be on disk", calls)
	}
}

func TestDownload_NilHookIsSafe(t *testing.T) {
	h := &DownloadHandler{}
	_ = h.ProcessTask(context.Background(), Task{
		Type:    TypeDownloadGeneric,
		Payload: &DownloadPayload{Source: "no-such-source", VideoID: "x"},
	})
}

// A bad payload is rejected before the hook is armed: nothing was added, so
// telling the index would be a pointless run.
func TestDownload_NoHookForAnInvalidPayload(t *testing.T) {
	calls := 0
	h := &DownloadHandler{OnLibraryChanged: func() { calls++ }}
	if err := h.ProcessTask(context.Background(), Task{Type: TypeDownloadGeneric}); err == nil {
		t.Error("a download with no payload should be rejected")
	}
	if calls != 0 {
		t.Errorf("hook called %d times for a rejected payload, want 0", calls)
	}
}

func seedScannableFile(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
