// Untrusted-input handling for the persisted 智能刮削 / 搜索目录 UI state.
//
// Both values are read back from localStorage, which is not a trusted
// source: another version of the app, a half-finished write, or a hand-edit
// can leave anything there. This module exists so the sanitizers are
// ordinary pure functions that can be unit tested, rather than logic
// inlined in a component where there is no way to exercise them.
//
// The two failure modes are different, and that difference is the design:
//
//   - The 状态筛选 picks which rows the table shows. An unknown value is
//     not cosmetic: no sidebar chip would be highlighted, the table would
//     be empty, and the user has no visible way back to 「所有曲目」. So it
//     is checked against the valid set and falls back to 'all'.
//   - The 搜索目录 box filters the directory tree by substring. A bad value
//     has no structural failure — the tree just looks like it has no
//     matches, which is exactly what an over-narrow search looks like. So
//     it is only trimmed and capped, and deliberately NOT "repaired":
//     guessing at the user's intent would be worse than showing what they
//     typed.

import type { WorklistFilter } from '@/store/useWorklistStore';

const FILTER_STORAGE_KEY = 'worklist.filter.v1';
const SEARCH_QUERY_STORAGE_KEY = 'workstation.dirSearch.v1';

/** Every value `WorklistFilter` can take. Kept as data so this module does
 *  not import the store just for the union — the store imports it. */
const VALID_FILTERS: ReadonlySet<string> = new Set([
  'all',
  'pending',
  'scraped',
  'failed',
  'duplicate',
]);

export const FILTER_KEY = FILTER_STORAGE_KEY;
export const SEARCH_KEY = SEARCH_QUERY_STORAGE_KEY;

/** Ceiling on the stored query. The filter is a substring match against
 *  directory names, so a useful query is short; anything longer is a paste
 *  accident or a leftover from a much wider tree. Without a cap, a long
 *  paste is written to disk on every keystroke. */
export const MAX_STORED_QUERY = 200;

/** Narrow a stored filter to a real one, or fall back to 'all'.
 *
 *  'all' and not "nothing": the same default a first visit gets, and the
 *  only unusable-input answer that does not leave the user on a blank
 *  table. */
export function sanitizeFilter(raw: string | null): WorklistFilter {
  if (raw && VALID_FILTERS.has(raw)) return raw as WorklistFilter;
  return 'all';
}

/** Clamp a stored search query to something safe to hand the tree filter.
 *
 *  Trimming is here for the same reason the cap is: a value of only
 *  whitespace filters the tree to nothing while looking like a filled-in
 *  box, so it is indistinguishable from a real search that found nothing. */
export function sanitizeSearchQuery(raw: string | null): string {
  if (!raw) return '';
  return raw.trim().slice(0, MAX_STORED_QUERY);
}
