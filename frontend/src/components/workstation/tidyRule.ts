// The client half of 整理目录 — the ordered list of directory levels, and
// everything about a level list that can be decided without the server.
//
// The shape is deliberately the one 解析文件名 and 从标签改名 already use:
// pick a preset, edit it, read the plan, apply. The third dialog in that
// family needs no explanation, and that is the point — the old one was a
// three-box form that nobody could predict the result of.
//
// # What lives here vs. the server
//
// Here: which fields a level may name, the preset shapes, list surgery
// (add / remove / reorder), and a local render against a fixed example so
// the operator sees the shape of their rule without a round trip.
//
// Server: whether a field is actually set on THIS file, whether the
// destination is already occupied, whether two files want the same one,
// and the actual move. Those need the disk, and the last two are the
// reason the preview cannot be faked here.
//
// # Why the field list is duplicated
//
// TIDY_FIELDS mirrors utils.RenameTemplateFields, which the server enforces
// as an allow-list. The server is the authority; the worst a stale copy can
// do is let the user press a button and read a clear error naming the
// field. A round trip on every keystroke to keep a chip list honest would
// be a worse trade, and the chips have to be renderable to be useful.

import type { TidyPlanRow, TidyPlanStatus } from '@/api/client';

/** The fields a directory level may name, in the order the dialog lists
 *  them. Mirrors `utils.RenameTemplateFields` — the same eight the other
 *  two template dialogs use, so one set of chips serves all three. */
export const TIDY_FIELDS = [
  'artist',
  'album',
  'albumartist',
  'title',
  'genre',
  'year',
  'tracknumber',
  'discnumber',
] as const;

export type TidyField = (typeof TIDY_FIELDS)[number];

export const TIDY_FIELD_LABELS: Record<TidyField, string> = {
  artist: '艺术家',
  album: '专辑',
  albumartist: '专辑艺术家',
  title: '标题',
  genre: '流派',
  year: '年份',
  tracknumber: '音轨',
  discnumber: '碟片',
};

/** A named shape for the common library layouts.
 *
 *  These are the structures people actually keep their music in, and the
 *  old fixed two-field form could only ever offer the first two. The
 *  presets are editable starting points, not modes — picking one fills
 *  the level list and the operator is expected to change it, which is why
 *  they are not a radio group. */
export interface TidyPreset {
  id: string;
  label: string;
  hint: string;
  segments: string[];
}

export const TIDY_PRESETS: TidyPreset[] = [
  {
    id: 'artist-album',
    label: '艺术家 / 专辑',
    hint: '最常见的整理结构',
    segments: ['${artist}', '${album}'],
  },
  {
    id: 'artist-album-title',
    label: '艺术家 / 专辑 / 标题',
    hint: '文件名已含标题时，目录就不必再分一次',
    segments: ['${artist}', '${album}', '${title}'],
  },
  {
    id: 'genre-artist-year',
    label: '流派 / 艺术家 / 年份 - 专辑',
    hint: '一层里混用字段和固定文字；合辑库常用',
    segments: ['${genre}', '${artist}', '${year} - ${album}'],
  },
  {
    id: 'artist-album-disc',
    label: '艺术家 / 专辑 / 碟片',
    hint: '多碟专辑分开存放',
    segments: ['${artist}', '${album}', '${discnumber}'],
  },
  {
    id: 'artist-only',
    label: '仅艺术家',
    hint: '打平所有专辑到同一层',
    segments: ['${artist}'],
  },
];

/** The example the try-it box renders against, so the operator is looking
 *  at something shaped like their own library rather than at `${artist}`. */
export const TIDY_TRY_EXAMPLE: Partial<Record<TidyField, string>> = {
  title: '晴天',
  artist: '周杰伦',
  album: '叶惠美',
  albumartist: '周杰伦',
  genre: '流行',
  year: '2003',
  tracknumber: '3',
  discnumber: '1',
};

/** How many levels a single tidy may build.
 *
 *  Bounded rather than open-ended because each level is a directory the
 *  operator has to think about, and because the limit is what makes
 *  "${a}/${b}/${c}/${d}/${e}..." in one paste behave the same as clicking
 *  the add button five times. A list longer than this is a sign the
 *  operator meant a filename template, not a directory structure. */
export const MAX_TIDY_LEVELS = 6;

// ─── List surgery ──────────────────────────────────────────────────────────
//
// These return new arrays rather than mutating, so React state updates
// from a list editor stay predictable and each one is testable on its own.

/** Appends an empty level. Refuses past MAX_TIDY_LEVELS. */
export function addLevel(segments: string[]): string[] {
  if (segments.length >= MAX_TIDY_LEVELS) return segments;
  return [...segments, ''];
}

/** Removes level `i`. */
export function removeLevel(segments: string[], i: number): string[] {
  if (i < 0 || i >= segments.length) return segments;
  return segments.filter((_, idx) => idx !== i);
}

/** Moves level `i` by `delta` (-1 up, +1 down). Out-of-range moves are
 *  no-ops rather than clamped, because a button that silently moves the
 *  wrong row is worse than one that does nothing. */
export function moveLevel(segments: string[], i: number, delta: number): string[] {
  const j = i + delta;
  if (i < 0 || i >= segments.length || j < 0 || j >= segments.length) return segments;
  const out = [...segments];
  [out[i], out[j]] = [out[j], out[i]];
  return out;
}

/** Replaces level `i`. */
export function setLevel(segments: string[], i: number, value: string): string[] {
  if (i < 0 || i >= segments.length) return segments;
  const out = [...segments];
  out[i] = value;
  return out;
}

/** Appends `${field}` to level `i`, at the end.
 *
 *  Appending rather than replacing is the point: a level is a template
 *  that may be half fixed text, and clicking 专辑 should give you
 *  "${year} - ${album}", not "${album}" with your separator thrown away. */
export function appendField(segments: string[], i: number, field: TidyField): string[] {
  if (i < 0 || i >= segments.length) return segments;
  return setLevel(segments, i, segments[i] + '${' + field + '}');
}

// ─── Validation ────────────────────────────────────────────────────────────

/** Why level `i` is unusable, or null if it is fine.
 *
 *  Deliberately NOT the server's "a level must contain a placeholder"
 *  rule. That rule exists for filenames, where a template with no
 *  placeholder renames every file to the same literal name and the
 *  collision check catches it N times instead of once. A directory level
 *  has no such failure mode — 「Live」 and 「合辑」 are legitimate fixed
 *  groupings, and refusing them would forbid the case this feature
 *  exists for. The unknown-field half is what matters, and both sides
 *  enforce it. */
export function levelProblem(level: string): string | null {
  const t = level.trim();
  if (t === '') return '这一层是空的';
  const open = (t.match(/\$\{/g) ?? []).length;
  const close = (t.match(/\}/g) ?? []).length;
  if (open !== close) return '有 ${ 没有配对的 }';
  for (const m of t.matchAll(/\$\{([^}]*)\}/g)) {
    const key = m[1].trim();
    if (key === '') return '有一个 ${} 忘了写字段名';
    if (!TIDY_FIELDS.includes(key as TidyField)) {
      return `未知字段 ${key}，可用：${TIDY_FIELDS.join(' / ')}`;
    }
  }
  return null;
}

/** Why the whole list is unusable, or null.
 *
 *  The first offending level wins: a dialog listing six problems for six
 *  half-typed levels is noise, and the one being typed right now is the
 *  one being looked at. */
export function segmentsProblem(segments: string[]): string | null {
  if (segments.length === 0) return '至少需要一层目录';
  for (let i = 0; i < segments.length; i++) {
    const p = levelProblem(segments[i]);
    if (p) return `第 ${i + 1} 层：${p}`;
  }
  return null;
}

/** Whether there is anything to preview. */
export function canPreview(
  segments: string[],
  rootPath: string,
  pathCount: number,
): boolean {
  return pathCount > 0 && rootPath.trim() !== '' && segmentsProblem(segments) === null;
}

// ─── Local preview ─────────────────────────────────────────────────────────

/** The fields named by a level, in order, for lighting up chips. */
export function fieldsFromLevel(level: string): TidyField[] {
  const out: TidyField[] = [];
  for (const m of level.matchAll(/\$\{([^}]*)\}/g)) {
    const key = m[1].trim() as TidyField;
    if (TIDY_FIELDS.includes(key) && !out.includes(key)) out.push(key);
  }
  return out;
}

/** What one level does, in one sentence. */
export function describeLevel(level: string): string {
  const t = level.trim();
  if (t === '') return '空层';
  const fields = fieldsFromLevel(t);
  if (fields.length === 0) return `固定目录名「${t}」`;
  const named = fields.map((f) => TIDY_FIELD_LABELS[f]).join(' + ');
  // A level that is nothing BUT its placeholders is the common case and
  // needs no "自定义" hedge; one with fixed text around them is the case
  // worth naming, because the operator should know their separator is part
  // of the directory name.
  return t === fields.map((f) => '${' + f + '}').join('') ? named : `${named}（含固定文字）`;
}

/** Render one level against a fixed example, locally.
 *
 *  A preview of a PREVIEW — the server renders against each file's real
 *  tags. It exists so the operator sees the shape of the structure without
 *  waiting for a round trip, and it is honest about the one thing that
 *  matters: an empty field leaves its separators behind, because that is
 *  what the server does too. Hiding it here would make the two disagree
 *  the moment one of them was wrong. */
export function tryLevel(
  level: string,
  values: Partial<Record<TidyField, string>>,
): { name: string; missing: TidyField[] } {
  const missing: TidyField[] = [];
  const name = level.replace(/\$\{([^}]*)\}/g, (_m, raw: string) => {
    const key = raw.trim() as TidyField;
    if (!TIDY_FIELDS.includes(key)) return '';
    const v = (values[key] ?? '').trim();
    if (v === '') missing.push(key);
    return v;
  });
  return { name: name.trim(), missing };
}

/** The whole structure, rendered against the example, as a path the
 *  operator can read top to bottom. */
export function tryStructure(
  segments: string[],
  values: Partial<Record<TidyField, string>> = TIDY_TRY_EXAMPLE,
): string[] {
  return segments.map((s) => {
    // `missing` is deliberately dropped here: the per-level row in the
    // dialog already shows which fields were empty against this same
    // example, and repeating it in the joined path would say the same
    // thing twice in two places.
    const { name } = tryLevel(s, values);
    // The 未知 fallback is the server's, reproduced here so the example
    // does not show a level that silently disappears.
    return name === '' ? '未知' : name;
  });
}

// ─── The plan ──────────────────────────────────────────────────────────────

export interface TidyTally {
  total: number;
  move: number;
  same: number;
  taken: number;
  duplicate: number;
  blocked: number;
  withGaps: number;
}

/** Fallback tally for a plan the server has not sent yet.
 *
 *  Only used to keep the summary line from flickering during a refresh;
 *  the authoritative counts come from the server's own `TallyTidy`, and
 *  that is what gates the apply button. Duplicated here rather than
 *  imported because the server shape is a flat record and this is a
 *  narrower one — a cast would hide a real mismatch. */
export function tallyPlan(rows: TidyPlanRow[]): TidyTally {
  const t: TidyTally = {
    total: rows.length,
    move: 0,
    same: 0,
    taken: 0,
    duplicate: 0,
    blocked: 0,
    withGaps: 0,
  };
  for (const r of rows) {
    switch (r.status) {
      case 'move':
        t.move += 1;
        if ((r.missing?.length ?? 0) > 0) t.withGaps += 1;
        break;
      case 'same':
        t.same += 1;
        break;
      case 'taken':
        t.taken += 1;
        break;
      case 'duplicate':
        t.duplicate += 1;
        break;
      case 'blocked':
        t.blocked += 1;
        break;
    }
  }
  return t;
}

/** The one-sentence summary above the plan table.
 *
 *  The refused rows are named separately from the moved ones rather than
 *  folded into a total. "412 首已整理" and "412 首已整理，3 首冲突未动" are
 *  very different sentences about the same batch, and only the second one
 *  tells the operator they have work left. */
export function describeTally(t: TidyTally): string {
  if (t.total === 0) return '没有可整理的文件';
  const parts: string[] = [];
  if (t.move > 0) {
    parts.push(`${t.move} 首将移动`);
    if (t.withGaps > 0) parts.push(`其中 ${t.withGaps} 首缺少标签，目录名会有空缺`);
  }
  if (t.same > 0) parts.push(`${t.same} 首已在正确位置`);
  const refused = t.taken + t.duplicate + t.blocked;
  if (refused > 0) {
    const why: string[] = [];
    if (t.taken > 0) why.push(`${t.taken} 首目标已有文件`);
    if (t.duplicate > 0) why.push(`${t.duplicate} 首本批次内重名`);
    if (t.blocked > 0) why.push(`${t.blocked} 首无法处理`);
    parts.push(`${refused} 首不动（${why.join('，')}）`);
  }
  return parts.join('，');
}

/** The files the plan approved, as the paths the apply should send.
 *
 *  A row marked `taken` / `duplicate` / `blocked` is one the operator has
 *  already been shown and has accepted as "this one does not move".
 *  Sending it anyway would make the worker fail it at `os.Rename`, and
 *  that failure would arrive as a second, separate fact — in the audit
 *  log, hours later — for something the plan already explained.
 *
 *  `same` is included deliberately. Those files are where they should be,
 *  and the worker's re-plan turns them into a no-op move; filtering them
 *  out here would silently shrink the batch below what the operator saw.
 */
export function movablePathsFromPlan(rows: TidyPlanRow[]): string[] {
  return rows
    .filter((r) => r.status === 'move' || r.status === 'same')
    .map((r) => r.old_path);
}

/** Row colour / label for the plan table. */
export function statusLabel(status: TidyPlanStatus): string {
  switch (status) {
    case 'move':
      return '将移动';
    case 'same':
      return '已在位';
    case 'taken':
      return '目标被占';
    case 'duplicate':
      return '批次内重名';
    case 'blocked':
      return '无法处理';
  }
}

/** The reason column, preferring the server's `reason` and falling back to
 *  the missing-field list so a row is never blank-but-ok. */
export function statusDetail(row: TidyPlanRow): string {
  if (row.reason) return row.reason;
  const missing = row.missing ?? [];
  if (missing.length > 0) {
    return `缺少 ${missing.map((m) => TIDY_FIELD_LABELS[m as TidyField] ?? m).join('、')}`;
  }
  return '';
}
