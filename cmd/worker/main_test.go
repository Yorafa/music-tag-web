package main

import (
	"testing"

	"github.com/hibiken/asynq"

	"go-music-tag/internal/tasks"
)

// TestWireTaskHandlers pins the one invariant that decides whether a task
// runs at all: the type the gateway enqueues must resolve to a handler on the
// worker's ServeMux.
//
// This codebase has broken that invariant twice. TypeApplyParsedFilenames
// was enqueued with no consumer, so apply tasks burned their retries into
// asynq's dead-letter queue and the parsed names never reached the tags.
// tag:batch_auto was the same shape of failure: the handler existed and was
// registered, but it read a worklist that nothing ever populated, so a
// registered handler still processed zero files. The first is caught by a
// test like this one; the second is not — it needs a handler to do real
// work — which is why the startup log now names the types it consumed
// instead of counting them.
func TestWireTaskHandlers(t *testing.T) {
	mux := asynq.NewServeMux()
	// nils are safe: the constructors only store the deps on their handler
	// struct and call mux.HandleFunc. Nothing touches the DB or the bus at
	// wire time.
	wireTaskHandlers(mux, taskHandlerDeps{MusicRoot: t.TempDir()})

	for _, typename := range []string{
		tasks.TypeFullScanFolder,
		tasks.TypeUpdateScanFolder,
		tasks.TypeTidyFolder,
		tasks.TypeDownloadGeneric,
		tasks.TypeClearMusic,
		tasks.TypePruneEmptyFolders,
		tasks.TypeApplyParsedFilenames,
	} {
		typ := typename
		t.Run(typ, func(t *testing.T) {
			h, pattern := mux.Handler(asynq.NewTask(typ, nil))
			if h == nil {
				t.Fatalf("no handler registered for %q", typ)
			}
			if pattern != typ {
				t.Errorf("resolved to pattern %q, want %q", pattern, typ)
			}
		})
	}
}
