// Package taskclient is the singleton asynq producer shared by all gateway
// handlers (and any other process that just submits tasks, like a CLI admin
// tool). The worker process uses queue package directly; this is only for
// pushing tasks onto the queue.
//
// Init() is called once from gateway main; handlers then call
// taskclient.Enqueue(type, payload, opts...) to push a task.
package taskclient

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/hibiken/asynq"

	"go-music-tag/internal/queue"
)

// Singleton state guarded by muInit + initialized (was sync.Once in P1;
// P1.5 issue B/C refactor replaced it because tests need to swap REDIS_ADDR
// via t.Setenv between sub-tests, which requires tearing the singleton down
// and re-init under new opts). sync.Once can't be cleared once fired; we
// now hide the once-semantics behind a mutex + bool pair.

var (
	muInit      sync.Mutex
	initialized bool
	client      *asynq.Client
	inspector   *asynq.Inspector
)

// Client is the lazy-init, package-level asynq producer.
func Client() *asynq.Client {
	ensure()
	return client
}

// Inspector is the asynq Inspector for /api/active_queue/ + /api/clear_celery/.
func Inspector() *asynq.Inspector {
	ensure()
	return inspector
}

// Init idempotent. Safe to call concurrently (muInit).
func Init() {
	ensure()
}

// Reset closes the existing client/inspector and clears the initialized
// flag so the next Init() call re-reads queue.ClientOpts() (which reads
// REDIS_ADDR env at call time). Production code does NOT call Reset() —
// it is test-only, used between sub-tests that mutate REDIS_ADDR via
// t.Setenv. Safe to call concurrently with a running ensure() — the
// mutex serialises init/reset transitions.
func Reset() {
	muInit.Lock()
	defer muInit.Unlock()
	if !initialized {
		return
	}
	if client != nil {
		_ = client.Close()
	}
	// asynq.Inspector has no public Close; garbage-collected.
	client = nil
	inspector = nil
	initialized = false
}

// ensure runs the constructor at most once between Reset() calls. Holds
// muInit while swapping pointers; concurrent clients block briefly then
// see the freshly-initialised state.
func ensure() {
	muInit.Lock()
	defer muInit.Unlock()
	if initialized {
		return
	}
	opts := queue.ClientOpts()
	client = asynq.NewClient(opts)
	inspector = asynq.NewInspector(opts)
	log.Printf("[taskclient] initialised (redis=%s)", opts.Addr)
	initialized = true
}

// Enqueue helper. Wraps Client.Enqueue with sensible defaults; returns
// (*asynq.TaskInfo, error) from asynq. Caller can override opts.
func Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if task == nil {
		return nil, fmt.Errorf("taskclient: nil task")
	}
	c := Client()
	if c == nil {
		return nil, fmt.Errorf("taskclient: client not initialised")
	}
	return c.Enqueue(task, opts...)
}

// EnqueueContext respects ctx cancellation.
func EnqueueContext(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if task == nil {
		return nil, fmt.Errorf("taskclient: nil task")
	}
	c := Client()
	if c == nil {
		return nil, fmt.Errorf("taskclient: client not initialised")
	}
	return c.EnqueueContext(ctx, task, opts...)
}

// CancelAll cancels every in-flight task (used by /api/clear_celery/).
// asynq v0.24.x does not expose Inspector.CancelAll. Instead we delete every
// pending task in every queue we care about (best-effort walk over the known
// queues). Active tasks finish naturally.
func CancelAll() (int64, error) {
	ins := Inspector()
	if ins == nil {
		return 0, fmt.Errorf("taskclient: inspector not initialised")
	}
	var total int64
	for _, q := range []string{"default", "critical", "low"} {
		n, err := ins.DeleteAllPendingTasks(q)
		if err != nil {
			continue
		}
		total += int64(n)
	}
	return total, nil
}
