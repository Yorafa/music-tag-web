// Strip heavy fields before writing musicInfo into localStorage.
//
// music_id3 responses often embed cover art as multi-MB data-URIs on
// `artwork` / `album_img`. Persisting those verbatim exhausts the
// ~5MB localStorage quota after a handful of tracks and silently
// drops the entire library/worklist write (persist helpers swallow
// QuotaExceededError). Keep title/artist/album etc.; covers re-fetch
// via /api/music_id3/ on open or boot hydrate when needsMusicInfoRefetch
// reports a missing usable cover.

import type { MusicTagInfo } from '@/types';
import { resolveCoverSrc } from '@/utils/cover';

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
 *  - empty / missing cache → fetch
 *  - lightweight persisted cache (title/artist kept, data-URI cover
 *    stripped) → fetch so list thumbs recover after reload
 *
 *  Files that truly have no embedded cover will re-fetch once per page
 *  load; concurrency is bounded in hydrateTagsBatched. */
export function needsMusicInfoRefetch(
  info: Partial<MusicTagInfo> | undefined | null,
): boolean {
  if (!info) return true;
  if (!Object.values(info).some((v) => v != null)) return true;
  return !resolveCoverSrc(info);
}
