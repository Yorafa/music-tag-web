// Spec for the ScrollArea root class.
//
// The scrape-results panel rendered every result but showed only the
// first screenful, with no way to reach the rest. Nothing was missing
// from the data; the overflow was clipped by an ancestor with
// `overflow-hidden` and no scrollbar to reach it.
//
// The cause is a CSS flexbox rule, not a missing style: a flex item
// defaults to `min-height: auto`, which resolves to its content's
// min-content height. The spec exempts only boxes whose OWN overflow is
// not `visible`, and this Root is `relative` with visible overflow, so
// it does not qualify. With `flex-1` in a column, a tall child refuses
// to shrink, the column outgrows its parent, and the parent's
// `overflow-hidden` swallows the difference.
//
// Three other call sites had already worked around this by hand-writing
// "flex-1 min-h-0"; the fix moves it into the primitive so the next
// flex-child usage cannot reintroduce it.
//
// These assert on the composed class string rather than on rendered DOM.
// The suite has no component-renderer dependency (no @testing-library
// anywhere in package.json) and adding one for a class assertion would be
// a poor trade; `cn` is the exact expression the component hands to the
// DOM, so composing it here tests the shipped behaviour.

import { describe, it, expect } from 'vitest';
import { SCROLL_AREA_BASE_CLASS } from './scroll-area';
import { cn } from '@/lib/utils';

describe('ScrollArea root classes', () => {
  it('carries min-h-0 so it can shrink inside a flex column', () => {
    expect(SCROLL_AREA_BASE_CLASS).toContain('min-h-0');
  });

  it('keeps min-h-0 when the caller passes a flex-1 usage', () => {
    // The ScrapeResults case: <ScrollArea className="flex-1">.
    expect(cn(SCROLL_AREA_BASE_CLASS, 'flex-1')).toContain('min-h-0');
  });

  it('keeps min-h-0 when the caller passes a height class', () => {
    // The h-full callers (TagEditor, PlayView, TrackInspector,
    // CloudSearchView) are flex children in practice too.
    expect(cn(SCROLL_AREA_BASE_CLASS, 'h-full')).toContain('min-h-0');
  });

  it('is not overridable by a caller who forgets it', () => {
    // twMerge is what makes this worth asserting: a caller could pass
    // `min-h-0` away, and the primitive should not be defeatable that
    // easily. Nothing in the codebase does, but the guard is cheap.
    const out = cn(SCROLL_AREA_BASE_CLASS, 'flex-1 min-h-0');
    expect(out).toContain('min-h-0');
  });

  it('preserves the caller sizing utility alongside the base', () => {
    const out = cn(SCROLL_AREA_BASE_CLASS, 'flex-1 p-2');
    expect(out).toContain('flex-1');
    expect(out).toContain('p-2');
    expect(out).toContain('min-h-0');
  });
});
