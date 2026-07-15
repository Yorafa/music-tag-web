// Package queue centralizes asynq client/server setup.
//
// Both the gateway (enqueue-only) and worker (process-only) construct their
// respective asynq components using the same Redis connection options.
package queue

import (
	"os"
	"time"

	"github.com/hibiken/asynq"
)

// ClientOpts returns Redis client options for an asynq producer (gateway).
func ClientOpts() asynq.RedisClientOpt {
	addr := getEnv("REDIS_ADDR", "127.0.0.1:6379")
	pass := getEnv("REDIS_PASSWORD", "")
	dbStr := getEnv("REDIS_DB", "0")
	db := parseInt(dbStr, 0)
	return asynq.RedisClientOpt{Addr: addr, Password: pass, DB: db}
}

// ServerOpts returns Redis client options for an asynq consumer (worker).
func ServerOpts() asynq.RedisClientOpt {
	return ClientOpts()
}

// ServerConfig returns the default asynq.Server config for the worker.
// Concurrency and queue priorities are tunable via env.
func ServerConfig() asynq.Config {
	concurrency := parseInt(getEnv("WORKER_CONCURRENCY", "10"), 10)
	return asynq.Config{
		Concurrency: concurrency,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
		StrictPriority: false,
		// Worker-level retry up to 25 attempts (asynq default).
		// Long-running scan-like tasks don't want to retry too aggressively.
		RetryDelayFunc: func(n int, err error, t *asynq.Task) time.Duration {
			return time.Duration(n) * 5 * time.Second
		},
	}
}

func getEnv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func parseInt(s string, d int) int {
	if s == "" {
		return d
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return d
		}
		n = n*10 + int(c-'0')
	}
	return n
}
