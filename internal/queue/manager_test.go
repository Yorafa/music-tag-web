package queue

import (
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// TestClientOpts_ReadsEnv pins the env wiring both the gateway (producer)
// and the worker (consumer) depend on. A typo here is invisible until a
// task silently goes to the wrong Redis or the wrong logical database.
func TestClientOpts_ReadsEnv(t *testing.T) {
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("REDIS_PASSWORD", "s3cret")
	t.Setenv("REDIS_DB", "3")

	opts := ClientOpts()
	if opts.Addr != "redis:6379" {
		t.Errorf("Addr = %q, want redis:6379", opts.Addr)
	}
	if opts.Password != "s3cret" {
		t.Errorf("Password = %q, want s3cret", opts.Password)
	}
	if opts.DB != 3 {
		t.Errorf("DB = %d, want 3", opts.DB)
	}
}

// TestClientOpts_Defaults covers the "operator set nothing" path, which is
// what a local `go run` of either binary does.
func TestClientOpts_Defaults(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "")

	opts := ClientOpts()
	if opts.Addr != "127.0.0.1:6379" {
		t.Errorf("Addr = %q, want the 127.0.0.1:6379 default", opts.Addr)
	}
	if opts.Password != "" {
		t.Errorf("Password = %q, want empty", opts.Password)
	}
	if opts.DB != 0 {
		t.Errorf("DB = %d, want 0", opts.DB)
	}
}

// TestServerOpts_MatchesClientOpts documents that there is deliberately one
// connection configuration: the worker and the gateway have to agree on
// addr/password/db or the gateway enqueues into a Redis the worker never
// reads.
func TestServerOpts_MatchesClientOpts(t *testing.T) {
	t.Setenv("REDIS_ADDR", "cache:6380")
	t.Setenv("REDIS_PASSWORD", "pw")
	t.Setenv("REDIS_DB", "7")

	c, s := ClientOpts(), ServerOpts()
	if c != s {
		t.Errorf("ServerOpts() = %+v, ClientOpts() = %+v; they must agree", s, c)
	}
}

// TestServerConfig_Concurrency pins WORKER_CONCURRENCY, including the
// fallbacks. A non-numeric value must not become a 0 (which asynq treats as
// "no workers") — parseInt is strict about exactly that.
func TestServerConfig_Concurrency(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "4")
		if got := ServerConfig().Concurrency; got != 4 {
			t.Errorf("Concurrency = %d, want 4", got)
		}
	})
	t.Run("unset", func(t *testing.T) {
		t.Setenv("WORKER_CONCURRENCY", "")
		if got := ServerConfig().Concurrency; got != 10 {
			t.Errorf("Concurrency = %d, want the default 10", got)
		}
	})
	for _, bad := range []string{"0", "abc", "4x", "-1", " 4", "4.0"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			t.Setenv("WORKER_CONCURRENCY", bad)
			got := ServerConfig().Concurrency
			if got != 10 {
				t.Errorf("WORKER_CONCURRENCY=%q gave Concurrency %d, want the default 10", bad, got)
			}
		})
	}
}

// TestServerConfig_QueuePriorities pins the weights. A typo in a queue name
// does not fail loudly — asynq just never dequeues from it — so the
// critical/default/low set and their ordering are worth pinning.
func TestServerConfig_QueuePriorities(t *testing.T) {
	t.Setenv("WORKER_CONCURRENCY", "")
	cfg := ServerConfig()
	want := map[string]int{"critical": 6, "default": 3, "low": 1}
	if len(cfg.Queues) != len(want) {
		t.Fatalf("Queues = %v, want exactly %v", cfg.Queues, want)
	}
	for name, weight := range want {
		if cfg.Queues[name] != weight {
			t.Errorf("Queues[%q] = %d, want %d", name, cfg.Queues[name], weight)
		}
	}
	if !(cfg.Queues["critical"] > cfg.Queues["default"] &&
		cfg.Queues["default"] > cfg.Queues["low"]) {
		t.Errorf("queue weights are not strictly ordered critical > default > low: %v", cfg.Queues)
	}
	if cfg.StrictPriority {
		t.Error("StrictPriority = true; a busy low-priority queue must not starve default")
	}
}

// TestServerConfig_RetryDelayIsLinear pins the retry policy. The intent is
// "do not hammer a failing scrape", so the delay grows linearly with the
// retry count and tops out at 25 x 5s = 125 s, inside asynq's default of
// 25 attempts.
func TestServerConfig_RetryDelayIsLinear(t *testing.T) {
	t.Setenv("WORKER_CONCURRENCY", "")
	cfg := ServerConfig()
	if cfg.RetryDelayFunc == nil {
		t.Fatal("RetryDelayFunc is nil; a failing task would retry immediately")
	}
	task := asynq.NewTask("scan:full", nil)
	// n counts retries, so n=0 is the FIRST retry and is immediate by
	// design — a transient blip should not cost five seconds. The growth
	// is what matters, and it starts at n=1.
	if d := cfg.RetryDelayFunc(0, nil, task); d != 0 {
		t.Errorf("first retry delay = %v, want 0", d)
	}
	var prev time.Duration
	for n := 1; n <= 25; n++ {
		d := cfg.RetryDelayFunc(n, nil, task)
		if d <= 0 {
			t.Fatalf("retry %d: delay = %v, want a positive backoff", n, d)
		}
		if d <= prev {
			t.Fatalf("retry %d: delay %v did not grow past retry %d's %v", n, d, n-1, prev)
		}
		if want := time.Duration(n) * 5 * time.Second; d != want {
			t.Fatalf("attempt %d: delay = %v, want %v", n, d, want)
		}
		prev = d
	}
}
