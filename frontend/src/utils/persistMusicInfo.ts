// Strip heavy fields before writing musicInfo into localStorage.
//
// music_id3 responses can embed cover art as multi-MB data-URIs on
// `artwork` / `album_img`. Persisting those verbatim exhausts the
// ~5MB localStorage quota after a handful of tracks. Keep
// title/artist/album etc.
//
// Covers are not merely "too big to store" any more — the batch hydrate
// path does not request them at all (see id3Reader), because a full-
// resolution scan runs 3–15 MB per response and the worklist draws
// 32-pixel thumbnails. The table fetches a cover per visible row from
// /api/album_cover/ and lets the browser cache it. So this is now a
// backstop for the tag editor's whole-record responses rather than the
// main defence.

import type { MusicTagInfo } from '@/types';

const HEAVY_KEYS = new Set(['artwork', 'album_img']);

/** Return a shallow copy of `info` with data-URI / oversized image
 *  fields removed. Remote http(s) cover URLs are kept — those are
 *  small strings and useful for list thumbnails. */
export function stripHeavyMusicInfo(
  info: Partial<MusicTagInfo> | undefined,
): Partial<MusicTagInfo> | undefined {
  if (!info) return undefined;
  const out: Partial<MusicTagInfo> = {};
  for (const [k, v] of Object.entries(info)) {
    if (v == null) continue;
    if (HEAVY_KEYS.has(k) && typeof v === 'string' && v.startsWith('data:')) {
      continue;
    }
    // Defensive: drop any multi-KB string fields that look like
    // embedded payloads even if the key set drifts.
    if (
      HEAVY_KEYS.has(k) &&
      typeof v === 'string' &&
      v.length > 2048
    ) {
      continue;
    }
    (out as Record<string, unknown>)[k] = v;
  }
  return out;
}

/** Apply stripHeavyMusicInfo to every row that carries musicInfo. */
export function stripHeavyFromRows<T extends { musicInfo?: Partial<MusicTagInfo> }>(
  rows: T[],
): T[] {
  return rows.map((r) => {
    if (!r.musicInfo) return r;
    return { ...r, musicInfo: stripHeavyMusicInfo(r.musicInfo) };
  });
}

/** True when boot hydrate / openEditor should re-call music_id3.
 *
 *  Decided on the TEXT tags alone: a row whose cache has a title or artist
 *  is populated and must not be re-read.
 *
 *  This used to also require a cover ("no cover → refetch, so list thumbs
 *  recover after reload"). That is now WRONG and was a real performance bug
 *  waiting to happen: the batch hydrate path asks the server for tags
 *  WITHOUT artwork (an embedded cover is 3–15 MB per response — see
 *  id3Reader), so NO row ever comes back with one. Keying the refetch on
 *  the cover therefore marked every row stale, and every page load would
 *  re-read the entire library.
 *
 *  Nothing is lost by dropping the cover condition. Cover bytes are no
 *  longer part of this cache at all — they are not persisted (stripHeavy
 *  drops them) and the table fetches them per visible row from
 *  /api/album_cover/, which the browser caches with its own Cache-Control
 *  and ETag. So a re-read here would re-fetch data that was never stored
 *  and would not fix a thumbnail. */
export function needsMusicInfoRefetch(
  info: Partial<MusicTagInfo> | undefined | null,
): boolean {
  if (!info) return true;
  // Cover fields are excluded from the "is this row populated" test. A
  // record whose ONLY populated field is a cover says nothing about the
  // track — no title, no artist — so the row still needs a real read.
  // Counting it as populated would leave such a row permanently blank in
  // the table.
  for (const [key, value] of Object.entries(info)) {
    if (value == null) continue;
    if (HEAVY_KEYS.has(key)) continue;
    return false;
  }
  return true;
}
