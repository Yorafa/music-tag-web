package tasks

import "log"

// Telling the indexer that the library changed.
//
// The duration/fingerprint index is what makes the fingerprint stage of
// duplicate detection work, and it is built by decoding audio — so it cannot
// be maintained by the scanners, which only stat files. Something has to
// notice that new library files arrived.
//
// Two mechanisms, because neither alone is sufficient:
//
//   - The indexer re-arms itself after a run that made progress. This is
//     what makes the index converge on a large library without polling.
//   - The producers (scan, download) call OnLibraryChanged. This is what
//     makes the index *prompt*.
//
// The re-arm alone was tried first and has a hole: once the index catches up
// the chain stops, so a file added afterwards waits for the next worker
// restart. On the first deployment of this, a track added a minute after the
// chain went idle still had no duration an hour later. A chain can only
// converge on work that arrives while it is running.
//
// Polling instead would close the hole but wakes the worker forever on an
// unchanged library, and a 2000-track library re-checks 2000 stat calls every
// tick for no benefit.
//
// So the two producers that can add library files call the hook, and the
// re-arm remains as the safety net for anything they miss — a file copied in
// by hand, say, which produces no event at all.

// OnLibraryChanged is invoked after a task that may have added files to the
// library. The worker wires it to the index queue; tests leave it nil.
//
// A nil hook is the normal case outside the worker's wiring, so every caller
// must treat it as optional rather than checking at each call.
type LibraryChangedHook func()

// notifyLibraryChanged fires the hook, swallowing a panic.
//
// A panic in a queue-plumbing callback must not fail the scan or download
// that triggered it: the file is already on disk and already indexed as far
// as the scanner is concerned. The index is an optimisation layered on top.
func notifyLibraryChanged(h LibraryChangedHook) {
	if h == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[library-changed] hook panicked: %v", r)
		}
	}()
	h()
}
