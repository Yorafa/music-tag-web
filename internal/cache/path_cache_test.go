package cache

import (
	"fmt"
	"testing"
)

// TestPathCache_NewRespectsCapacity asserts cap is enforced and stored.
func TestPathCache_NewRespectsCapacity(t *testing.T) {
	c := NewPathCacheWithCap(7)
	if c.Cap() != 7 {
		t.Fatalf("Cap() = %d, want 7", c.Cap())
	}
	if c.Size() != 0 {
		t.Fatalf("Size() = %d, want 0", c.Size())
	}
}

// TestPathCache_EvictsLRUOnOverflow is the regression for P1.5 issue F (M3):
// when the cap is exceeded, the least-recently-USED entry is dropped, not
// the most-recently-inserted.
func TestPathCache_EvictsLRUOnOverflow(t *testing.T) {
	c := NewPathCacheWithCap(3)
	for i := 0; i < 3; i++ {
		c.Set(TrackEntry{Path: fmt.Sprintf("/p/%d", i), Name: fmt.Sprintf("n%d", i)})
	}
	if c.Size() != 3 {
		t.Fatalf("Size() = %d, want 3", c.Size())
	}

	// Insert a 4th — /p/0 is the LRU; it must be evicted.
	c.Set(TrackEntry{Path: "/p/3", Name: "n3"})

	if c.Size() != 3 {
		t.Fatalf("Size after overflow = %d, want 3", c.Size())
	}
	if _, ok := c.Get("/p/0"); ok {
		t.Errorf("/p/0 still present after overflow; LRU was forgotten")
	}
	for _, p := range []string{"/p/1", "/p/2", "/p/3"} {
		if _, ok := c.Get(p); !ok {
			t.Errorf("%s missing after overflow; wanted kept", p)
		}
	}
}

// TestPathCache_GetPromotesToFront asserts Touch/Set promotion so the
// LRU policy matches typical web-access patterns (last read stays hot).
func TestPathCache_GetPromotesToFront(t *testing.T) {
	c := NewPathCacheWithCap(3)
	c.Set(TrackEntry{Path: "/p/0"})
	c.Set(TrackEntry{Path: "/p/1"})
	c.Set(TrackEntry{Path: "/p/2"})

	// Touch /p/0 — it is now the MRU; /p/1 is the new LRU.
	if _, ok := c.Get("/p/0"); !ok {
		t.Fatalf("/p/0 hit unexpectedly failed")
	}

	// Inserting a 4th should evict /p/1, NOT /p/0.
	c.Set(TrackEntry{Path: "/p/3"})

	if _, ok := c.Get("/p/0"); !ok {
		t.Errorf("/p/0 evicted despite being MRU after Get")
	}
	if _, ok := c.Get("/p/1"); ok {
		t.Errorf("/p/1 unexpectedly survived (should be new LRU post-GMRU-promotion)")
	}
	if _, ok := c.Get("/p/2"); !ok {
		t.Errorf("/p/2 missing")
	}
	if _, ok := c.Get("/p/3"); !ok {
		t.Errorf("/p/3 missing")
	}
}

// TestPathCache_InvalidateRemovedFromLRU ensures Invalidate + HandleFileMoved
// actually free map + list slots so a previously-evicted path can be
// re-inserted without tripping the cap.
func TestPathCache_InvalidateRemovedFromLRU(t *testing.T) {
	c := NewPathCacheWithCap(2)
	c.Set(TrackEntry{Path: "/a"})
	c.Set(TrackEntry{Path: "/b"})
	c.Invalidate("/a")
	if c.Size() != 1 {
		t.Fatalf("Size after Invalidate = %d, want 1", c.Size())
	}
	c.Set(TrackEntry{Path: "/c"})
	if c.Size() != 2 {
		t.Fatalf("Size after reinsert = %d, want 2", c.Size())
	}
	if _, ok := c.Get("/a"); ok {
		t.Errorf("/a survived Invalidate")
	}
}

// TestPathCache_NilReceiverSafe keeps the package API friendliness for
// callers that may operate on a nil cache (mid-bootstrap). Any operation
// on a nil *PathCache must NO-OP rather than panic.
func TestPathCache_NilReceiverSafe(t *testing.T) {
	var c *PathCache
	if _, ok := c.Get("/x"); ok {
		t.Errorf("nil Get returned ok=true")
	}
	c.Set(TrackEntry{Path: "/x"})
	c.Invalidate("/x")
	c.HandleFileMoved("/a", "/b")
	if sz := c.Size(); sz != 0 {
		t.Errorf("nil Size = %d, want 0", sz)
	}
	if cp := c.Cap(); cp != 0 {
		t.Errorf("nil Cap = %d, want 0", cp)
	}
}
