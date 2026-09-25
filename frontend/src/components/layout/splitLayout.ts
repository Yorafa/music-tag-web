// Geometry for the song-detail Dialog's two-panel split (editor ↔ scrape
// results).
//
// These numbers were extracted out of AppShell.tsx so they can be unit
// tested. jsdom performs no layout — `clientWidth` is always 0 and there
// is no reflow — so an integration test cannot tell whether a panel is
// actually being squeezed; it can only assert on the arithmetic that
// decides the width. That arithmetic is where both of the "not displaying
// fully" bugs lived, so it is what gets tested.

/** Bounds for the scrape-results panel, in px. */
export const SCRAPE_RIGHT_MIN = 280
export const SCRAPE_RIGHT_MAX = 720

/** Floor for the editor column. The tag form's field grid stops being
 *  readable below this, so widening the scrape panel must never be able
 *  to squeeze the editor out of existence. */
export const SCRAPE_LEFT_MIN = 420

export const DEFAULT_SCRAPE_RIGHT = 448

/** Below this available width the panels stack vertically. */
export const SCRAPE_STACK_BELOW = SCRAPE_LEFT_MIN + SCRAPE_RIGHT_MIN

export function clamp(n: number, lo: number, hi: number): number {
  if (Number.isNaN(n)) return lo
  return Math.min(Math.max(n, lo), hi)
}

/** Clamp a persisted (or user-dragged) width into the absolute limits.
 *
 *  This is what a stored value goes through on load. Without it, a width
 *  written by a build that did not bound its drags — or by any drag past
 *  the old unbounded `Math.max(200, …)` — dictates the layout forever,
 *  because it is read straight back out of localStorage.
 *
 *  Returns DEFAULT_SCRAPE_RIGHT for non-finite input (NaN from a corrupt
 *  localStorage entry, Infinity from a division gone wrong) rather than
 *  propagating it into a CSS width. */
export function sanitizeStoredWidth(raw: number | null | undefined): number {
  if (raw == null || !Number.isFinite(raw)) return DEFAULT_SCRAPE_RIGHT
  return clamp(raw, SCRAPE_RIGHT_MIN, SCRAPE_RIGHT_MAX)
}

export interface SplitLayout {
  /** Width to hand the scrape panel. */
  right: number
  /** Upper bound the drag handle should advertise. */
  maxRight: number
  /** True when the panels must stack rather than sit side by side. */
  stacked: boolean
}

/** Decide the split geometry for a given available width.
 *
 *  `available` is the width of the dialog's split row. Pass 0 when it has
 *  not been measured yet (first paint, or a test environment with no
 *  layout): the result then falls back to the absolute limits, which is
 *  the correct conservative answer.
 *
 *  The order matters. The panel is first clamped to its absolute range,
 *  then additionally capped so the editor keeps SCRAPE_LEFT_MIN. When
 *  the available width cannot satisfy both minimums the layout stacks
 *  instead, so neither panel is left in a degenerate width. */
export function resolveSplitLayout(
  available: number,
  preferred: number,
): SplitLayout {
  const base = clamp(preferred, SCRAPE_RIGHT_MIN, SCRAPE_RIGHT_MAX)

  if (!Number.isFinite(available) || available <= 0) {
    return { right: base, maxRight: SCRAPE_RIGHT_MAX, stacked: false }
  }

  if (available < SCRAPE_STACK_BELOW) {
    return { right: base, maxRight: SCRAPE_RIGHT_MAX, stacked: true }
  }

  // Room left for the editor after honouring the user's preference.
  const roomForRight = available - SCRAPE_LEFT_MIN
  // If the dialog is between STACK_BELOW and the point where
  // SCRAPE_RIGHT_MIN no longer fits alongside SCRAPE_LEFT_MIN, keep the
  // panel at its own minimum rather than pushing the editor under it.
  const hi = Math.max(SCRAPE_RIGHT_MIN, Math.min(SCRAPE_RIGHT_MAX, roomForRight))

  return { right: clamp(base, SCRAPE_RIGHT_MIN, hi), maxRight: hi, stacked: false }
}

/** Apply a drag delta to the current width, honouring both limit sets.
 *
 *  The delta is positive when the handle moves right, which shrinks the
 *  scrape panel. Clamping to `maxRight` (not the absolute max) is what
 *  stops a drag from pushing the editor below its floor. */
export function applyResize(
  current: number,
  delta: number,
  layout: SplitLayout,
): number {
  return clamp(current - delta, SCRAPE_RIGHT_MIN, layout.maxRight)
}
