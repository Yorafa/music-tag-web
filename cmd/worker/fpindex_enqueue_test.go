package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
)

// These tests pin the three ways an index run gets scheduled, because the
// differences between them are the whole design.
//
// A duplicate run is not free. pendingFiles is one indexed query, but
// staleIndexedFiles then stats every already-indexed library file, so on a
// 2000-track library a redundant run costs ~2000 syscalls to conclude there
// is nothing to do. Adding fifty tracks at once fires the producer hook fifty
// times; without a window that is fifty runs and ~100k wasted stats.
//
// They assert on fpIndexBootOptions / fpIndexFollowupOptions /
// fpIndexRearmOptions rather than on fpIndexTaskOptions. An earlier version
// tested the shared builder directly and mutation testing showed why that is
// worthless: removing the producer path's window, and restoring the boot
// seed's, both passed — because the builder accepts any (delay, unique) pair
// and the tests were asserting about the builder rather than about the paths.
// A test that passes while the wiring is wrong is worse than no test, since
// it reads as coverage.

func optionStrings(opts []asynq.Option) string {
	var b strings.Builder
	for _, o := range opts {
		b.WriteString(o.String())
		b.WriteString(" ")
	}
	return b.String()
}

func hasOption(opts []asynq.Option, want string) bool {
	for _, o := range opts {
		if o.String() == want {
			return true
		}
	}
	return false
}

func hasOptionType(opts []asynq.Option, t asynq.OptionType) bool {
	for _, o := range opts {
		if o.Type() == t {
			return true
		}
	}
	return false
}

// The producer path must dedupe. This is the fix: it previously carried no
// uniqueness window at all, on the reasoning that the indexer is idempotent
// and a redundant run is "one indexed query".
func TestFpIndexFollowupPath_IsWindowed(t *testing.T) {
	opts := fpIndexFollowupOptions()
	if !hasOption(opts, "Unique(30s)") {
		t.Errorf("producer path = %q, want a Unique(30s) window", optionStrings(opts))
	}
	// Immediate: the point of the producer hook is that a scan which just
	// found two hundred tracks does not wait for them.
	if hasOptionType(opts, asynq.ProcessAtOpt) {
		t.Errorf("producer path = %q, want it to run immediately", optionStrings(opts))
	}
}

// The boot seed must NOT be windowed.
//
// It used to take Unique(1h), to stop a restart from stacking runs. But the
// re-arm chain is seeded by this call, so a refused seed means the chain
// never starts: on the first deployment of the re-arm, the queue was empty,
// the enqueue came back ErrDuplicateTask, and a file added a minute later
// still had no duration an hour later. Refusing the seed is far worse than
// the duplicate run it was avoiding.
func TestFpIndexBootPath_HasNoUniquenessWindow(t *testing.T) {
	opts := fpIndexBootOptions()
	if hasOptionType(opts, asynq.UniqueOpt) {
		t.Errorf("boot seed = %q, want no uniqueness window: a refusal leaves the "+
			"re-arm chain with nothing to start from", optionStrings(opts))
	}
}

// The re-arm path keeps both: delayed so a burst collapses, and windowed so
// repeated progress does not stack one run per batch.
func TestFpIndexRearmPath_IsDelayedAndWindowed(t *testing.T) {
	opts := fpIndexRearmOptions()
	if !hasOption(opts, "ProcessIn(30s)") {
		t.Errorf("re-arm path = %q, want ProcessIn(30s)", optionStrings(opts))
	}
	if !hasOption(opts, "Unique(30s)") {
		t.Errorf("re-arm path = %q, want Unique(30s)", optionStrings(opts))
	}
}

// The three paths must not converge. If they did, one of the distinctions the
// design rests on would be gone, and the tests above would still pass because
// each checks a property in isolation.
func TestFpIndexPaths_AreDistinguishableFromEachOther(t *testing.T) {
	seen := map[string]string{}
	for name, opts := range map[string][]asynq.Option{
		"boot":     fpIndexBootOptions(),
		"producer": fpIndexFollowupOptions(),
		"rearm":    fpIndexRearmOptions(),
	} {
		key := optionStrings(opts)
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s schedule identically (%q); the distinction between "+
				"them is the design, not an implementation detail", name, other, key)
		}
		seen[key] = name
	}
}

// Every path must keep the queue, the retry cap and the timeout. The timeout
// matters most: the run decodes every unindexed track, so a short one kills
// it partway through a large library.
func TestFpIndexPaths_KeepTheTimeout(t *testing.T) {
	for name, opts := range map[string][]asynq.Option{
		"boot":     fpIndexBootOptions(),
		"producer": fpIndexFollowupOptions(),
		"rearm":    fpIndexRearmOptions(),
	} {
		if !hasOption(opts, `Queue("default")`) {
			t.Errorf("%s = %q, want Queue(\"default\")", name, optionStrings(opts))
		}
		if !hasOption(opts, "MaxRetry(1)") {
			t.Errorf("%s = %q, want MaxRetry(1)", name, optionStrings(opts))
		}
		if !hasOption(opts, "Timeout(30m0s)") {
			t.Errorf("%s = %q, want Timeout(30m0s)", name, optionStrings(opts))
		}
	}
}

// A refusal is the window working, not a failure. Reporting it as "could not
// enqueue" trained the reader to ignore the line — which is the same line
// that would have told them the re-arm chain had no seed.
//
// The distinction is only meaningful because the uniqueness key is deleted
// when its task reaches a terminal state, so its presence means a run exists
// right now. A Redis outage means no run exists and the index goes stale.
func TestIsBenignIndexRefusal_SeparatesRefusalFromFailure(t *testing.T) {
	if !isBenignIndexRefusal(asynq.ErrDuplicateTask) {
		t.Error("ErrDuplicateTask should be benign: it means a run already exists")
	}
	// Wrapped, because asynq returns its errors through a stack of
	// errors.E layers and a bare equality check would miss all of them.
	wrapped := fmt.Errorf("enqueue: %w", asynq.ErrDuplicateTask)
	if !isBenignIndexRefusal(wrapped) {
		t.Error("a wrapped ErrDuplicateTask should still be benign")
	}
	for _, err := range []error{
		nil,
		errors.New("connection refused"),
		asynq.ErrTaskIDConflict,
	} {
		if isBenignIndexRefusal(err) {
			t.Errorf("%v should be reported as a real failure, not a refusal", err)
		}
	}
}
