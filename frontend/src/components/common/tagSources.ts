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

import { readJson, writeJson } from '@/utils/persist';
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

/** Where the last-used selection is kept. Versioned because the stored
 *  value is a list of source ids and the id set changes when a plugin is
 *  added or dropped — an unversioned key would silently resurrect a
 *  selection naming a source this build no longer offers. */
const REMEMBERED_SOURCES_KEY = 'scrape.sources.v1';

/** The subset of `saved` that this build still offers, in the order given.
 *
 *  Everything here is defensive about `saved` because it is parsed
 *  localStorage: another version of the app, or a user with a stale key,
 *  can leave anything there. An empty result is a valid answer and means
 *  "fall back to the defaults" — never "search nothing". */
export function knownSources(saved: unknown): MusicSource[] {
  if (!Array.isArray(saved)) return [];
  const offered = new Set<string>(SOURCES.map((s) => s.id));
  const out: MusicSource[] = [];
  for (const id of saved) {
    if (typeof id !== 'string' || !offered.has(id) || out.includes(id as MusicSource)) {
      continue;
    }
    out.push(id as MusicSource);
  }
  return out;
}

/** The sources the last search used, or `fallback` when there is nothing
 *  usable stored. */
export function loadRememberedSources(fallback: MusicSource[]): MusicSource[] {
  const remembered = knownSources(readJson<unknown>(REMEMBERED_SOURCES_KEY));
  return remembered.length > 0 ? remembered : fallback;
}

/** Remember a selection for the next dialog open and the next page load. */
export function rememberSources(sources: MusicSource[]): void {
  writeJson(REMEMBERED_SOURCES_KEY, sources);
}
