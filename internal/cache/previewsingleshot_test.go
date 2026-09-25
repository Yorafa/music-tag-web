package cache

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestPreviewTokenIsSingleShot pins REVIEW.md P2-7.
//
// The package doc described the gate as "single-shot", but Load only read:
// the entry stayed in the map for the whole TTL, so the same token could be
// applied repeatedly, and the map grew to hold every preview a user ever
// opened.
func TestPreviewTokenIsSingleShot(t *testing.T) {
	c := newPreviewCache(10 * time.Minute)

	tok, err := c.Save(ParsedBundle{Results: []ParsedResult{{Path: "a.mp3", Title: "A"}}})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if c.Size() != 1 {
		t.Fatalf("size after save = %d, want 1", c.Size())
	}

	// First Load succeeds.
	got, err := c.Load(tok)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Title != "A" {
		t.Errorf("bundle round-trip mismatch: %+v", got)
	}

	// The entry is gone, not merely expired.
	if c.Size() != 0 {
		t.Errorf("size after load = %d, want 0 (Load must consume)", c.Size())
	}

	// Replay is refused.
	if _, err := c.Load(tok); err != ErrTokenExpired {
		t.Errorf("replayed load err = %v, want ErrTokenExpired", err)
	}
}

// TestPreviewConcurrentLoadYieldsOneWinner pins that a replay race is
// impossible, not merely unlikely: two concurrent applies of one token must
// not both receive the bundle.
func TestPreviewConcurrentLoadYieldsOneWinner(t *testing.T) {
	c := newPreviewCache(10 * time.Minute)
	tok, err := c.Save(ParsedBundle{Results: []ParsedResult{{Path: "a.mp3"}}})
	if err != nil {
		t.Fatal(err)
	}

	const racers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0

	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			if _, err := c.Load(tok); err == nil {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if winners != 1 {
		t.Errorf("%d concurrent loads succeeded, want exactly 1", winners)
	}
}

// TestPreviewExpiredIsAlsoConsumed pins that a stale entry is dropped on
// the read that reports it expired, so it cannot linger.
func TestPreviewExpiredIsAlsoConsumed(t *testing.T) {
	c := newPreviewCache(1 * time.Millisecond)
	tok, err := c.Save(ParsedBundle{Results: []ParsedResult{{Path: "a.mp3"}}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)

	if _, err := c.Load(tok); err != ErrTokenExpired {
		t.Errorf("err = %v, want ErrTokenExpired", err)
	}
	if c.Size() != 0 {
		t.Errorf("size = %d, want 0 (expired entry must be dropped too)", c.Size())
	}
}

// TestPreviewSweepReapsAbandoned pins the janitor half: a preview that is
// never applied still has to be reaped, which is why StartJanitor exists.
func TestPreviewSweepReapsAbandoned(t *testing.T) {
	c := newPreviewCache(time.Millisecond)

	const abandoned = 5
	for i := 0; i < abandoned; i++ {
		if _, err := c.Save(ParsedBundle{Results: []ParsedResult{{Path: "x.mp3"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if c.Size() != abandoned {
		t.Fatalf("size = %d, want %d", c.Size(), abandoned)
	}

	time.Sleep(5 * time.Millisecond)
	c.Sweep()

	if c.Size() != 0 {
		t.Errorf("size after sweep = %d, want 0", c.Size())
	}
}

// TestPreviewJanitorGoroutineStops pins that StartJanitor honours context
// cancellation, so it does not outlive the process in tests or on shutdown.
func TestPreviewJanitorGoroutineStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	c := newPreviewCache(time.Millisecond)
	if _, err := c.Save(ParsedBundle{Results: []ParsedResult{{Path: "x.mp3"}}}); err != nil {
		t.Fatal(err)
	}

	go func() {
		defer close(done)
		// Reap on a very short interval so the assertion does not sleep.
		runJanitor(ctx, time.Millisecond, c)
	}()

	time.Sleep(20 * time.Millisecond)
	if c.Size() != 0 {
		t.Errorf("janitor did not sweep within 20ms (size=%d)", c.Size())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("janitor goroutine did not exit after context cancel")
	}
}
