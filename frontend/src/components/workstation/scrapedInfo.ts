// Turning one search hit into the tags to write for one track.
//
// This was a `const newInfo = { ... genre: '流行' }` literal inline in
// handleRunBatchScrape. Two things were wrong with that, and only one of
// them is visible in the diff:
//
//  1. Genre was hardcoded. Every auto-scraped file in the library got
//     「流行」 regardless of what it actually was, and the matched
//     candidate's real genre was never even read — the search response
//     carries one, the literal just ignored it.
//
//  2. It wrote `genre: ''`-shaped blanks for everything it had no value
//     for. An absent field is not the same as an empty one: the handler
//     treats a present key as "set this tag", so sending an empty string
//     clears a tag the file already had. Omission leaves it alone.
//
// So: take genre from the candidate, and omit any field the provider did
// not supply. As of this writing only MusicBrainz populates genre
// (internal/plugin/musicbrainz/server.go); the other six tag sources leave
// it empty, which is why omission — not a fallback constant — is the
// correct behaviour here. Writing 「流行」 to a Kuwo track because Kuwo
// does not report genre is worse than writing nothing.

import { baseNameOf } from '@/components/detail/renameResult';

/** The subset of a search hit this module reads. */
export interface ScrapeCandidate {
  name?: string;
  artist?: string;
  album?: string;
  album_img?: string;
  year?: string;
  genre?: string;
  lyric?: string;
  lyrics?: string;
}

/** A `select_data` row for POST /api/batch_update_id3/. */
export interface BatchSelection {
  /** Base name, because the endpoint joins it onto `file_full_path`. */
  name: string;
  /** This row's own tags — the per-row override, not the shared map. */
  music_info: Record<string, unknown>;
}

/** Trimmed, or undefined when the provider sent nothing usable. Empty and
 *  missing mean the same thing to a tag writer, and both must be omitted
 *  rather than written as ''. */
function orOmit(v: unknown): string | undefined {
  if (typeof v !== 'string') return undefined;
  const t = v.trim();
  return t === '' ? undefined : t;
}

/** The tags to write for `candidate`, ready to hand to the batch endpoint.
 *
 *  `fallbackTitle` is the query that produced no usable title — the file's
 *  own name — so a hit with an empty `name` does not blank the title. */
export function scrapedMusicInfo(
  candidate: ScrapeCandidate,
  fallbackTitle: string,
): Record<string, unknown> {
  const info: Record<string, unknown> = {
    title: orOmit(candidate.name) ?? fallbackTitle,
  };

  const optional: Array<[string, unknown]> = [
    ['artist', candidate.artist],
    ['album', candidate.album],
    ['album_img', candidate.album_img],
    ['year', candidate.year],
    // The bug this module exists for: the candidate's real genre, and
    // nothing at all when there isn't one.
    ['genre', candidate.genre],
    ['lyrics', orOmit(candidate.lyric) ?? orOmit(candidate.lyrics)],
  ];
  for (const [key, value] of optional) {
    const v = orOmit(value);
    if (v !== undefined) info[key] = v;
  }
  return info;
}

/** The directory part of a music-root-relative path, '' for a bare name. */
export function dirOf(relPath: string): string {
  const cut = relPath.lastIndexOf('/');
  return cut === -1 ? '' : relPath.slice(0, cut);
}

/** Group rows into one `select_data` list per directory.
 *
 *  The endpoint takes a single `file_full_path` and SafeJoins every
 *  `select_data[].name` onto it, so one request can only cover one
 *  directory. Rows are grouped rather than flattened so a selection that
 *  spans folders still writes the right tags to the right files. */
export function groupSelectionsByDir(
  rows: Array<{ fullPath: string }>,
  infoFor: (row: { fullPath: string }) => Record<string, unknown>,
): Map<string, BatchSelection[]> {
  const groups = new Map<string, BatchSelection[]>();
  for (const row of rows) {
    const dir = dirOf(row.fullPath);
    const list = groups.get(dir) ?? [];
    list.push({ name: baseNameOf(row.fullPath), music_info: infoFor(row) });
    groups.set(dir, list);
  }
  return groups;
}
