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

/** The lyric a candidate can actually offer, preferring one already
 *  fetched over anything the search carried.
 *
 *  A search response never carries lyrics — the proto Song message has no
 *  lyric field — so `lyric`/`lyrics` on a fresh candidate are always empty
 *  and a fetched one lives outside the candidate entirely, in the parent's
 *  cache. A card that shows a lyric it fetched must hand THAT to the apply
 *  path; passing the raw candidate applies an empty field, and the user has
 *  to open 详情 and fetch the lyric a second time.
 *
 *  Returns undefined rather than '' so callers can use it as "there is
 *  nothing here" without having to remember that '' is a real (empty) lyric
 *  the user already fetched. */
export function effectiveCandidateLyric(
  candidate: ScrapeCandidate,
  cachedLyric?: string,
): string | undefined {
  return (
    orOmit(cachedLyric) ?? orOmit(candidate.lyric) ?? orOmit(candidate.lyrics)
  );
}

/** The candidate as an apply should receive it, with a fetched lyric folded
 *  in.
 *
 *  Narrow on purpose: only the lyric is overridden. Every other field must
 *  come from the candidate, so a card cannot accidentally "apply" something
 *  the user did not choose. A candidate with no lyric available comes back
 *  unchanged, which keeps the "leave my tag alone" rule for lyrics intact
 *  rather than overwriting with an empty string.
 *
 *  Every apply path that can be reached after the user pressed 获取歌词 must
 *  go through here — the whole-candidate 应用此标签 and the per-field
 *  应用歌词 alike. They previously disagreed: the per-field button spread
 *  the lyric in by hand and the whole-candidate button passed the raw
 *  candidate, which is why the preview showed a lyric that 保存 did not
 *  write. */
export function candidateWithLyric<T extends ScrapeCandidate>(
  candidate: T,
  cachedLyric?: string,
): T {
  const lyric = effectiveCandidateLyric(candidate, cachedLyric);
  return lyric ? { ...candidate, lyric } : candidate;
}

/** What to actually search a scrape source for.
 *
 *  This exists because the precedence used to be baked into a component and
 *  got it wrong. The candidate search seeded a `searchQuery` state from the
 *  row's title ONCE, on open, and never updated it when the user edited the
 *  title — while the search preferred `searchQuery` over the live title. So
 *  correcting a wrong title, which is the single most common reason to search
 *  candidates at all, searched the old title and returned matches for the
 *  thing the user had just decided was wrong. The symptom read as "the search
 *  ignores my edit".
 *
 *  Precedence, highest first:
 *    1. `explicit` — a query handed in by the caller (the fingerprint search,
 *       or the source picker's "search these sources for this").
 *    2. `override` — what the user typed into the search box. Only set once
 *       they type, and cleared by the UI when they go back to editing the
 *       title, so it can never silently shadow a title edit.
 *    3. the CURRENT title.
 *    4. the filename stem, for a row that never had a title at all.
 *
 *  Returns undefined when nothing usable is available, which the caller
 *  treats as "do not search" rather than searching for an empty string. */
export function resolveCandidateSearchQuery(
  args: {
    explicit?: string;
    override?: string;
    title?: string;
    fileName?: string;
  },
): string | undefined {
  const candidates = [args.explicit, args.override, args.title];
  for (const c of candidates) {
    const trimmed = (c ?? '').trim();
    if (trimmed) return trimmed;
  }
  // Filename last, and stripped of its extension — "song.flac" as a search
  // term matches nothing, while "song" is what the user meant.
  const stem = (args.fileName ?? '').replace(/\.[^/.]+$/, '').trim();
  return stem || undefined;
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
