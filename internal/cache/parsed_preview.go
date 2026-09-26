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
	"context"
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
//
// Every tag field the parser can fill is here, and this struct is the
// server's own copy: the apply step writes from the cached bundle, with the
// client's overrides merged on top, so a field that is not carried simply
// cannot be written. That is why adding a field means adding it in three
// places (parser, this, the worker's TagUpdate) — the cache is the trust
// boundary, and a missing field fails closed.
type ParsedResult struct {
	Path        string `json:"path"`
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"albumartist,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Year        string `json:"year,omitempty"`
	TrackNumber string `json:"tracknumber,omitempty"`
	DiscNumber  string `json:"discnumber,omitempty"`
	Status      string `json:"status"`
}

// Empty reports whether the row carries nothing to write. The apply step
// skips these rather than calling tag.Write with an empty update.
func (r ParsedResult) Empty() bool {
	return r.Title == "" && r.Artist == "" && r.Album == "" &&
		r.AlbumArtist == "" && r.Genre == "" && r.Year == "" &&
		r.TrackNumber == "" && r.DiscNumber == ""
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

// Load returns the bundle at `token` and CONSUMES it (REVIEW.md P2-7).
//
// The package doc already described this gate as "single-shot", but the
// implementation only read: the entry stayed in the map until some future
// Sweep, and nothing called Sweep. Two consequences:
//
//   - the map only ever grew, holding up to 5000 rows per preview for the
//     full 10-minute TTL regardless of whether the user ever applied;
//   - the same token could be replayed repeatedly within its TTL, so
//     "apply what I previewed" was re-runnable by whoever had the token.
//
// Both are fixed by deleting on read. An apply that legitimately needs to
// retry re-previews, which is cheap and is what the UI already does on an
// expired-token error.
//
// Takes the write lock because it mutates. A replay race is now impossible
// rather than merely unlikely: two concurrent applies of one token cannot
// both win.
func (c *previewCache) Load(token string) (ParsedBundle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.bundles[token]
	if !ok {
		return ParsedBundle{}, ErrTokenExpired
	}
	// Consume unconditionally, including on the stale path — an expired
	// entry is dead weight either way.
	delete(c.bundles, token)
	if c.now().Sub(b.CreatedAt) > c.ttl {
		return ParsedBundle{}, ErrTokenExpired
	}
	return b, nil
}

// Sweep walks the cache and deletes entries whose CreatedAt exceeds TTL.
//
// Load now consumes on read, so the only entries left are ones the user
// previewed and never applied. Those still need reaping, or an abandoned
// preview pins its rows for the full TTL; StartJanitor below calls this
// periodically so the janitor is no longer merely aspirational.
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

// StartJanitor reaps abandoned previews on a ticker (REVIEW.md P2-7).
//
// Load consumes on read, so what remains is the preview-a-then-abandon
// case: the user opened the modal, previewed, and walked away. Without a
// janitor those rows sit for the full 10-minute TTL. The old comment here
// said "until a janitor is wired" — this wires it.
//
// Called once from the gateway's main; the goroutine exits with the
// process, so no shutdown plumbing is needed for a 5-minute sweep of a
// map guarded by its own mutex.
func StartJanitor(ctx context.Context, interval time.Duration) {
	go runJanitor(ctx, interval, DefaultPreviewCache)
}

// runJanitor is the loop body, split out so tests can drive it against a
// private cache and a short interval instead of the 5-minute production one.
func runJanitor(ctx context.Context, interval time.Duration, c *previewCache) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Sweep()
		}
	}
}
