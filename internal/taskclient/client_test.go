package taskclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
)

// withRedis points the singleton at an in-process Redis and returns its
// address. Reset is deferred so the next test starts from a clean slate —
// the package keeps client/inspector in globals precisely so tests can swap
// REDIS_ADDR between cases.
func withRedis(t *testing.T) string {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	t.Setenv("REDIS_ADDR", mr.Addr())
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "0")
	Reset()
	t.Cleanup(Reset)
	return mr.Addr()
}

// TestEnqueue_LandsInTheQueue is the basic contract every handler relies
// on: a task handed to Enqueue is really in the queue the worker reads.
func TestEnqueue_LandsInTheQueue(t *testing.T) {
	withRedis(t)

	info, err := Enqueue(asynq.NewTask("scan:full", []byte(`{}`)))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if info == nil || info.ID == "" {
		t.Fatalf("Enqueue returned no task id: %+v", info)
	}
	queued, err := Inspector().GetTaskInfo("default", info.ID)
	if err != nil {
		t.Fatalf("task %s is not in the default queue: %v", info.ID, err)
	}
	if queued.Type != "scan:full" {
		t.Errorf("queued type = %q, want scan:full", queued.Type)
	}
}

// TestEnqueue_RejectsNilTask pins the guard. asynq happens to reject a nil
// task too, so this is not about the outcome but about where the refusal
// happens: the check belongs in this package, before anything is handed to
// a library, and it should say so. The assertion is on the message for
// exactly that reason — a bare "did it error" check would pass with the
// guard deleted.
func TestEnqueue_RejectsNilTask(t *testing.T) {
	withRedis(t)

	if _, err := Enqueue(nil); err == nil || !strings.Contains(err.Error(), "nil task") {
		t.Errorf("Enqueue(nil) error = %v, want a 'nil task' refusal from this package", err)
	}
	if _, err := EnqueueContext(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "nil task") {
		t.Errorf("EnqueueContext(ctx, nil) error = %v, want a 'nil task' refusal", err)
	}
}

// TestEnqueueContext_RespectsCancellation: a request whose client already
// gave up must not leave a task in the queue for the worker to pick up.
func TestEnqueueContext_RespectsCancellation(t *testing.T) {
	withRedis(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	info, err := EnqueueContext(ctx, asynq.NewTask("scan:full", []byte(`{}`)))
	if err == nil {
		t.Fatalf("EnqueueContext on a cancelled context succeeded (task %s)", info.ID)
	}
	if n := countPending(t, "default"); n != 0 {
		t.Errorf("%d task(s) queued despite the cancelled context", n)
	}
}

// TestCancelAll_ClearsEveryQueue backs /api/clear_celery/. The queue names
// here are a hand-maintained list in CancelAll, and a typo there means the
// operator clicks "clear" and a queue silently survives.
func TestCancelAll_ClearsEveryQueue(t *testing.T) {
	withRedis(t)

	for _, q := range []string{"default", "critical", "low"} {
		opts := make([]asynq.Option, 0, 1)
		if q != "default" {
			opts = append(opts, asynq.Queue(q))
		}
		for i := 0; i < 2; i++ {
			if _, err := Enqueue(asynq.NewTask("scan:full", []byte(`{}`)), opts...); err != nil {
				t.Fatalf("enqueue to %s: %v", q, err)
			}
		}
	}

	n, err := CancelAll()
	if err != nil {
		t.Fatalf("CancelAll: %v", err)
	}
	if n != 6 {
		t.Errorf("CancelAll reported %d, want 6", n)
	}
	for _, q := range []string{"default", "critical", "low"} {
		if n := countPending(t, q); n != 0 {
			t.Errorf("queue %s still has %d pending task(s)", q, n)
		}
	}
}

// TestInit_IsIdempotentAndSingleton pins the lazy-init contract: repeated
// Init (or a concurrent Client()/Inspector()) must not build a second
// client, because each one holds its own connection pool.
func TestInit_IsIdempotentAndSingleton(t *testing.T) {
	withRedis(t)

	Init()
	first := Client()
	Init()
	Init()
	if Client() != first {
		t.Error("Init() built a second client; each holds its own pool")
	}
	if Inspector() == nil {
		t.Error("Inspector() returned nil after Init()")
	}
}

// TestReset_ReReadsEnv is why Reset exists at all: the Redis address is
// read at construction time, so a test (or an operator changing REDIS_ADDR
// and restarting only part of the stack) needs a way to rebuild.
func TestReset_ReReadsEnv(t *testing.T) {
	addr := withRedis(t)
	if Client() == nil {
		t.Fatal("Client() is nil before Reset")
	}

	// A different address, unreachable. Init must pick it up rather than
	// handing back the client built against the old one.
	t.Setenv("REDIS_ADDR", "127.0.0.1:1")
	Reset()
	if Client() == nil {
		t.Fatal("Client() is nil after Reset")
	}
	// The old address is still the one miniredis is listening on, so a
	// successful enqueue now proves the client was rebuilt against the new
	// (dead) address.
	if _, err := Enqueue(asynq.NewTask("scan:full", nil)); err == nil {
		t.Error("enqueue succeeded against an unreachable Redis; the client was not rebuilt")
	}

	// And going back works.
	t.Setenv("REDIS_ADDR", addr)
	Reset()
	if _, err := Enqueue(asynq.NewTask("scan:full", nil)); err != nil {
		t.Errorf("enqueue after restoring REDIS_ADDR: %v", err)
	}
}

// TestEnqueue_UnreachableRedisReturnsErrorNotPanic: Redis being down is
// the normal failure mode of a two-process deployment, and the gateway
// must answer its HTTP request with an error rather than dying.
func TestEnqueue_UnreachableRedisReturnsErrorNotPanic(t *testing.T) {
	t.Setenv("REDIS_ADDR", "127.0.0.1:1")
	Reset()
	t.Cleanup(Reset)

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Enqueue panicked with Redis down: %v", r)
			}
		}()
		if _, err := Enqueue(asynq.NewTask("scan:full", nil)); err == nil {
			t.Error("Enqueue succeeded against a dead Redis")
		}
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Enqueue did not return within 15s with Redis down")
	}
}

// countPending reports the pending depth of a queue, treating
// ErrQueueNotFound as zero: asynq only creates the Redis key once
// something is enqueued, so "no key" is the normal state of an empty
// queue rather than an error.
func countPending(t *testing.T, q string) int {
	t.Helper()
	pending, err := Inspector().ListPendingTasks(q)
	if errors.Is(err, asynq.ErrQueueNotFound) {
		return 0
	}
	if err != nil {
		t.Fatalf("ListPendingTasks(%s): %v", q, err)
	}
	return len(pending)
}
