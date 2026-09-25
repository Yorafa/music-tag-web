// Package cache — parsed_preview.go holds the short-lived in-memory
// cache backing the C.2 Filename Parse preview/apply round-trip.
//
// Lifecycle:
//  1. POST /api/tag/preview_parse_filenames/ → Save(bundle) returns a
//     random 32-byte token; bundle is stored keyed by token with
//     CreatedAt stamped at Save time.
//  2. POST /api/tag/apply_parsed_filenames/ → Load(token) reads back
//     the bundle, validates TTL inline (single-shot check so a stale
//     token errors without extra lookup cost), merges per-row user
//     overrides, and enqueues an asynq TypeApplyParsedFilenames task
//     that writes tags per row.
//
// Why in-memory + not path_cache (LRU by path):
//   - preview/apply is a small number of large bulk operations per
//     human-driven session (one preview → one apply per modal open);
//     LRU eviction is unnecessary because the user has a tight round-trip.
//   - any preview older than 10 min is presumptively abandoned; the
//     gate lives in Load, so a slow task worker doesn't accidentally
//     write stale-preview overrides that the user couldn't see anymore.
//
// Token strategy: 16 random bytes hex-encoded (32 chars). crypto/rand
// is sufficient — we don't need the secret-store-grade properties of
// larger tokens because the worst-case abuse is "write tags against a
// guessed token", and the token is short-lived AND the worst case
// only reaches files the user owns via MUSIC_DIR anyway.
package cache

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// ErrTokenExpired returned by Load when the bundle's TTL has elapsed
// OR the token isn't in the map. The two cases are merged because
// callers respond identically (re-call Preview, not re-use the token).
var ErrTokenExpired = errors.New("cache: parsed_preview token expired or unknown")

// ParsedResult is one row's preview (carried forward to the worker).
// Status is "ok"/"ambiguous"/"unparsable" — mirror utils.Status*
// string values so JSON wire-shapes round-trip byte-identically.
type ParsedResult struct {
	Path   string `json:"path"`
	Artist string `json:"artist,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"`
}

// ParsedBundle is what Save accepts and Load returns.
type ParsedBundle struct {
	Results   []ParsedResult `json:"results"`
	CreatedAt time.Time      `json:"-"`
}

// previewTTL default; cmd/gateway tests override via SetPreviewTTL.
const previewTTL = 10 * time.Minute

// previewCache is the implementation type. Used as DefaultPreviewCache.
type previewCache struct {
	mu      sync.RWMutex
	bundles map[string]ParsedBundle
	ttl     time.Duration
	now     func() time.Time // injection seam for tests
}

// DefaultPreviewCache is the process-wide singleton keyed by generated
// token. Same pattern as path_cache.Default — only safe because
// gateway is a single process; any future horizontal scaling would
// either swap this for Redis (we already have go-redis in go.mod for
// events) or document a load-balancer stickiness rule for tokens.
var DefaultPreviewCache = newPreviewCache(previewTTL)

func newPreviewCache(ttl time.Duration) *previewCache {
	return &previewCache{
		bundles: make(map[string]ParsedBundle),
		ttl:     ttl,
		now:     time.Now,
	}
}

// Save stores `b` keyed by a fresh random token and stamps CreatedAt.
// Returns the token so the caller (handler) can pass it back in apply.
// Thread-safe; takes write-lock over the whole insert (cheap because
// preview size is bounded at the human-bulk-edit level — thousands of
// rows max per session).
func (c *previewCache) Save(b ParsedBundle) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	token, err := randomPreviewToken()
	if err != nil {
		return "", err
	}
	b.CreatedAt = c.now()
	c.bundles[token] = b
	return token, nil
}

// Load returns the bundle at `token` IF still within TTL. Stale OR
// missing → ErrTokenExpired. Read-lock only, so concurrent Save calls
// are blocked (correctly) but concurrent Load calls do not contend.
func (c *previewCache) Load(token string) (ParsedBundle, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.bundles[token]
	if !ok {
		return ParsedBundle{}, ErrTokenExpired
	}
	if c.now().Sub(b.CreatedAt) > c.ttl {
		return ParsedBundle{}, ErrTokenExpired
	}
	return b, nil
}

// Sweep walks the cache and deletes entries whose CreatedAt exceeds
// TTL. Intended for a periodic janitor (e.g. add to the same ticker
// path_cache already uses). Until a janitor is wired the cache grows
// only when humans are actively previewing — bounded by human session
// volume, but a future heavy-load day may want the sweep ticker.
func (c *previewCache) Sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, b := range c.bundles {
		if now.Sub(b.CreatedAt) > c.ttl {
			delete(c.bundles, k)
		}
	}
}

// Size returns the current entry count (diagnostic / test helper).
func (c *previewCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.bundles)
}

// SetTTL overrides DefaultPreviewCache's TTL (test-only seam).
// Production code should NOT call this; pass a different cache or
// tweak `previewTTL` constant directly.
func (c *previewCache) SetTTL(ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ttl = ttl
}

// SetClock swaps the clock source (test-only seam).
func (c *previewCache) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// randomPreviewToken returns 32 hex chars from crypto/rand. The error
// return is preserved so a caller can distinguish "out of entropy"
// (essentially impossible in practice) from a logical bug. Future
// hardening: rate-limit Save calls so a hostile caller can't drain
// the entropy pool faster than Linux refills it — currently unbounded.
func randomPreviewToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
