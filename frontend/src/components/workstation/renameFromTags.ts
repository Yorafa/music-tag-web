// The client half of 从标签改名 (rename from tags) — the inverse of
// 解析文件名, and deliberately the same shape so the second dialog needs
// no explanation: pick a template, try it, read the plan, apply.
//
// What lives here is everything that can be decided without the server:
// which fields a template may name, what a template is worth, what it
// will do to one example, and how to count the plan. The server owns the
// filesystem, the collision rules and the actual rename.
//
// # Why the field list is duplicated
//
// RENAME_FIELDS mirrors utils.RenameTemplateFields. The server is the
// authority and rejects anything outside its list with a 400 naming the
// offender, so the worst a stale copy can do is let the user press a
// button and read a clear error. A hidden second source of truth would
// be worse, but so would a round trip on every keystroke, and the chips
// have to be renderable to be useful at all.

import type { RenamePlanRow } from '@/api/client';

/** The fields a filename template may name, in the order the dialog
 *  lists them. Mirrors `utils.RenameTemplateFields`. */
export const RENAME_FIELDS = [
  'title',
  'artist',
  'album',
  'albumartist',
  'genre',
  'year',
  'tracknumber',
  'discnumber',
] as const;

export type RenameField = (typeof RENAME_FIELDS)[number];

/** Column labels, in the same order. */
export const RENAME_FIELD_LABELS: Record<RenameField, string> = {
  title: '标题',
  artist: '艺术家',
  album: '专辑',
  albumartist: '专辑艺术家',
  genre: '流派',
  year: '年份',
  tracknumber: '音轨',
  discnumber: '碟片',
};

/** What the dialog runs when the box is empty — nothing. Unlike the
 *  parse dialog there is no meaningful default: a filename template
 *  always has to be stated, because guessing one is how a library gets
 *  renamed into uselessness. */
export const DEFAULT_RULE_TEXT =
  '留空则不执行任何改名。选一个预设或自己拼一条规则。';

/** A named template for the common library shapes. */
export interface RenamePreset {
  id: string;
  label: string;
  hint: string;
  fields: RenameField[];
}

export const RENAME_PRESETS: RenamePreset[] = [
  {
    id: 'artist-title',
    label: '艺术家 - 标题',
    hint: '最常见；与「解析文件名」的默认规则互为逆运算',
    fields: ['artist', 'title'],
  },
  {
    id: 'track-artist-title',
    label: '音轨 - 艺术家 - 标题',
    hint: '音轨在最前，便于播放器按顺序排',
    fields: ['tracknumber', 'artist', 'title'],
  },
  {
    id: 'artist-album-title',
    label: '艺术家 - 专辑 - 标题',
    hint: '专辑在中间',
    fields: ['artist', 'album', 'title'],
  },
  {
    id: 'track-title',
    label: '音轨 - 标题',
    hint: '同一专辑内多产 artists 时用',
    fields: ['tracknumber', 'title'],
  },
  {
    id: 'artist-year-title',
    label: '艺术家 - 年份 - 标题',
    hint: '合辑/现场专辑常这样命名',
    fields: ['artist', 'year', 'title'],
  },
  {
    id: 'disc-track-title',
    label: '碟片 - 音轨 - 标题',
    hint: '经典专辑（CD 按碟片分）',
    fields: ['discnumber', 'tracknumber', 'title'],
  },
];

/** Build the template text for a chosen field order.
 *
 *  It joins on " - " and nothing else. There is deliberately no
 *  zero-padding of track numbers here even though `3 - Title` sorts
 *  before `10 - Title`: the value comes from the file's own tag, so
 *  padding would have to be applied to the tag, not to the filename,
 *  and a template that pretended otherwise would be lying about what it
 *  controls. An operator who wants padded numbers fixes the tag. */
export function buildTemplate(fields: readonly RenameField[]): string {
  if (fields.length === 0) return '';
  return fields.map((f) => `\${${f}}`).join(' - ');
}

/** Validate a template without calling the server.
 *
 *  `patternProblem`'s sibling for placeholders: the same three failure
 *  kinds the server reports, caught early so the field can say why
 *  inline. It is deliberately permissive about everything else —
 *  separators, padding, whatever the operator wants — because the point
 *  is to stop the two ways a template is unusable, not to legislate. */
export function templateProblem(template: string): string | null {
  const t = template.trim();
  if (t === '') return null; // empty means "no rename", handled by the button
  if (!t.includes('${')) {
    return '模板里没有 ${字段}；照字面重命名会把所有文件改成同一个名字';
  }
  // An unterminated placeholder: "${artist" is a typo, and the
  // permissive renderer used to leave it in the filename verbatim.
  const open = (t.match(/\$\{/g) ?? []).length;
  const close = (t.match(/\}/g) ?? []).length;
  if (open !== close) return '有 ${ 没有配对的 }';
  for (const m of t.matchAll(/\$\{([^}]*)\}/g)) {
    const key = m[1].trim();
    if (key === '') return '有一个 ${} 忘了写字段名';
    if (!RENAME_FIELDS.includes(key as RenameField)) {
      return `未知字段 ${key}，可用：${RENAME_FIELDS.join(' / ')}`;
    }
  }
  return null;
}

/** Read the field order back out, so chips light up for a generated
 *  template and grey out for a hand-typed one it cannot summarise. */
export function fieldsFromTemplate(template: string): RenameField[] {
  const out: RenameField[] = [];
  for (const m of template.matchAll(/\$\{([^}]*)\}/g)) {
    const key = m[1].trim() as RenameField;
    if (RENAME_FIELDS.includes(key) && !out.includes(key)) out.push(key);
  }
  return out;
}

/** What the template does, in one sentence. Same three states as the
 *  parse dialog's `describeRule`: nothing, generated, or hand-typed. */
export function describeTemplate(template: string): string {
  const t = template.trim();
  if (t === '') return DEFAULT_RULE_TEXT;
  const fields = fieldsFromTemplate(t);
  if (fields.length === 0) return '自定义模板（无法自动解读）';
  const order = fields.map((f) => RENAME_FIELD_LABELS[f]).join(' → ');
  if (t === buildTemplate(fields)) return `按顺序取：${order}`;
  return `自定义模板，取：${order}`;
}

/** Render a template against one example, locally.
 *
 *  This is a preview of a PREVIEW — the server computes the real names
 *  from the real tags. It exists so the operator can see the shape of
 *  the rule without waiting for a round trip, and it is honest about
 *  the limit: an empty field leaves its separators, because that is
 *  what the server does too, and hiding it here would make the two
 *  disagree the moment one of them was wrong. */
export function tryTemplate(
  template: string,
  values: Partial<Record<RenameField, string>>,
): { name: string; missing: RenameField[] } {
  const missing: RenameField[] = [];
  const name = template.replace(/\$\{([^}]*)\}/g, (_m, raw: string) => {
    const key = raw.trim() as RenameField;
    if (!RENAME_FIELDS.includes(key)) return '';
    const v = (values[key] ?? '').trim();
    if (v === '') missing.push(key);
    return v;
  });
  return { name: name.trim(), missing };
}

/** The example the try-it box starts from, so the operator is looking
 *  at something shaped like their own library. */
export const TRY_EXAMPLE: Partial<Record<RenameField, string>> = {
  title: '晴天',
  artist: '周杰伦',
  album: '叶惠美',
  year: '2003',
  tracknumber: '3',
  discnumber: '1',
};

/** Counts for the summary line.
 *
 *  `ok` is the only bucket that means a file moves. `no_change` is
 *  called out separately rather than folded into it: "37 renamed, 3
 *  already correct" is a reassuring sentence and "37 renamed, 3 skipped"
 *  is not, even though the same three files did not move. */
export interface RenameTally {
  total: number;
  ok: number;
  noChange: number;
  taken: number;
  blocked: number;
  failed: number;
  /** Rows where at least one template field was empty. Counted
   *  separately from `ok` because those files DID get renamed, to a
   *  name with a gap in it. */
  withGaps: number;
}

export function tallyRename(rows: RenamePlanRow[]): RenameTally {
  const t: RenameTally = {
    total: rows.length,
    ok: 0,
    noChange: 0,
    taken: 0,
    blocked: 0,
    failed: 0,
    withGaps: 0,
  };
  for (const r of rows) {
    switch (r.status) {
      case 'ok':
        t.ok += 1;
        break;
      case 'no_change':
        t.noChange += 1;
        break;
      case 'taken':
        t.taken += 1;
        break;
      case 'blocked':
        t.blocked += 1;
        break;
      default:
        t.failed += 1;
    }
    if ((r.missing?.length ?? 0) > 0) t.withGaps += 1;
  }
  return t;
}

/** True when at least one row will actually move, which is what the
 *  apply button is for. A plan of nothing but no_change rows is a
 *  correct answer and there is nothing to do about it. */
export function hasWorkToDo(t: RenameTally): boolean {
  return t.ok > 0;
}

/** A one-line explanation of a row that will not be renamed, for the
 *  plan list. Empty string for a row that will be. */
export function rowReason(r: RenamePlanRow): string {
  if (r.status === 'ok') return '';
  return r.detail || r.status;
}
