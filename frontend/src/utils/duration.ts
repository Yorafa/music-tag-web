// Formatting a track length for display.
//
// The duration reaches the browser in whatever shape the source plugin
// serialised: the gRPC field is a double, but the same value passes
// through JSON, a Zustand store, and — for the sources added later — a
// plain string. Search results therefore arrive as a number, a numeric
// string, or not at all, and all three have to render without the caller
// pre-checking.
//
// This lived inside CloudSearchView as a local function until the search
// results actually carried a duration, at which point two surfaces
// needed it and the second copy would have been free to drift.

// A track shorter than this is treated as "no length" rather than 0:00.
// Sources use 0 for a genuinely unknown length (MusicBrainz search
// returns none at all), and rendering that as 0:00 would read as a
// zero-second track instead of an absent one.
const MIN_DISPLAYABLE_SECONDS = 1;

/**
 * Render a track length as `m:ss`, or `h:mm:ss` past an hour.
 *
 * Returns '' when there is no usable length, so callers can interpolate
 * the result directly and get no stray separator:
 *
 *   `${song.artist}${album ? ` · ${album}` : ''}${dur ? ` · ${dur}` : ''}`
 */
export function formatDuration(
  d: string | number | null | undefined,
): string {
  const secs = toSeconds(d);
  if (secs === null) return '';

  // Round rather than truncate: a 119.7s track displayed as 1:59 reads
  // as short, where 2:00 is what the listener will actually experience.
  const total = Math.round(secs);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;

  if (h > 0) {
    return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  }
  return `${m}:${String(s).padStart(2, '0')}`;
}

/**
 * The same length with an always-visible placeholder, for table cells
 * that need the column to stay aligned.
 */
export function formatDurationOrDash(
  d: string | number | null | undefined,
): string {
  return formatDuration(d) || '—';
}

/**
 * Coerce the shapes a duration arrives in to a positive number of
 * seconds, or null when the value carries no length.
 *
 * Kept separate from the formatting so the "is there a length at all"
 * decision — which the UI branches on — is testable on its own and is not
 * re-derived from a formatted string.
 */
export function toSeconds(
  d: string | number | null | undefined,
): number | null {
  if (d === null || d === undefined || d === '') return null;

  const secs = typeof d === 'number' ? d : Number(String(d).trim());
  if (!Number.isFinite(secs)) return null;
  if (secs < MIN_DISPLAYABLE_SECONDS) return null;
  return secs;
}
