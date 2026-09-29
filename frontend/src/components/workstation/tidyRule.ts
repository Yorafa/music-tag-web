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
// (add / remove / reorder), and the validation. The plan itself is
// rendered in localPreview.ts, from the rows' cached tags.
//
// Server: whether the destination is already occupied, whether two files
// want the same one, and the actual move. The first two need the disk,
// which the client cannot see, so they are not claimed either way — the
// dialog says so rather than implying a clean bill of health.
//
// # Why the field list is duplicated
//
// TIDY_FIELDS mirrors utils.RenameTemplateFields, which the server enforces
// as an allow-list. The server is the authority; the worst a stale copy can
// do is let the user press a button and read a clear error naming the
// field. A round trip on every keystroke to keep a chip list honest would
// be a worse trade, and the chips have to be renderable to be useful.

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

/** Adds `${field}` to level `i`, or removes it if it is already there.
 *
 *  It was append-always, which was a bug wearing a disguise: the chip
 *  renders as PRESSED once the field is in the level, and clicking a
 *  pressed chip that appends anyway produced `${artist}${artist}` — and
 *  again on a second click, and a third. Toggling is what the pressed
 *  state already promises.
 *
 *  Appending rather than replacing stays the point for the ADD case: a
 *  level is a template that may be half fixed text, so clicking 专辑 on
 *  "${year} - " must give "${year} - ${album}" rather than discarding the
 *  separator. Removing takes out ONE occurrence, so a level that really
 *  did name the field twice keeps the other one.
 */
export function toggleField(segments: string[], i: number, field: TidyField): string[] {
  if (i < 0 || i >= segments.length) return segments;
  const level = segments[i];
  const token = '${' + field + '}';
  const at = level.indexOf(token);
  if (at === -1) return setLevel(segments, i, level + token);
  return setLevel(segments, i, level.slice(0, at) + level.slice(at + token.length));
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

/** Whether a tidy can be submitted at all.
 *
 *  Just the rule and a non-empty selection. The root is NOT a gate, and
 *  that is the fix for a real dead button: the old version required one
 *  and the client cannot know the server's absolute MUSIC_DIR, so the
 *  only way past it was to guess a server-side path. An EMPTY root is
 *  valid and is the default — the server resolves it to the library root
 *  (tasks.TidyRoot). */
export function canTidy(segments: string[], rowCount: number): boolean {
  return rowCount > 0 && segmentsProblem(segments) === null;
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
