// The local half of the two previews: 整理目录 and 从标签改名.
//
// # Why this is computed here and not asked for
//
// Both dialogs used to POST their rule to a preview endpoint before
// showing anything. That bought two things — a real read of each file's
// tags, and a collision check — at the price of a round trip on every
// change to the rule, which is the interaction the dialogs invite
// (pick a preset, look, adjust a level, look again).
//
// At the size a preview is actually shown — ten rows — the round trip is
// most of the latency and none of the value is in the tenth row. The user
// is looking to recognise the SHAPE of a rule, and the first ten of their
// own library show that perfectly well. The check the server was doing
// is the part that goes: whether a destination is occupied, and whether
// two files collide, need a stat of the target path, which the client has
// no data about. So those are simply not reported here, and the dialog
// says so rather than implying a clean bill of health.
//
// What the server still owns, unchanged: the actual write. The apply
// re-derives every path with the same rules the preview used to share
// (tasks.tidyDestPath / utils.ExpandFilenameTemplate), so a rule that
// renders differently here — a tag that changed since the cache, a
// sanitising rule we do not mirror — shows up as a per-file outcome in
// 操作审计 rather than as silent damage.
//
// # What the client CAN see, and does
//
// It has `row.musicInfo` — the tags from the last /api/music_id3/ — so it
// can render the rule against real files. It can also compare the
// rendered destinations against EACH OTHER, which is the collision people
// hit most and which costs nothing. It cannot know what is already on
// disk at the destination, so that check is not attempted; a row whose
// destination is already occupied will simply fail at write time, and
// that is stated in the dialog rather than hidden.

import { PREVIEW_LIMIT } from '@/lib/previewLimit';
import type { MusicTagInfo, WorklistRow } from '@/types';

/** The fields a rule may name, in the order the dialogs list them. */
export const LOCAL_FIELDS = [
  'artist',
  'album',
  'albumartist',
  'title',
  'genre',
  'year',
  'tracknumber',
  'discnumber',
] as const;

export type LocalField = (typeof LOCAL_FIELDS)[number];

/** A rule's variables, from whatever tags a row happens to have.
 *
 *  `musicInfo` is Partial and often absent — the cache is filled lazily by
 *  hydrateTags, and a row the user has never opened may still be empty.
 *  An absent tag is the SAME fact as an empty one here: this rule does
 *  not distinguish them, and inventing a difference would make the
 *  preview disagree with the server for no reason. */
export function templateVars(
  info: Partial<MusicTagInfo> | undefined,
): Record<LocalField, string> {
  const s = (v: unknown) => (typeof v === 'string' ? v.trim() : '');
  return {
    artist: s(info?.artist),
    album: s(info?.album),
    albumartist: s(info?.albumartist),
    title: s(info?.title),
    genre: s(info?.genre),
    year: s(info?.year),
    tracknumber: s(info?.tracknumber),
    discnumber: s(info?.discnumber),
  };
}

/** One rendered rule against one row. */
export interface Rendered {
  /** What the rule produced, trimmed. Empty when every field was blank. */
  text: string;
  /** Fields the rule named that this row does not have, in rule order.
   *  A warning, not an error: the server renders the same gap. */
  missing: LocalField[];
}

const PLACEHOLDER = /\$\{([^}]*)\}/g;

/** Render one rule against one row's tags.
 *
 *  An unknown key is left as the literal `${typo}` rather than dropped:
 *  the dialog validates keys before this runs, and a rule that somehow
 *  got here should look wrong rather than quietly lose a segment. */
export function renderRule(rule: string, vars: Record<LocalField, string>): Rendered {
  const missing: LocalField[] = [];
  const text = rule.replace(PLACEHOLDER, (_m, raw: string) => {
    const key = raw.trim() as LocalField;
    if (!LOCAL_FIELDS.includes(key)) return _m;
    const v = vars[key];
    if (v === '') missing.push(key);
    return v;
  });
  return { text: text.trim(), missing };
}

/** The stand-in the server uses for a level that renders to nothing.
 *  Mirrors `tasks.unknownDir`, because a local preview that dropped the
 *  level would show a shallower tree than the move will build. */
const UNKNOWN = '未知';

/** One row's preview verdict. */
export interface PlanRow {
  /** Row id (== fullPath), so React keys and callers agree with the store. */
  id: string;
  fileName: string;
  /** The rendered result: a directory path for tidy, a filename for
   *  rename. Empty only when the rule rendered nothing at all. */
  result: string;
  missing: LocalField[];
  /** Another previewed row already renders to the same `result`. Only
   *  ever true WITHIN the ten shown — never a statement about the rest
   *  of the selection. */
  clash: boolean;
  /** The file is already filed this way, so this row has nothing to do. */
  unchanged: boolean;
}

function basename(fullPath: string): string {
  const i = fullPath.lastIndexOf('/');
  return i === -1 ? fullPath : fullPath.slice(i + 1);
}

/** The rows to preview: the first PREVIEW_LIMIT of `rows`.
 *
 *  Head, not sample: the operator is recognising their rule from the top
 *  of their own selection, in the order they would see it anyway. */
export function previewRows(rows: WorklistRow[]): WorklistRow[] {
  return rows.slice(0, PREVIEW_LIMIT);
}

/** Mark the rows whose result another previewed row already claims.
 *
 *  Mutates nothing and compares only what is on screen, which is what
 *  makes the claim safe to make: with a batch of ten, "these two want the
 *  same name" is a fact, and "nothing else clashes" is not being claimed. */
function markClashes(plan: PlanRow[]): PlanRow[] {
  const seen = new Set<string>();
  for (const r of plan) {
    r.clash = r.result !== '' && seen.has(r.result);
    if (r.result !== '') seen.add(r.result);
  }
  return plan;
}

// ─── 整理目录 ───────────────────────────────────────────────────────────────

/** Preview a tidy locally: root_path/<levels>/<basename> per row.
 *
 *  `root` is the server-side destination root the operator typed. It is
 *  shown back verbatim, including when it is empty — empty means the
 *  library root, and printing the levels alone would hide which root the
 *  move will actually land in. */
export function localTidyPlan(
  rows: WorklistRow[],
  root: string,
  segments: string[],
): PlanRow[] {
  const base = root.trim().replace(/\/+$/, '');
  const plan = previewRows(rows).map((r) => {
    const name = basename(r.fullPath);
    const levels = segments.map((seg) => {
      const { text, missing } = renderRule(seg, templateVars(r.musicInfo));
      return { text: text === '' ? UNKNOWN : text, missing };
    });
    const joined = levels.map((l) => l.text).join('/');
    // Where the file would live, relative to the library — which is what
    // a row's fullPath already is, so the two are directly comparable.
    // Only meaningful when the destination is the library root; a typed
    // root is a server-side path the client cannot see into.
    const rel = joined === '' ? name : `${joined}/${name}`;
    return {
      id: r.id,
      fileName: name,
      // Joined from parts rather than interpolated, because the root may
      // legitimately be empty (the library root) and `${base}/${...}`
      // would render a leading slash — a path that looks absolute and is
      // not, and does not compare equal to the row's own fullPath.
      result: [base, joined, name].filter((p) => p !== '').join('/'),
      missing: levels.flatMap((l) => l.missing),
      clash: false,
      unchanged: base === '' && r.fullPath === rel,
    };
  });
  // The destination is the DIRECTORY a file moves into; two files in one
  // album share it on purpose, so only a clash on the full path counts.
  return markClashes(plan);
}

// ─── 从标签改名 ─────────────────────────────────────────────────────────────

/** Split a filename into stem and extension.
 *
 *  The server's rule: everything before the LAST '.', and a name starting
 *  with '.' has no stem. Matching it matters because the extension is
 *  carried over verbatim — a rename never changes the format. */
export function splitExt(name: string): { stem: string; ext: string } {
  const i = name.lastIndexOf('.');
  if (i <= 0) return { stem: name, ext: '' };
  return { stem: name.slice(0, i), ext: name.slice(i) };
}

/** Preview a rename locally: the rule plus the file's own extension. */
export function localRenamePlan(
  rows: WorklistRow[],
  template: string,
): PlanRow[] {
  const plan = previewRows(rows).map((r) => {
    const fileName = basename(r.fullPath);
    const { ext } = splitExt(fileName);
    const { text, missing } = renderRule(template, templateVars(r.musicInfo));
    const rendered = text === '' ? '' : text + ext;
    return {
      id: r.id,
      fileName,
      result: rendered,
      missing,
      clash: false,
      // Same name means there is nothing to do — the same fact the
      // server reports as no_change, and the one bucket that must never
      // be counted as work.
      unchanged: rendered === fileName,
    };
  });
  return markClashes(plan);
}
