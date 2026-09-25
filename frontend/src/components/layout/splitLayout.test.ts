// Specs for the song-detail Dialog's split geometry.
//
// Both original bugs were arithmetic, not styling:
//
//  1. `onResize` clamped only the low end (`Math.max(200, w - d)`) while
//     `ResizeHandle`'s `max` prop was fed solely to `aria-valuemax`. A
//     drag to the right grew the panel without bound and the value was
//     persisted, so one over-wide drag narrowed the editor column
//     permanently — including across reloads, because the stored number
//     was read back with no re-clamp.
//
//  2. Nothing capped the panel against the dialog's actual width, and
//     below the two panels' combined minimums the fixed-width panel took
//     the whole dialog and pushed the editor to zero width.
//
// jsdom does no layout, so these specs assert the decisions rather than
// rendered geometry. That is the honest scope: the numbers are the fix.

import { describe, it, expect } from 'vitest';
import {
  SCRAPE_LEFT_MIN,
  SCRAPE_RIGHT_MAX,
  SCRAPE_RIGHT_MIN,
  SCRAPE_STACK_BELOW,
  DEFAULT_SCRAPE_RIGHT,
  applyResize,
  clamp,
  resolveSplitLayout,
  sanitizeStoredWidth,
} from './splitLayout';

describe('clamp', () => {
  it('passes values inside the range through', () => {
    expect(clamp(500, 100, 900)).toBe(500)
  })
  it('clamps both ends', () => {
    expect(clamp(50, 100, 900)).toBe(100)
    expect(clamp(5000, 100, 900)).toBe(900)
  })
  it('yields the low bound for NaN rather than propagating it', () => {
    // NaN would otherwise land in a CSS width as "NaNpx" and silently
    // drop the panel's sizing entirely.
    expect(clamp(NaN, 100, 900)).toBe(100)
  })
})

describe('sanitizeStoredWidth', () => {
  it('keeps a stored width that is already in range', () => {
    expect(sanitizeStoredWidth(500)).toBe(500)
  })

  it('falls back to the default when nothing is stored', () => {
    expect(sanitizeStoredWidth(null)).toBe(DEFAULT_SCRAPE_RIGHT)
    expect(sanitizeStoredWidth(undefined)).toBe(DEFAULT_SCRAPE_RIGHT)
  })

  it('clamps a stored width written past the maximum', () => {
    // This is the regression: an old build persisted whatever the drag
    // produced, so localStorage can legitimately contain 3000. Reading
    // that back verbatim squeezed the editor to nothing on every load.
    expect(sanitizeStoredWidth(3000)).toBe(SCRAPE_RIGHT_MAX)
  })

  it('clamps a stored width below the minimum', () => {
    expect(sanitizeStoredWidth(50)).toBe(SCRAPE_RIGHT_MIN)
  })

  it('rejects non-finite stored values', () => {
    expect(sanitizeStoredWidth(NaN)).toBe(DEFAULT_SCRAPE_RIGHT)
    expect(sanitizeStoredWidth(Infinity)).toBe(DEFAULT_SCRAPE_RIGHT)
  })
})

describe('resolveSplitLayout', () => {
  it('honours the preference on a wide dialog', () => {
    const l = resolveSplitLayout(1600, 500)
    expect(l.right).toBe(500)
    expect(l.stacked).toBe(false)
    expect(l.maxRight).toBe(SCRAPE_RIGHT_MAX)
  })

  it('stacks when the dialog is narrower than the combined minimums', () => {
    // A phone lands around 343px here (the dialog is
    // `min(64rem, 100% - 2rem)`), which is below either panel's floor.
    const l = resolveSplitLayout(343, DEFAULT_SCRAPE_RIGHT)
    expect(l.stacked).toBe(true)
  })

  it('stacks exactly at the boundary-minus-one', () => {
    expect(resolveSplitLayout(SCRAPE_STACK_BELOW - 1, 448).stacked).toBe(true)
  })

  it('does not stack at the boundary', () => {
    expect(resolveSplitLayout(SCRAPE_STACK_BELOW, 448).stacked).toBe(false)
  })

  it('caps the panel so the editor keeps its floor', () => {
    // 1000px available, user prefers the full 720 max: the editor would
    // be left with 280px, under its 420 floor.
    const l = resolveSplitLayout(1000, SCRAPE_RIGHT_MAX)
    expect(l.right).toBe(1000 - SCRAPE_LEFT_MIN)
    expect(l.right).toBeLessThanOrEqual(SCRAPE_RIGHT_MAX)
    expect(1000 - l.right).toBeGreaterThanOrEqual(SCRAPE_LEFT_MIN)
  })

  it('never drives the editor below its floor at any available width', () => {
    for (let available = SCRAPE_STACK_BELOW; available <= 3000; available += 37) {
      for (const preferred of [SCRAPE_RIGHT_MIN, 448, SCRAPE_RIGHT_MAX, 5000]) {
        const l = resolveSplitLayout(available, preferred)
        expect(l.right).toBeGreaterThanOrEqual(SCRAPE_RIGHT_MIN)
        expect(l.right).toBeLessThanOrEqual(SCRAPE_RIGHT_MAX)
        // Not stacked ⟹ the two panels coexist in the row.
        expect(available - l.right).toBeGreaterThanOrEqual(SCRAPE_LEFT_MIN)
      }
    }
  })

  it('falls back to the absolute limits before measurement', () => {
    // clientWidth is 0 on first paint and always 0 under jsdom.
    const l = resolveSplitLayout(0, 500)
    expect(l.right).toBe(500)
    expect(l.stacked).toBe(false)
    expect(l.maxRight).toBe(SCRAPE_RIGHT_MAX)
  })

  it('still bounds the panel before measurement', () => {
    // The un-measured path must not be the one place an over-wide stored
    // value slips through.
    expect(resolveSplitLayout(0, 99999).right).toBe(SCRAPE_RIGHT_MAX)
  })
})

describe('applyResize', () => {
  const wide = resolveSplitLayout(1600, 448)

  it('grows the panel when the handle moves left', () => {
    // movementX is positive to the right, which shrinks the right panel.
    expect(applyResize(448, -50, wide)).toBe(498)
  })

  it('shrinks the panel when the handle moves right', () => {
    expect(applyResize(448, 50, wide)).toBe(398)
  })

  it('never shrinks below the minimum', () => {
    expect(applyResize(300, 500, wide)).toBe(SCRAPE_RIGHT_MIN)
  })

  it('never grows past the absolute maximum', () => {
    // The original bug: `Math.max(200, w - d)` with no ceiling, so
    // repeated leftward drags grew the panel without limit.
    expect(applyResize(700, -1000, wide)).toBe(SCRAPE_RIGHT_MAX)
  })

  it('stops at the room-dependent ceiling, not the absolute one', () => {
    const narrow = resolveSplitLayout(1000, 448)
    // Absolute max is 720, but only 580 fits alongside the editor floor.
    expect(narrow.maxRight).toBe(1000 - SCRAPE_LEFT_MIN)
    expect(applyResize(600, -1000, narrow)).toBe(narrow.maxRight)
  })

  it('cannot squeeze the editor out of existence by repeated drags', () => {
    let w = DEFAULT_SCRAPE_RIGHT
    const l = resolveSplitLayout(1000, w)
    for (let i = 0; i < 200; i++) w = applyResize(w, -40, l)
    expect(1000 - w).toBeGreaterThanOrEqual(SCRAPE_LEFT_MIN)
  })
})
