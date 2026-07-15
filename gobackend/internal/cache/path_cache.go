// Package cache holds in-process lookup tables that mirror db invariants
// without paying a SELECT per HTTP request.
//
// PathCache is the only cache for now. It maps absolute filesystem paths
// to lightweight TrackEntry values. The TidyFolder worker fires a
// FileMoved event after a rename + db update; the gateway subscriber
// applies the invalidation via HandleFileMoved.
//
// Cache miss policy: callers (the handler chain) re-query the DB on
// Get-miss. We do NOT lazy-populate here to avoid surprising behaviour
// where the cache returns rows the DB no longer has.
//
// P1.5 issue F (M3): the cache is now an LRU with a sized cap so a
// pathological scan or rename burst cannot OOM the gateway. Eviction
// is LRU — on Get/Set, the touched entry moves to the front; when the
// cap is exceeded, the LEAST-recently-used entry is dropped. This
// matches typical web-access patterns where recently-fetched tracks
// are more likely to be hit again before archival.
package cache

import (
	"container/list"
	"sync"
)

// defaultLRUCap bounds the number of entries stored in a PathCache that
// is constructed without an explicit cap. Operators can override via
// NewPathCacheWithCap for high-volume deployments.
const defaultLRUCap = 50_000

// TrackEntry is the cache-resident shape of a music file. Trimmed down vs
// db.Track (only the fields handlers actually read on the hot path).
type TrackEntry struct {
	UID    string `json:"uid,omitempty"`
	Path   string `json:"path"`
	Name   string `json:"name"`
	Album  string `json:"album,omitempty"`
	Artist string `json:"artist,omitempty"`
	Year   int    `json:"year,omitempty"`
}

// lruNode is the doubly-linked-list payload. `key` is kept alongside
// `val` so eviction can delete from the map in O(1).
type lruNode struct {
	key string
	val TrackEntry
}

// PathCache is a path → TrackEntry LRU map.
//
// Integrity model:
//
//   • Set replaces the entry at `path` (idempotent) and moves it to the
//     front of the LRU.
//   • Get returns (entry, true) on hit and moves the entry to the front;
//     (zero, false) on miss.
//   • Invalidate removes `path` from the cache regardless of whether it
//     exists; the (now-free) list position is also removed.
//   • HandleFileMoved is the rename-invalidation primitive: evict OldPath
//     and tombstone NewPath so a caller reading the post-rename path
//     will hit-miss and re-query the DB (which has the freshly-updated
//     row).
//
// Concurrency: sync.Mutex (NOT RWMutex). LRU operations touch the list
// + map on every Get/Set so the read/write distinction does not help —
// RWMutex would only add overhead.
type PathCache struct {
	mu     sync.Mutex
	cap    int
	byPath map[string]*list.Element
	order  *list.List // front = most recently used
}

// NewPathCache returns an empty LRU cache with the default cap. Callers
// typically use the package-level `Default` singleton (initialized at
// gateway boot) but tests can instantiate independently.
func NewPathCache() *PathCache {
	return NewPathCacheWithCap(defaultLRUCap)
}

// NewPathCacheWithCap returns an empty LRU cache that holds at most
// `cap` entries. A non-positive cap falls back to defaultLRUCap so a
// misconfigured low cap cannot degrade into an effective no-op.
func NewPathCacheWithCap(cap int) *PathCache {
	if cap <= 0 {
		cap = defaultLRUCap
	}
	return &PathCache{
		cap:    cap,
		byPath: make(map[string]*list.Element, cap),
		order:  list.New(),
	}
}

// Get returns the cached entry for `path`. On hit the entry is promoted
// to the front of the LRU so subsequent reads stay hot.
func (c *PathCache) Get(path string) (TrackEntry, bool) {
	if c == nil {
		return TrackEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byPath[path]
	if !ok {
		return TrackEntry{}, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*lruNode).val, true
}

// Set inserts or replaces the entry at `path`. On insert that would
// overflow the cap, the least-recently-used entry is evicted first.
// Replacing an existing entry does not consume new capacity.
func (c *PathCache) Set(e TrackEntry) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byPath[e.Path]; ok {
		el.Value.(*lruNode).val = e
		c.order.MoveToFront(el)
		return
	}
	if c.order.Len() >= c.cap {
		// Evict tail; safe because we know Len() ≥ 1 when at cap.
		old := c.order.Back()
		if old != nil {
			c.order.Remove(old)
			delete(c.byPath, old.Value.(*lruNode).key)
		}
	}
	node := &lruNode{key: e.Path, val: e}
	c.byPath[e.Path] = c.order.PushFront(node)
}

// Invalidate removes `path` from the cache regardless of whether it
// exists. No-op on miss.
func (c *PathCache) Invalidate(path string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byPath[path]; ok {
		c.order.Remove(el)
		delete(c.byPath, path)
	}
}

// HandleFileMoved applies the rename invalidation atomically. The new
// path is tombstoned so any subsequent Get will miss — callers re-query
// the DB which has the post-update row. Old path is also removed.
//
// This is intentionally brutal: if the cache contained a stale entry for
// newPath (e.g. from a previous rename of the same file to a different
// location), we drop it anyway. The DB is the source of truth.
func (c *PathCache) HandleFileMoved(oldPath, newPath string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byPath[oldPath]; ok {
		c.order.Remove(el)
		delete(c.byPath, oldPath)
	}
	if el, ok := c.byPath[newPath]; ok {
		c.order.Remove(el)
		delete(c.byPath, newPath)
	}
}

// Size returns the number of cached entries (for diagnostics / tests).
func (c *PathCache) Size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// Cap returns the configured maximum number of entries.
func (c *PathCache) Cap() int {
	if c == nil {
		return 0
	}
	return c.cap
}

// ─── Default singleton ─────────────────────────────────────────────────────

var (
	defaultMu  sync.Mutex
	defaultRef *PathCache
)

// Default returns the process-wide singleton, creating one on first use.
// Library callers can rely on Default returning a non-nil cache; cmd/gateway
// main() should call SetDefault BEFORE any worker goroutine might call
// Default() to take ownership of the bootstrap path.
func Default() *PathCache {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultRef == nil {
		defaultRef = NewPathCache()
	}
	return defaultRef
}

// SetDefault installs a pre-constructed cache as the package-level
// singleton. Subsequent Default() calls return `c` (NOT a fresh instance).
// cmd/gateway/main() calls this at boot after constructing the cache so
// every component sharing the default reference sees the same map.
//
// Re-setting overwrites; the prior instance is no longer reachable via
// Default().
func SetDefault(c *PathCache) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultRef = c
}
