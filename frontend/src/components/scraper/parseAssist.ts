// Helping someone who has never written a regex use the 解析文件名 dialog.
//
// The pattern field is the feature's only real control, and a regex is a
// bad thing to ask of someone whose problem is "this folder is named
// wrong". So the dialog does not lead with a pattern box — it leads with
// a question in the user's own terms ("文件名里依次是: 艺术家、专辑、
// 标题"), answers it by generating the pattern, and shows the generated
// text so nothing is hidden. Someone who does know regexs can still type
// over it; the generated form is a starting point, not a cage.
//
// Everything here is pure so it can be tested against the Go parser's own
// fixture (see parseAssist.test.ts) — the one thing a generator like this
// must never do is generate a pattern that parses differently from what
// the person was shown.

import {
  PARSE_TAG_FIELDS,
  PARSE_TAG_LABELS,
  type ParseTagField,
  type ParsedPreviewRow,
} from '@/api/client';

export { PARSE_TAG_FIELDS, PARSE_TAG_LABELS };
export type { ParseTagField };

/** The rule the dialog runs when the pattern box is empty. Shown in full
 *  rather than described, because "it splits on dashes" hides the part
 *  people get wrong: which segment becomes what. */
export const DEFAULT_RULE_PARTS = ['artist', 'title'] as const;

/** What the default split does, in words. The first segment is the
 *  artist and everything after it rejoins as the title — so
 *  `A - B - C` is artist `A`, title `B - C`, flagged `ambiguous` because
 *  the parser is saying it guessed. */
export const DEFAULT_RULE_TEXT = '按 “- _ / \\ | ·” 切分：第一段 = 艺术家，其余 = 标题';

/** The pattern a user's stated order generates. This is the literal text
 *  shown in the box, so the two can never disagree.
 *
 *  `\\d+` for the numeric fields, `.+?` for the rest, `.+` for the last
 *  one: the last group must be greedy or the title truncates at the first
 *  separator, and the middle ones must be lazy or the first field eats
 *  the whole name. */
export function buildPattern(fields: readonly ParseTagField[]): string {
  if (fields.length === 0) return '';
  const body = fields
    .map((f, i) => {
      const expr = fieldExpr(f, i === fields.length - 1);
      return `(?P<${f}>${expr})`;
    })
    .join(' - ');
  return `^${body}$`;
}

/** The capture expression for one field. Numeric fields get `\\d+` because
 *  accepting letters there produces a track number of "Bonus" and a year
 *  of "Deluxe"; the last group gets `.+` so it runs to the end. */
function fieldExpr(f: ParseTagField, isLast: boolean): string {
  switch (f) {
    case 'year':
      return '\\d{4}';
    case 'tracknumber':
    case 'discnumber':
      return '\\d+';
    default:
      return isLast ? '.+' : '.+?';
  }
}

/** A named preset, so the common shapes are one click instead of a regex.
 *  `fields` is what the person chose; `example` is a filename in that
 *  shape, used by the try-it box so the preset proves itself on screen. */
export interface PatternPreset {
  id: string;
  /** Short name shown on the button. */
  label: string;
  /** What this shape is for, in one clause. */
  hint: string;
  /** The order the user picks, in reading order. */
  fields: ParseTagField[];
  example: string;
}

export const PATTERN_PRESETS: PatternPreset[] = [
  {
    id: 'artist-title',
    label: '艺术家 - 标题',
    hint: '最常见的两段式',
    fields: ['artist', 'title'],
    example: '周杰倫 - 晴天.flac',
  },
  {
    id: 'track-artist-title',
    label: '音轨 - 艺术家 - 标题',
    hint: '音轨在最前，如 01 - Artist - Song',
    fields: ['tracknumber', 'artist', 'title'],
    example: '01 - 周杰倫 - 晴天.flac',
  },
  {
    id: 'artist-album-title',
    label: '艺术家 - 专辑 - 标题',
    hint: '专辑在中间',
    fields: ['artist', 'album', 'title'],
    example: '周杰伦 - 叶惠美 - 晴天.flac',
  },
  {
    id: 'artist-album-track-title',
    label: '艺术家 - 专辑 - 音轨 - 标题',
    hint: '四段式，最完整的一种',
    fields: ['artist', 'album', 'tracknumber', 'title'],
    example: 'Radiohead - OK Computer - 01 - Airbag.flac',
  },
  {
    id: 'artist-year-title',
    label: '艺术家 - 年份 - 标题',
    hint: '年份在中间',
    fields: ['artist', 'year', 'title'],
    example: '窦唯 - 1994 - 黑梦.flac',
  },
  {
    id: 'artist-disc-track-title',
    label: '艺术家 - 碟片 - 音轨 - 标题',
    hint: '碟片号在音轨前',
    fields: ['artist', 'discnumber', 'tracknumber', 'title'],
    example: 'Beatles - 1 - 03 - Something.flac',
  },
];

/** Read the field order back out of a pattern, so picking a preset or
 *  reordering chips lights up the matching chips. Returns [] for a pattern
 *  this generator did not write (hand-typed, or no named groups) — the
 *  caller then shows nothing selected rather than guessing. */
export function fieldsFromPattern(pattern: string): ParseTagField[] {
  const js = pattern.replace(/\(\?P</g, '(?<');
  const out: ParseTagField[] = [];
  for (const m of js.matchAll(/\(\?<([^>]+)>/g)) {
    const n = m[1] as ParseTagField;
    if (PARSE_TAG_FIELDS.includes(n) && !out.includes(n)) out.push(n);
  }
  return out;
}

/** What the pattern box currently does, as a sentence in the user's terms.
 *
 *  Distinguishes three states on purpose: the default split, a pattern
 *  this dialog generated, and a hand-typed one it cannot summarise. In
 *  the third case saying nothing beats describing a regex the reader
 *  cannot check. */
export function describeRule(pattern: string): string {
  const p = pattern.trim();
  if (p === '') return DEFAULT_RULE_TEXT;
  const fields = fieldsFromPattern(p);
  if (fields.length === 0) return '自定义规则（无法自动解读，请对照下方示例自查）';
  if (p === buildPattern(fields)) {
    return '按顺序取：' + fields.map((f) => PARSE_TAG_LABELS[f]).join(' → ');
  }
  return `自定义规则，取：${fields.map((f) => PARSE_TAG_LABELS[f]).join(' → ')}`;
}

/** One field's outcome in the try-it box. */
export interface TryResult {
  field: ParseTagField;
  value: string;
}

/** Run the pattern against a sample filename, locally, so the user can
 *  see the effect before spending a preview round trip.
 *
 *  This mirrors the Go parser's steps in order — NFC, strip the
 *  extension, apply the pattern — because a try-it box that disagrees
 *  with the server is worse than none. The two known limits, stated
 *  rather than hidden:
 *
 *   - An empty pattern runs the DEFAULT split, not the pattern, so the
 *     result here is what the server will do too.
 *   - The sample goes through the same code path, so agreement is
 *     structural; parseAssist.test.ts pins it to the Go fixture so a
 *     divergence shows up as a test failure rather than a surprise.
 *
 *  Returns null fields rather than empty strings for groups that did not
 *  participate — "the rule did not capture an album" and "the album is
 *  now empty" are different statements, and the tag-write contract draws
 *  the same line. */
export function tryPattern(
  filename: string,
  pattern: string,
): { results: TryResult[]; status: 'ok' | 'ambiguous' | 'unparsable' } {
  const stem = stripExt(filename);
  if (stem === '') return { results: [], status: 'unparsable' };

  const p = pattern.trim();
  if (p === '') {
    // Default split: separator class, first part artist, rest title.
    const parts = stem
      .split(/\s*[-_/\\|·]\s*/)
      .map((x) => x.trim())
      .filter((x) => x !== '');
    if (parts.length < 2) return { results: [], status: 'unparsable' };
    const results: TryResult[] = [
      { field: 'artist', value: parts[0] },
      { field: 'title', value: parts.slice(1).join(' - ') },
    ];
    return {
      results,
      status: parts.length === 2 ? 'ok' : 'ambiguous',
    };
  }

  let re: RegExp;
  try {
    re = new RegExp(p.replace(/\(\?P</g, '(?<'), 'u');
  } catch {
    return { results: [], status: 'unparsable' };
  }
  const m = re.exec(stem);
  if (m === null) return { results: [], status: 'unparsable' };

  const results: TryResult[] = [];
  const names = groupNames(p);
  for (let i = 1; i < m.length; i++) {
    const v = (m[i] ?? '').trim();
    if (v === '') continue;
    const field = names[i];
    if (field === null) continue;
    results.push({ field, value: v });
  }
  if (results.length === 0) return { results: [], status: 'unparsable' };
  return { results, status: 'ok' };
}

/** Group names in capture order, index 0 being the whole match — the
 *  shape Go's `SubexpNames()` returns, so the two can be walked
 *  identically. A non-capturing group or a flag setting contributes a
 *  null; that is why this cannot be a plain regex over the pattern. */
function groupNames(pattern: string): (ParseTagField | null)[] {
  const js = pattern.replace(/\(\?P</g, '(?<');
  const names: (ParseTagField | null)[] = [null];
  let inClass = false;
  for (let i = 0; i < js.length; i++) {
    const c = js[i];
    if (c === '\\') {
      i++; // the escaped character cannot start a group
      continue;
    }
    if (inClass) {
      if (c === ']') inClass = false;
      continue;
    }
    if (c === '[') {
      inClass = true;
      continue;
    }
    if (c !== '(') continue;

    if (js.startsWith('(?<', i) && js[i + 3] !== '=' && js[i + 3] !== '!') {
      const end = js.indexOf('>', i);
      if (end < 0) break;
      const name = js.slice(i + 3, end);
      names.push(PARSE_TAG_FIELDS.includes(name as ParseTagField)
        ? (name as ParseTagField)
        : null);
      i = end;
      continue;
    }
    // `(?...)` covers both non-capturing groups and flag settings; neither
    // captures, so neither shifts the index.
    if (js[i + 1] === '?') continue;
    names.push(null);
  }
  return names;
}

/** Everything before the LAST '.', matching the Go side: a basename that
 *  starts with '.' has no stem. */
function stripExt(name: string): string {
  const idx = name.lastIndexOf('.');
  if (idx <= 0) return '';
  return name.slice(0, idx);
}

/** A client-side mirror of the server's `CompilePattern` rejection, used
 *  to disable the re-parse button before spending a request.
 *
 *  The server remains the authority — it re-validates and returns a 400
 *  naming the offending group. This only saves the round trip, and it is
 *  deliberately conservative: a pattern this accepts is not proof the
 *  server will, it only means this found nothing obviously wrong. */
export function patternProblem(pattern: string): string | null {
  const p = pattern.trim();
  if (p === '') return null;

  // The pattern is typed once and sent to Go, so it must be spelled the Go
  // way (`(?P<name>...)`). `new RegExp` rejects that spelling outright —
  // verified, not assumed: it throws "Invalid group" for `(?P<a>x)`. So
  // the compile check has to translate first, or every valid pattern
  // reads as invalid in the browser and never reaches the server.
  //
  // Only `(?P<` is rewritten. `(?P=name)` and `(?P>name)` are different Go
  // syntaxes and stay untouched, which means a pattern using them fails
  // here for a reason the message does not explain — acceptable, since
  // RE2 rejects them too and the server says so properly.
  const jsPattern = p.replace(/\(\?P</g, '(?<');
  try {
    new RegExp(jsPattern);
  } catch {
    return '正则表达式无法编译';
  }

  // Now check the names against the allow-list. The server does this too
  // and is the authority; catching it here saves a round trip and lets
  // the field show the reason inline instead of as a toast.
  for (const m of jsPattern.matchAll(/\(\?<([^>]*)>/g)) {
    const n = m[1];
    if (n === '') return '命名分组缺少字段名';
    if (!PARSE_TAG_FIELDS.includes(n as ParseTagField)) {
      return `未知字段 ${n}，可用字段：${PARSE_TAG_FIELDS.join(' / ')}`;
    }
  }
  return null;
}

/** The combined client-side complaint, or null when the pattern is fine
 *  here. Exists so the dialog has one place to ask and the field shows a
 *  single reason rather than two competing ones. */
export function patternHelp(pattern: string): {
  problem: string | null;
  note: string | null;
} {
  const p = pattern.trim();
  if (p === '') return { problem: null, note: null };

  // Order matters, and the order is not arbitrary. The RE2 check has to
  // run FIRST: `(?<=a)` and `(?!x)` both start with `(?<`, so the
  // name-scanner in patternProblem reads `=a` as a capture name and
  // reports "未知字段 =a" — a message about a group the user never wrote,
  // which hides the real problem. Caught by the lookbehind test.
  //
  // Go's RE2 rejects lookahead, lookbehind and backreferences. V8 accepts
  // all three, so without this the box calls the pattern fine and the
  // server 400s on the next click. Verified against regexp.Compile, not
  // assumed: RE2 says "invalid or unsupported Perl syntax".
  const unsupported = findUnsupportedPerl(p);
  if (unsupported !== null) {
    return {
      problem: `不支持 ${unsupported}：服务端用 Go 的 RE2，不支持前瞻与反向引用`,
      note: null,
    };
  }

  const basic = patternProblem(p);
  if (basic !== null) return { problem: basic, note: null };
  return { problem: null, note: null };
}

function findUnsupportedPerl(p: string): string | null {
  // Only look outside character classes; `[(?=]` is a literal.
  let inClass = false;
  for (let i = 0; i < p.length; i++) {
    const c = p[i];
    if (inClass) {
      if (c === '\\') {
        i++;
        continue;
      }
      if (c === ']') inClass = false;
      continue;
    }
    if (c === '[') {
      inClass = true;
      continue;
    }
    if (c === '(' && p[i + 1] === '?') {
      if (p.startsWith('(?=', i)) return '前瞻 (?=…)';
      if (p.startsWith('(?!', i)) return '否定前瞻 (?!…)';
      if (p.startsWith('(?<=', i)) return '反向引用 (?<=…)';
      if (p.startsWith('(?<!', i)) return '否定反向引用 (?<!…)';
    }
    if (c !== '\\') continue;
    // Escaped character: `\d`, `\.` and a literal backslash all land
    // here. A digit means a real backreference, which RE2 refuses. The
    // check has to live inside this scan rather than as a whole-string
    // `/\\[1-9]/` test, because a regex cannot see that this backslash
    // was itself escaped — `\\1` is a literal backslash then a one, and
    // the string-level test flagged it as a backreference.
    if (/^[1-9]$/.test(p[i + 1] ?? '')) return '反向引用 \\1';
    i++;
  }
  return null;
}

/** Exposed so a test can pin the "is it really a backreference?" scan
 *  directly; the scan itself is an implementation detail. */
export function findUnsupportedPerlForTest(p: string): string | null {
  return findUnsupportedPerl(p);
}

/** The example filename a preset should be tried against, used as the
 *  try-it box's initial content. */
export function presetById(id: string): PatternPreset | undefined {
  return PATTERN_PRESETS.find((p) => p.id === id);
}

/** Rows the preview already parsed, in the reading order of the table,
 *  so the try-it box and the real table can be compared side by side. */
export function firstParsedExample(results: ParsedPreviewRow[]): string {
  for (const r of results) {
    const i = r.path.lastIndexOf('/');
    const base = i >= 0 ? r.path.slice(i + 1) : r.path;
    if (base !== '') return base;
  }
  return '';
}

/** How a preview came out, as the counts the summary line shows.
 *
 *  Extracted from the component so the arithmetic is testable — the
 *  distinction that matters is `guessed` vs `skipped`. An ambiguous row
 *  DID parse; the parser split the name on a separator and guessed how
 *  many segments there were, and the worker WILL write it. Folding it
 *  into "unparsable" would tell the user fewer files were being written
 *  than actually are. */
export interface PreviewTally {
  total: number;
  matched: number;
  guessed: number;
  skipped: number;
}

export function tallyPreview(results: ParsedPreviewRow[]): PreviewTally {
  const matched = results.filter((r) => r.status === 'ok').length;
  const guessed = results.filter((r) => r.status === 'ambiguous').length;
  return {
    total: results.length,
    matched,
    guessed,
    skipped: results.length - matched - guessed,
  };
}
