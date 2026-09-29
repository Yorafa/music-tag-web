// The rules of the three batch dialogs — 解析文件名 / 从标签改名 / 整理目录 —
// remembered across reloads.
//
// # Why these need remembering more than anything else in the toolbar
//
// They are the only controls in the app where the work is in the rule, not
// in the selection. The selection is re-picked every time (that is the
// point of a queue), but the rule is a decision the user makes once and
// then applies to batch after batch: a downloader's naming convention, a
// library's folder shape, a rename template. None of the three survive a
// reload today, and only 解析文件名 survives even a re-open — its pattern
// is lifted into WorkstationToolbar precisely because the modal's own
// state is thrown away when the dialog closes. So the rule is re-typed, or
// re-picked from presets, before every batch. The presets make that
// survivable for the simple cases, which is exactly why it went unnoticed:
// the dialog looks like it is only two clicks from ready.
//
// # Why it lives here and not in utils/
//
// The validators these rules need (`patternProblem`, `templateProblem`,
// `segmentsProblem`) live next to the dialogs that use them, and the whole
// point of validating on read is to ask the SAME question the dialog asks.
// Importing them beats copying the rules — a second copy of "what counts
// as a valid pattern" is a second answer, and the two would drift until
// remembered rules stopped matching what the dialog accepts. Nothing in
// utils/ imports from components/, and this does not start it. This is the
// same arrangement as tagSources.ts next door.
//
// # The sanitizers are the design
//
// Every value here comes back from localStorage, which is not a trusted
// source: a past version of the app, a hand-edit, or a half-finished write
// can leave anything there. Each rule fails differently when restored
// wrong, and each gets the treatment its failure deserves:
//
//   - A broken 解析 pattern or 改名 template falls back to empty, which
//     for both means "use the default rule" and "pick a rule" — a state
//     the dialog already has a first-class answer for. The alternative
//     (restore it and show the error) opens the dialog on a disabled
//     button, which reads as a broken feature rather than as a value that
//     could not be trusted.
//   - A broken 整理 level list falls back to the default two levels for
//     the same reason. Levels are validated as a list, not individually:
//     one unusable level invalidates the whole structure, because the list
//     IS the directory tree and a tree with a hole in it is not a tree.
//   - The 整理 root is only trimmed and capped. It is a path, every string
//     is a syntactically valid path, and whether it is inside the library
//     is a question only the server can answer — it already does, by
//     naming the fault in its 400. Guessing here would invent a rule the
//     user never agreed to.
//
// The stored values are capped because every write happens on a keystroke
// (the rename and tidy rules are typed into a box, not only picked from a
// preset), and an uncapped value is a value pasted by accident that gets
// written to disk on every character after it.

import { readJson, readString, writeJson, writeString } from '@/utils/persist';
import { patternProblem } from '@/components/scraper/parseAssist';
import { templateProblem } from '@/components/workstation/renameFromTags';
import { MAX_TIDY_LEVELS, segmentsProblem } from '@/components/workstation/tidyRule';

/** Versioned per key, like every other persisted preference here: the shape
 *  of a remembered rule can change (a template placeholder is added, a
 *  level stops being legal) and an unversioned key would then hand the new
 *  build a value the old one wrote. */
const PARSE_PATTERN_KEY = 'workstation.parsePattern.v1';
const RENAME_TEMPLATE_KEY = 'workstation.renameTemplate.v1';
const TIDY_ROOT_KEY = 'workstation.tidyRoot.v1';
const TIDY_SEGMENTS_KEY = 'workstation.tidySegments.v1';

/** Ceiling on any one stored rule. High enough that no rule anyone would
 *  hand-write reaches it — the longest plausible 解析 pattern is a few
 *  hundred characters — and low enough that a pasted paragraph is a
 *  bounded write rather than one repeated per keystroke. */
export const MAX_STORED_RULE = 1000;

/** The 整理目录 structure a first visit gets: artist, then album. Also the
 *  fallback for a stored list that cannot be trusted, which is why it is
 *  exported as data rather than spelled out at the one call site — the
 *  dialog's initial state and this fallback have to be the same answer. */
export const DEFAULT_TIDY_SEGMENTS: readonly string[] = ['${artist}', '${album}'];

const cap = (v: string): string => v.slice(0, MAX_STORED_RULE);

/** A stored 解析文件名 pattern, or '' for "use the default rule".
 *
 *  `''` is the right fallback and not a compromise: it is what the dialog
 *  shows on a first visit, it is a valid rule (the server has a default
 *  that handles 「艺术家 - 标题」), and it never leaves the field in an
 *  error state. */
export function sanitizeParsePattern(raw: string | null): string {
  if (!raw) return '';
  const p = cap(raw.trim());
  if (p === '') return '';
  return patternProblem(p) === null ? p : '';
}

/** A stored 从标签改名 template, or '' for "no rule chosen yet".
 *
 *  Empty is the dialog's own neutral state — the apply button says 先选一条规则
 *  — so a rejected template degrades to the same thing a first visit sees. */
export function sanitizeRenameTemplate(raw: string | null): string {
  if (!raw) return '';
  const t = cap(raw.trim());
  // templateProblem('') is null, so an emptied box is stored as '' rather
  // than rejected: clearing the rule is a legitimate answer, not a bad one.
  return templateProblem(t) === null ? t : '';
}

/** A stored 整理目录 destination, trimmed and capped but not otherwise
 *  touched. See the note above: what counts as inside the library is not
 *  knowable here, and the server already says so in its own words. */
export function sanitizeTidyRoot(raw: string | null): string {
  if (!raw) return '';
  return cap(raw.trim());
}

/** A stored 整理目录 level list, or the default two levels.
 *
 *  Unlike the other two this validates a STRUCTURE, so the checks are
 *  ordered: shape (an array), element type, count against the same
 *  MAX_TIDY_LEVELS the dialog's 加一层 button respects, then the list as a
 *  whole. A list longer than the dialog can build is a value this build
 *  did not write, and a level that fails `levelProblem` takes the whole
 *  list with it — half a directory tree is not a smaller answer, it is a
 *  wrong one.
 *
 *  Levels are capped but NOT trimmed. A level is a directory NAME, and the
 *  name the user typed is the name they meant; silently trimming it on the
 *  way back in would make the restored rule differ from the one on screen
 *  at close time. (`segmentsProblem` trims for its own emptiness check,
 *  so a whitespace-only level is still rejected here.) */
export function sanitizeTidySegments(raw: unknown): string[] {
  const fallback = (): string[] => [...DEFAULT_TIDY_SEGMENTS];
  if (!Array.isArray(raw)) return fallback();
  const levels: string[] = [];
  for (const entry of raw) {
    if (typeof entry !== 'string') return fallback();
    levels.push(cap(entry));
  }
  if (levels.length === 0 || levels.length > MAX_TIDY_LEVELS) return fallback();
  return segmentsProblem(levels) === null ? levels : fallback();
}

// ─── Load / remember ───────────────────────────────────────────────────────
//
// Each rule gets its own key so editing one does not rewrite the others,
// and so a failed write to one cannot take the rest down with it.

// Sanitizing on the way OUT as well as in is not redundant with the load
// path doing the same thing. It costs nothing, it keeps every byte in
// storage a value this build would accept, and it means the stored form
// never depends on which validator was current when it was written. For
// these three rules the two are observationally identical — a value the
// writer rejects is one the reader would reject too — so this is about
// not having to reason about which is which.

export function loadParsePattern(): string {
  return sanitizeParsePattern(readString(PARSE_PATTERN_KEY));
}

export function rememberParsePattern(pattern: string): void {
  writeString(PARSE_PATTERN_KEY, sanitizeParsePattern(pattern));
}

export function loadRenameTemplate(): string {
  return sanitizeRenameTemplate(readString(RENAME_TEMPLATE_KEY));
}

export function rememberRenameTemplate(template: string): void {
  writeString(RENAME_TEMPLATE_KEY, sanitizeRenameTemplate(template));
}

export function loadTidyRoot(): string {
  return sanitizeTidyRoot(readString(TIDY_ROOT_KEY));
}

export function rememberTidyRoot(root: string): void {
  writeString(TIDY_ROOT_KEY, sanitizeTidyRoot(root));
}

export function loadTidySegments(): string[] {
  return sanitizeTidySegments(readJson<unknown>(TIDY_SEGMENTS_KEY));
}

export function rememberTidySegments(segments: string[]): void {
  writeJson(TIDY_SEGMENTS_KEY, sanitizeTidySegments(segments));
}
