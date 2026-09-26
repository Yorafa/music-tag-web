// Deciding what the 解析文件名 modal actually sends.
//
// Two separate questions, and the second one is where the bugs live:
//
//   1. What did the parser manage to read?       → `results` from the server
//   2. What did the person type?                 → the draft state
//
// The draft is a full grid (one cell per path per field) that starts empty,
// because an empty cell has to mean "use what the parser found" and a filled
// cell has to mean "use this instead". Collapsing that to "diff the two"
// after the fact is how a half-typed row ends up silently sending a blank
// title and erasing the one the parser got right.
//
// The second rule is about what does NOT travel. A field the draft leaves
// empty is omitted from the override entirely, which the server reads as
// "leave the parsed value alone". There is deliberately no way to say
// "clear this tag" from here: the wire format for that is `null` in the
// batch editor's payload, and reusing it here would mean a preview that
// looked like it could delete tags but could not. Filling in what a file
// is missing is this dialog's job; removing what it has is the batch
// editor's.
//
// Free of React so the rules can be tested directly — the same reason
// batchEdit.ts and renameResult.ts are separate modules.

import {
  PARSE_TAG_FIELDS,
  PARSE_TAG_LABELS,
  type ParsedPreviewRow,
  type ParseApplyOverride,
  type ParseTagField,
} from '@/api/client';

export { PARSE_TAG_FIELDS, PARSE_TAG_LABELS };
export type { ParseTagField };

/** The editable grid, keyed by the absolute path the server returned. */
export type OverrideDrafts = Record<string, Record<ParseTagField, string>>;

/** An all-empty draft row. Every field starts blank so "blank" keeps
 *  meaning "not overridden" no matter how many fields there are. */
export function emptyDraftRow(): Record<ParseTagField, string> {
  const row = {} as Record<ParseTagField, string>;
  for (const f of PARSE_TAG_FIELDS) row[f] = '';
  return row;
}

/** Seed the grid from a preview result set, one blank row per path. */
export function seedDrafts(results: ParsedPreviewRow[]): OverrideDrafts {
  const drafts: OverrideDrafts = {};
  for (const r of results) drafts[r.path] = emptyDraftRow();
  return drafts;
}

/** A row's draft, or a blank one if the grid has not been seeded for it
 *  (a result can arrive after the seed if a re-preview lands late). */
export function draftFor(
  drafts: OverrideDrafts,
  path: string,
): Record<ParseTagField, string> {
  return drafts[path] ?? emptyDraftRow();
}

/** The override entries to submit, in the order the rows are shown.
 *
 *  Rows the person did not touch are skipped entirely rather than sent
 *  as empty objects — the server would accept those, but a payload full
 *  of no-ops makes the "N 个覆盖" count in the button lie. */
export function buildOverrides(
  results: ParsedPreviewRow[],
  drafts: OverrideDrafts,
): ParseApplyOverride[] {
  const out: ParseApplyOverride[] = [];
  for (const r of results) {
    const draft = drafts[r.path];
    if (!draft) continue;
    const entry: ParseApplyOverride = { path: r.path };
    let touched = false;
    for (const f of PARSE_TAG_FIELDS) {
      const v = (draft[f] ?? '').trim();
      if (v !== '') {
        entry[f] = v;
        touched = true;
      }
    }
    if (touched) out.push(entry);
  }
  return out;
}

/** How many rows carry at least one manual value — the count the apply
 *  button shows. Deliberately rows, not cells: a person who fixes three
 *  fields on one track has corrected one track. */
export function changedRowCount(
  results: ParsedPreviewRow[],
  drafts: OverrideDrafts,
): number {
  return buildOverrides(results, drafts).length;
}

/** How many cells the person has typed into, for the "已改动" line. */
export function changedCellCount(drafts: OverrideDrafts): number {
  let n = 0;
  for (const row of Object.values(drafts)) {
    for (const f of PARSE_TAG_FIELDS) {
      if ((row[f] ?? '').trim() !== '') n += 1;
    }
  }
  return n;
}

/** Fields the parser found something for, per row, in reading order.
 *  The table hides a column's input for fields nothing was read into, so
 *  a default (pattern-less) preview does not show eight empty boxes. */
export function activeFields(results: ParsedPreviewRow[]): ParseTagField[] {
  return PARSE_TAG_FIELDS.filter((f) => results.some((r) => (r[f] ?? '') !== ''));
}

/** Whether any row the parser could not read exists. Drives the hint that
 *  says those rows can still be filled in by hand. */
export function hasUnreadable(results: ParsedPreviewRow[]): boolean {
  return results.some((r) => r.status !== 'ok');
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

/** An example pattern, shown as the input's placeholder. */
export const PATTERN_PLACEHOLDER =
  '^(?P<artist>.+?) - (?P<album>.+?) - (?P<title>.+)$';
