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

/** The tags a scrape candidate can write, in the order the UI shows them.
 *
 *  The list is the set of keys this module knows how to read off a
 *  candidate; the ids a source sends (artist_id / album_id) are NOT here,
 *  because a file's id3 frame has nowhere to put them and "apply the
 *  artist id" is not an action a user can mean. */
export const APPLYABLE_FIELDS = [
  'title',
  'artist',
  'album',
  'year',
  'genre',
  'album_img',
  'lyrics',
] as const;

export type ApplyableField = (typeof APPLYABLE_FIELDS)[number];

const FIELD_LABELS: Record<ApplyableField, string> = {
  title: '标题',
  artist: '艺术家',
  album: '专辑',
  year: '年份',
  genre: '流派',
  album_img: '封面',
  lyrics: '歌词',
};

export function fieldLabel(field: ApplyableField): string {
  return FIELD_LABELS[field] ?? field;
}

/** The tag value a candidate offers for one field, or undefined when it
 *  offers nothing. Undefined is the load-bearing part: applying a field
 *  the source did not fill in means "leave my tag alone", not "clear it". */
export function candidateTagValue(
  candidate: ScrapeCandidate,
  field: ApplyableField,
): string | undefined {
  if (field === 'lyrics') {
    return orOmit(candidate.lyric) ?? orOmit(candidate.lyrics);
  }
  const value = candidate[field as keyof ScrapeCandidate];
  return orOmit(value);
}

/** The tags to write for `candidate`, ready to hand to the batch endpoint.
 *
 *  `fallbackTitle` is the query that produced no usable title — the file's
 *  own name — so a hit with an empty `name` does not blank the title.
 *
 *  `fields` narrows the write to the ones asked for, which is what the
 *  candidate card's per-field 「应用」 does: taking one source's album but
 *  another source's genre is a normal thing to want, and "apply
 *  everything" makes it impossible. Omitted means all of them. */
export function scrapedMusicInfo(
  candidate: ScrapeCandidate,
  fallbackTitle: string,
  fields?: readonly ApplyableField[],
): Record<string, unknown> {
  const wanted = fields ?? APPLYABLE_FIELDS;
  const info: Record<string, unknown> = {};

  // Only ever a fallback when the title is one of the fields being
  // written. Asking for just the genre must not also rewrite the title.
  if (wanted.includes('title')) {
    info.title = orOmit(candidate.name) ?? fallbackTitle;
  }

  for (const field of wanted) {
    if (field === 'title') continue;
    const value = candidateTagValue(candidate, field);
    if (value !== undefined) info[field] = value;
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
