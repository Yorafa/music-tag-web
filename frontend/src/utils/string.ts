/**
 * Defensive string helpers for fields whose backend type is loose.
 *
 * The Django backend sometimes returns `year` / `album` / `lyrics` as a
 * number or another non-string shape rather than `string`. Calling
 * `.trim()` on a non-string (especially via `?.trim()`) throws
 * `TypeError: a?.trim is not a function` and the React tree unmounts.
 *
 * All id3-derived trim callsites should go through `toTrimmedString`
 * so backend type drift becomes a no-op rather than a runtime crash.
 */

/** Coerce any input (number, null, undefined, object, string) to a
 *  trimmed string. Returns `''` for nullish and `String(v)` for the
 *  rest, then strips whitespace. */
export function toTrimmedString(v: unknown): string {
  if (v == null) return '';
  return String(v).trim();
}

/** Convenience for cover-letter fallbacks. Returns the uppercase
 *  first character of the first non-whitespace char of `s`, or `'♫'`
 *  if `s` is empty / blank. Safe against non-string input. */
export function toInitialChar(s: unknown, fallback = '♫'): string {
  const trimmed = toTrimmedString(s);
  return trimmed.charAt(0).toUpperCase() || fallback;
}
