package events

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newRedisBus(t *testing.T) (*RedisBus, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	b := NewRedisBus(mr.Addr())
	return b, func() {
		_ = b.Close()
		mr.Close()
	}
}

// TestRedisBus_PublishReachesSubscriber is the cross-process contract the
// whole file-rename invalidation depends on: the worker publishes on one
// process, the gateway's subscriber sees it. A NullBus test cannot prove
// this, which is why the real bus gets one.
func TestRedisBus_PublishReachesSubscriber(t *testing.T) {
	b, cleanup := newRedisBus(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, cancelSub, err := b.Subscribe(ctx, TopicFileMoved)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancelSub()

	// Give the subscription time to register before publishing, or Redis
	// drops the message (pub/sub has no replay).
	waitForSubscribers(t, b, 1)

	want := FileMovedEvent{Action: "renamed", OldPath: "/media/a.mp3", NewPath: "/media/b.mp3"}
	if err := b.Publish(ctx, TopicFileMoved, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-ch:
		var ev FileMovedEvent
		if err := json.Unmarshal(got, &ev); err != nil {
			t.Fatalf("payload is not JSON: %v (%s)", err, got)
		}
		if ev != want {
			t.Errorf("event = %+v, want %+v", ev, want)
		}
	case <-ctx.Done():
		t.Fatal("no event within 10s")
	}
}

// TestRedisBus_TopicsAreIsolated pins that a subscriber only hears its own
// topic. It is the property NullBus used to get wrong, which let a test
// observe an event the production subscriber could never see.
func TestRedisBus_TopicsAreIsolated(t *testing.T) {
	b, cleanup := newRedisBus(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	moved, cancelMoved, err := b.Subscribe(ctx, TopicFileMoved)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancelMoved()
	waitForSubscribers(t, b, 1)

	if err := b.Publish(ctx, "file:something-else", map[string]string{"x": "y"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := b.Publish(ctx, TopicFileMoved, FileMovedEvent{Action: "moved", OldPath: "/x", NewPath: "/y"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-moved:
		var ev FileMovedEvent
		if err := json.Unmarshal(got, &ev); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		if ev.Action != "moved" {
			t.Errorf("received %+v, want the file:moved event", ev)
		}
	case <-ctx.Done():
		t.Fatal("no event within 10s")
	}
	// Nothing else should be sitting in the channel.
	select {
	case extra := <-moved:
		t.Errorf("subscriber received a foreign topic: %s", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestRedisBus_SubscribeClosesOnContextCancel pins the shutdown path: the
// gateway calls cancel on SIGTERM and must not leak the goroutine or leave
// a channel that never closes.
func TestRedisBus_SubscribeClosesOnContextCancel(t *testing.T) {
	b, cleanup := newRedisBus(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	ch, cancelSub, err := b.Subscribe(ctx, TopicFileMoved)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancelSub()
	waitForSubscribers(t, b, 1)

	cancel()
	select {
	case _, open := <-ch:
		if open {
			t.Error("channel delivered a value instead of closing")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not close within 5s of the context being cancelled")
	}
}

// TestNullBus_RecordsAndFansOut covers the test double every handler test
// relies on.
func TestNullBus_RecordsAndFansOut(t *testing.T) {
	b := NewNullBus()
	ctx := context.Background()

	ch, _, err := b.Subscribe(ctx, TopicFileMoved)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	want := FileMovedEvent{Action: "renamed", OldPath: "/a", NewPath: "/b"}
	if err := b.Publish(ctx, TopicFileMoved, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-ch:
		var ev FileMovedEvent
		if err := json.Unmarshal(got, &ev); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		if ev != want {
			t.Errorf("event = %+v, want %+v", ev, want)
		}
	default:
		t.Fatal("subscriber received nothing")
	}

	snap := b.Snapshot()
	if len(snap) != 1 || snap[0].Topic != TopicFileMoved {
		t.Fatalf("Snapshot = %+v, want one file:moved entry", snap)
	}
}

// TestNullBus_TopicsAreIsolated is the NullBus half of the RedisBus
// isolation test, and the reason NullBus now remembers each subscriber's
// topic: without it, a handler test could observe an event on a topic it
// never subscribed to and pass where production delivers nothing.
func TestNullBus_TopicsAreIsolated(t *testing.T) {
	b := NewNullBus()
	ctx := context.Background()

	moved, _, err := b.Subscribe(ctx, TopicFileMoved)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := b.Publish(ctx, "file:something-else", "noise"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case got := <-moved:
		t.Errorf("subscriber for %s received a foreign topic: %s", TopicFileMoved, got)
	default:
	}

	if err := b.Publish(ctx, TopicFileMoved, "real"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case got := <-moved:
		if string(got) != `"real"` {
			t.Errorf("payload = %s, want \"real\"", got)
		}
	default:
		t.Fatal("subscriber received nothing for its own topic")
	}
}

// TestNullBus_SnapshotIsACopy: the snapshot is handed to test assertions
// that then filter or sort it. Returning the live slice would let an
// assertion's own bookkeeping corrupt the recording.
func TestNullBus_SnapshotIsACopy(t *testing.T) {
	b := NewNullBus()
	if err := b.Publish(context.Background(), TopicFileMoved, "one"); err != nil {
		t.Fatal(err)
	}

	snap := b.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot = %+v", snap)
	}
	snap[0].Topic = "tampered"
	snap[0].Payload[0] = 'X'

	again := b.Snapshot()
	if again[0].Topic != TopicFileMoved {
		t.Errorf("topic = %q, want %q; Snapshot handed out the live slice", again[0].Topic, TopicFileMoved)
	}
	if string(again[0].Payload) != `"one"` {
		t.Errorf("payload = %s, want \"one\"; Snapshot handed out the live slice", again[0].Payload)
	}
}

// TestNullBus_ResetDetachesSubscribers pins Reset: it closes the channels
// (so a test blocked on one wakes up) and clears the recording list.
func TestNullBus_ResetDetachesSubscribers(t *testing.T) {
	b := NewNullBus()
	ctx := context.Background()
	ch, _, err := b.Subscribe(ctx, TopicFileMoved)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, TopicFileMoved, "one"); err != nil {
		t.Fatal(err)
	}

	b.Reset()

	// A closed Go channel still hands over whatever was buffered, so the
	// pre-Reset value arrives first; what matters is that the channel then
	// closes and no post-Reset publish lands in it.
	for i := 0; i < 2; i++ {
		select {
		case _, open := <-ch:
			if !open {
				return // closed as expected
			}
		case <-time.After(time.Second):
			t.Fatal("Reset did not close the subscriber channel")
		}
	}
	t.Error("channel neither closed nor drained after Reset")
	if n := len(b.Snapshot()); n != 0 {
		t.Errorf("Snapshot has %d entries after Reset", n)
	}
	// A publish after Reset must not reach the detached subscriber, and must
	// not panic on its closed channel.
	if err := b.Publish(ctx, TopicFileMoved, "two"); err != nil {
		t.Fatalf("Publish after Reset: %v", err)
	}
	if n := len(b.Snapshot()); n != 1 {
		t.Errorf("Snapshot has %d entries, want 1", n)
	}
}

// TestNullBus_ConcurrentPublishAndReset is the reason the fan-out moved
// inside the mutex. Reset closes subscriber channels while Publish is
// walking its (previously snapshotted) copy of the subscriber list, and a
// send on a channel closed in between is a panic, not a failed assertion.
//
// Iterations are bounded on purpose: an unbounded subscribe loop allocates
// 32-slot channels faster than anything drains them, and the test would
// exhaust memory long before it proved anything.
func TestNullBus_ConcurrentPublishAndReset(t *testing.T) {
	b := NewNullBus()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if _, _, err := b.Subscribe(ctx, TopicFileMoved); err != nil {
					t.Errorf("Subscribe: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				if err := b.Publish(ctx, TopicFileMoved, "x"); err != nil {
					t.Errorf("Publish: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				b.Reset()
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("concurrent Publish/Reset/Subscribe did not finish; the bus is deadlocked")
	}
}

// TestNullBus_PublishRejectsUnserializablePayload: a payload that cannot be
// JSON-encoded must be an error, not a half-published event. The webhook
// subscriber parses whatever arrives, so a truncated payload would be
// worse than none.
func TestNullBus_PublishRejectsUnserializablePayload(t *testing.T) {
	b := NewNullBus()
	if err := b.Publish(context.Background(), TopicFileMoved, make(chan int)); err == nil {
		t.Error("Publish accepted a channel, which cannot be JSON-encoded")
	}
	if n := len(b.Snapshot()); n != 0 {
		t.Errorf("a rejected publish still recorded %d event(s)", n)
	}
}

func TestRedisBus_PublishRejectsUnserializablePayload(t *testing.T) {
	b, cleanup := newRedisBus(t)
	defer cleanup()
	if err := b.Publish(context.Background(), TopicFileMoved, make(chan int)); err == nil {
		t.Error("Publish accepted a channel, which cannot be JSON-encoded")
	}
}

// waitForSubscribers blocks until the bus has n live Redis subscriptions.
// Publishing before the subscription is registered silently drops the
// message — pub/sub has no replay — which would make these tests flaky
// rather than wrong.
func waitForSubscribers(t *testing.T, b *RedisBus, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := b.rdb.PubSubNumSub(context.Background(), TopicFileMoved).Result()
		if err == nil {
			total := int64(0)
			for _, v := range got {
				total += v
			}
			if total >= n {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("only saw the subscription after 5s; publishing now would race the subscribe")
}
