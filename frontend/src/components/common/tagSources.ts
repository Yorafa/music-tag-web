// The tag sources a scrape can be pointed at, and the one rule that governs
// picking more than one.
//
// This list used to live inside WorkstationToolbar, which meant the track
// detail dialog — the surface that actually shows candidates, and the one a
// phone gets — could not offer the choice at all: it searched one hardcoded
// source ('smart_tag') and the user never learned that. The batch toolbar
// offered a picker whose selection was then thrown away (only
// `selectedSources[0]` was ever searched), so the choice existed on screen
// and not in the request.

import type { MusicSource } from '@/types';

export const SOURCES: { id: MusicSource; name: string }[] = [
  { id: 'netease', name: '网易云音乐' },
  { id: 'qmusic', name: 'QQ 音乐' },
  { id: 'kugou', name: '酷狗音乐' },
  { id: 'kuwo', name: '酷我音乐' },
  { id: 'migu', name: '咪咕音乐' },
  { id: 'musicbrainz', name: 'MusicBrainz' },
  { id: 'acoustid', name: 'AcoustID 声纹' },
];

/** Add or remove one source from a selection.
 *
 *  The last remaining source cannot be removed. A zero-source search is not
 *  "search nothing and show an empty list" — it is a request that silently
 *  finds nothing, and the user has no way to tell that apart from "this
 *  track has no matches". */
export function toggleSourceSelection(
  current: MusicSource[],
  id: MusicSource,
): MusicSource[] {
  if (current.includes(id)) {
    return current.length > 1 ? current.filter((s) => s !== id) : current;
  }
  return [...current, id];
}

/** How many candidates a multi-source search asks each source for. The
 *  gateway caps a single fan-out at 15 by default, so this is deliberately
 *  larger: paging over five results is a way of reviewing candidates, and a
 *  list that stops at 15 makes the pager look like the end of the music. */
export const CANDIDATE_FETCH_LIMIT = 50;
