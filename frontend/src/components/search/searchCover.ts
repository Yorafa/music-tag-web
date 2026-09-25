// Reading a cover out of a search result.
//
// Search results never carried a `cover` field: every tag-source plugin
// fills `AlbumImg` (wire name `album_img`), and only the YouTube
// download path populated `Cover`. Both search surfaces read `song.cover`
// anyway, so the field was always undefined and every result rendered
// the letter placeholder — the covers were in the response the whole time.
//
// Which name a result carries depends on the source, so this reads both
// rather than the frontend picking a winner and being wrong for half the
// fleet.

import type { SearchResult } from '@/types';

/**
 * The best cover URL on a search result, or undefined when it has none.
 *
 * Prefers `album_img` — what the plugins actually send — and falls back
 * to `cover`. Rejects the same empty-payload sentinel the id3 path
 * rejects, so a bare `data:image/jpeg,` does not render as a broken
 * image.
 */
export function searchCoverSrc(song: SearchResult | null | undefined): string | undefined {
  if (!song) return undefined;
  const candidates = [song.album_img, song.cover];
  for (const c of candidates) {
    if (typeof c === 'string' && c !== '' && !c.endsWith(',')) return c;
  }
  return undefined;
}

/** The same value, defaulted to '' so it can be handed straight to a
 *  `PlayerTrack.cover`, whose type is a plain string. */
export function searchCoverForTrack(
  song: SearchResult | null | undefined,
): string {
  return searchCoverSrc(song) ?? '';
}
