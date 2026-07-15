package handler

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/cache"
	"go-music-tag/internal/events"
)

// webhookState owns the optional Bus + the in-process registered cache.
//
// Both fields are process-global because:
//   • there is exactly one gateway process per binary, but
//   • tests may inject a NullBus independently — handlers don't reach
//     into global state for Bus calls; the lifecycle is owned here.
//
// We use sync.RWMutex for the cache pointer so SetCache can be swapped at
// boot before Serve runs without races on incoming requests.
type webhookState struct {
	mu    sync.RWMutex
	cache *cache.PathCache
	bus   events.Bus
}

var globalWebhook = &webhookState{}

// SetCache wires the process-wide PathCache. Called from cmd/gateway main()
// before `r.Run(addr)`. Safe to call from multiple goroutines.
func SetCache(c *cache.PathCache) {
	globalWebhook.mu.Lock()
	defer globalWebhook.mu.Unlock()
	globalWebhook.cache = c
	// Mirror into the package-level singleton so other code can use
	// cache.Default() without threading the pointer everywhere.
	if c != nil {
		cache.SetDefault(c)
	}
}

// SetBus wires the process-wide Event Bus. cmd/gateway main() calls this
// before starting the subscriber loop.
func SetBus(b events.Bus) {
	globalWebhook.mu.Lock()
	defer globalWebhook.mu.Unlock()
	globalWebhook.bus = b
}

// StartSubscriber launches a goroutine that drains `events.TopicFileMoved`
// into the cache. Returns the cancel func for graceful shutdown. Safe to
// call once at boot; double-call would subscribe twice — callers should
// guard at the host level.
func StartSubscriber(ctx context.Context) func() {
	globalWebhook.mu.RLock()
	bus := globalWebhook.bus
	c := globalWebhook.cache
	globalWebhook.mu.RUnlock()
	if bus == nil {
		log.Printf("[webhook] bus not configured; subscriber not started")
		return func() {}
	}
	if c == nil {
		c = cache.Default()
		globalWebhook.mu.Lock()
		globalWebhook.cache = c
		globalWebhook.mu.Unlock()
	}
	ch, cancel, err := bus.Subscribe(ctx, events.TopicFileMoved)
	if err != nil {
		log.Printf("[webhook] subscribe err: %v", err)
		return cancel
	}
	go func() {
		for data := range ch {
			var e events.FileMovedEvent
			if err := json.Unmarshal(data, &e); err != nil {
				log.Printf("[webhook] decode FileMoved: %v", err)
				continue
			}
			c.HandleFileMoved(e.OldPath, e.NewPath)
			log.Printf("[webhook] cache invalidated: %s -> %s", e.OldPath, e.NewPath)
		}
	}()
	log.Printf("[webhook] subscriber started on topic=%s", events.TopicFileMoved)
	return cancel
}

// ─── HTTP entry point ──────────────────────────────────────────────────────

// FileMovedWebhook handles POST /api/webhooks/file_moved/ — receives a
// FileMoved event from an external producer (e.g. another instance of the
// worker, a manual admin tool) and applies the cache invalidation without
// the publisher having to know about Redis. Mirrors what the in-process
// subscriber does for the same event.
func FileMovedWebhook(c *gin.Context) {
	var e events.FileMovedEvent
	if err := c.ShouldBindJSON(&e); err != nil {
		Failure(c, "invalid FileMoved payload: "+err.Error())
		return
	}
	if e.OldPath == "" || e.NewPath == "" {
		Failure(c, "old_path and new_path are required")
		return
	}
	globalWebhook.mu.RLock()
	cachePtr := globalWebhook.cache
	globalWebhook.mu.RUnlock()
	if cachePtr == nil {
		cachePtr = cache.Default()
	}
	cachePtr.HandleFileMoved(e.OldPath, e.NewPath)
	SuccessData(c, gin.H{
		"status":    "refreshed",
		"old_path":  e.OldPath,
		"new_path":  e.NewPath,
		"cache_size": cachePtr.Size(),
	})
}
