// Fetching lyrics the way the backend actually serves them.
//
// The bug this fixes: the 歌词 tab called `fetchLyric(title, 'netease')`,
// but the backend's FetchLyric is keyed by a source-specific *song_id*
// (netease numeric id, kuwo musicId, kugou hash, migu contentId, qmusic
// songmid), not a title — so passing a title returned nothing for every
// source. And the proto `Song` message has no lyric field, so search
// candidates never carry lyrics either: the only way to get a lyric is
// the separate FetchLyric(song_id) RPC.
//
// So a lyric fetch is two hops, not one: resolve the song_id by searching
// the source, then ask that source for the lyric of the id it returned.
// Doing the search immediately before FetchLyric also fixes migu, whose
// FetchLyric only works after a Search populated its per-contentId cache.

import { fetchId3ByTitle, fetchLyric } from '@/api/client';
import type { MusicSource, SongInfo } from '@/types';

/** The backend's not-found path returns SuccessData with the string
 *  "未找到歌词 <err>" rather than an error envelope (see
 *  gateway/handler/tag.go::FetchLyric). Treat that prefix as "no lyric"
 *  so we don't fill the editor with an error sentinel and don't stop
 *  trying the remaining sources. */
export const LYRIC_NOT_FOUND_PREFIX = '未找到歌词';

/** A payload is a real lyric only if it is a non-empty string that is not
 *  the not-found sentinel. LRC whitespace-only strings count as empty. */
export function isRealLyric(text: unknown): text is string {
  return (
    typeof text === 'string' &&
    text.trim().length > 0 &&
    !text.startsWith(LYRIC_NOT_FOUND_PREFIX)
  );
}

/** Fetch the lyric for one already-resolved candidate: it carries the very
 *  `id` + `source` FetchLyric needs. Returns the lyric string, or null when
 *  the source has none (empty, not-found sentinel, or a thrown request). */
export async function fetchLyricForSong(
  songId: string,
  source: string,
): Promise<string | null> {
  if (!songId || !source) return null;
  try {
    const res = await fetchLyric(songId, source);
    const data = res?.data;
    return isRealLyric(data) ? data : null;
  } catch {
    return null;
  }
}

export interface LyricHit {
  lyric: string;
  /** Which source actually produced it — surfaced so the notice can name
   *  the source instead of claiming a generic success. */
  source: string;
}

/** Resolve a lyric by title across several sources.
 *
 *  Sequential, not parallel: the first source that has the lyric wins and
 *  the rest are never asked, so a netease hit costs one round trip, not
 *  five. Sources are tried in the order given, which lets the caller put
 *  the user's preferred/most-reliable source first.
 *
 *  For each source: search by title (limit 1 — we only need the top
 *  match's id), then FetchLyric that id. A source that returns no
 *  candidate, or a candidate whose lyric is empty, falls through to the
 *  next source rather than aborting the whole fetch. */
export async function fetchLyricAcross(
  query: string,
  sources: MusicSource[],
  fullPath = '',
): Promise<LyricHit | null> {
  if (!query) return null;
  for (const source of sources) {
    let top: SongInfo | undefined;
    try {
      const res = await fetchId3ByTitle(query, source, fullPath, 1);
      const list = res?.data;
      top = Array.isArray(list) ? list[0] : undefined;
    } catch {
      continue;
    }
    if (!top?.id) continue;
    const lyric = await fetchLyricForSong(top.id, source);
    if (lyric) return { lyric, source };
  }
  return null;
}
