// The remembered-rule sanitizers. These are pure, so they are the cheap
// half of the coverage — but see batchRuleMemory.test.tsx for the half
// that matters: whether the dialogs actually call them.

import { describe, it, expect, beforeEach } from 'vitest';
import {
  DEFAULT_TIDY_SEGMENTS,
  MAX_STORED_RULE,
  loadParsePattern,
  loadRenameTemplate,
  loadTidyRoot,
  loadTidySegments,
  rememberParsePattern,
  rememberRenameTemplate,
  rememberTidyRoot,
  rememberTidySegments,
  sanitizeParsePattern,
  sanitizeRenameTemplate,
  sanitizeTidyRoot,
  sanitizeTidySegments,
} from './batchRuleCache';

beforeEach(() => {
  window.localStorage.clear();
});

describe('sanitizeParsePattern', () => {
  it('keeps a pattern the parser accepts', () => {
    const p = '^(?P<artist>.+?) - (?P<title>.+)$';
    expect(sanitizeParsePattern(p)).toBe(p);
  });

  it('falls back to the default rule for one that will not compile', () => {
    // The point of the fallback: '' is a VALID state (the server has a
    // default), so a rejected pattern reopens the dialog ready to use
    // rather than ready to complain.
    expect(sanitizeParsePattern('^(?P<artist>.+?')).toBe('');
  });

  it('rejects a pattern naming a field this build does not have', () => {
    // A rule stored by a version that offered a placeholder we dropped
    // would otherwise reach the server and 400 on every batch. (`genre`
    // is NOT such a field — it is in PARSE_TAG_FIELDS, which is worth
    // knowing before picking an example.)
    expect(sanitizeParsePattern('^(?P<retired_field>.+?)$')).toBe('');
  });

  it('keeps a pattern with no named groups, because the dialog would', () => {
    // `${album}` is a valid regex and names no field, so it passes the
    // dialog's own patternProblem — and it is a pattern that extracts
    // nothing. It is kept anyway, on purpose: this module asks the SAME
    // question the dialog asks (see the header), and a second, stricter
    // opinion here is how a remembered rule ends up disagreeing with what
    // the field will accept. Whether it extracts anything is the server's
    // call, and it says so.
    expect(sanitizeParsePattern('${album}')).toBe('${album}');
  });

  it('treats nothing stored as the default rule', () => {
    expect(sanitizeParsePattern(null)).toBe('');
    expect(sanitizeParsePattern('')).toBe('');
    expect(sanitizeParsePattern('   ')).toBe('');
  });

  it('caps a pasted paragraph', () => {
    // Written on every keystroke, so an uncapped value is a value that
    // gets written to disk again on every character after it.
    const huge = `^(?P<artist>${'x'.repeat(MAX_STORED_RULE * 2)})$`;
    expect(sanitizeParsePattern(huge).length).toBeLessThanOrEqual(MAX_STORED_RULE);
  });
});

describe('sanitizeRenameTemplate', () => {
  it('keeps a template whose placeholders are all real', () => {
    expect(sanitizeRenameTemplate('${artist} - ${title}')).toBe('${artist} - ${title}');
  });

  it('keeps a template with fixed text around the fields', () => {
    // Permissive on purpose — the validator only stops the two ways a
    // template is unusable, it does not legislate naming style.
    expect(sanitizeRenameTemplate('${discnumber}-${tracknumber} ${title}')).toBe(
      '${discnumber}-${tracknumber} ${title}',
    );
  });

  it('rejects one with no placeholder at all', () => {
    // Restoring this would offer to rename every file to the same name.
    expect(sanitizeRenameTemplate('track')).toBe('');
  });

  it('rejects an unterminated placeholder', () => {
    expect(sanitizeRenameTemplate('${artist - ${title}')).toBe('');
  });

  it('rejects an unknown field', () => {
    expect(sanitizeRenameTemplate('${artist} - ${lyric}')).toBe('');
  });

  it('treats a cleared box as a cleared rule, not a broken one', () => {
    // Clearing is a legitimate answer, and the dialog has a first-class
    // state for it (the apply button says 先选一条规则).
    expect(sanitizeRenameTemplate('')).toBe('');
    expect(sanitizeRenameTemplate(null)).toBe('');
  });
});

describe('sanitizeTidyRoot', () => {
  it('keeps a path and trims the spaces around it', () => {
    expect(sanitizeTidyRoot('  /music/Blues  ')).toBe('/music/Blues');
  });

  it('treats nothing as the library root, which is the default', () => {
    expect(sanitizeTidyRoot(null)).toBe('');
    expect(sanitizeTidyRoot('  ')).toBe('');
  });

  it('does not invent a rule about where the library is', () => {
    // Whether the root is inside the library needs the server, which
    // already names the fault in its own 400. Rejecting paths here would
    // be a second, worse opinion.
    expect(sanitizeTidyRoot('/definitely/not/the/library')).toBe('/definitely/not/the/library');
  });

  it('caps a pasted paragraph', () => {
    expect(sanitizeTidyRoot('/' + 'x'.repeat(MAX_STORED_RULE * 2)).length).toBe(
      MAX_STORED_RULE,
    );
  });
});

describe('sanitizeTidySegments', () => {
  it('keeps a level list this build can build', () => {
    expect(sanitizeTidySegments(['${genre}', '${artist}', '${year} - ${album}'])).toEqual([
      '${genre}',
      '${artist}',
      '${year} - ${album}',
    ]);
  });

  it('keeps fixed-text levels, which are the case the feature exists for', () => {
    expect(sanitizeTidySegments(['合辑', '${artist}'])).toEqual(['合辑', '${artist}']);
  });

  it('falls back to the default two levels for anything that is not a list', () => {
    for (const junk of [null, undefined, '${artist}', 42, {}]) {
      expect(sanitizeTidySegments(junk)).toEqual([...DEFAULT_TIDY_SEGMENTS]);
    }
  });

  it('falls back for an empty list', () => {
    // Zero levels is not a shallower tree, it is no tree — and the dialog
    // would show 「至少需要一层目录」 with its only button disabled.
    expect(sanitizeTidySegments([])).toEqual([...DEFAULT_TIDY_SEGMENTS]);
  });

  it('falls back for a list longer than the dialog can build', () => {
    // MAX_TIDY_LEVELS is the 加一层 button's own ceiling, so a longer list
    // is a value this build did not write and cannot represent.
    const tooMany = Array.from({ length: 7 }, () => '${artist}');
    expect(sanitizeTidySegments(tooMany)).toEqual([...DEFAULT_TIDY_SEGMENTS]);
  });

  it('takes the whole list down when one level is unusable', () => {
    // The list IS the directory tree. Half a tree is not a smaller answer,
    // it is a wrong one, and the user would find out by looking at the plan.
    expect(sanitizeTidySegments(['${artist}', '${nope}'])).toEqual([...DEFAULT_TIDY_SEGMENTS]);
    expect(sanitizeTidySegments(['${artist}', ''])).toEqual([...DEFAULT_TIDY_SEGMENTS]);
  });

  it('rejects a list holding something that is not a string', () => {
    expect(sanitizeTidySegments(['${artist}', 7])).toEqual([...DEFAULT_TIDY_SEGMENTS]);
    expect(sanitizeTidySegments(['${artist}', null])).toEqual([...DEFAULT_TIDY_SEGMENTS]);
  });

  it('does not trim a level, because a level is a name the user typed', () => {
    // Trimming would restore a rule that differs from the one on screen at
    // close time. A whitespace-ONLY level is still rejected, by
    // segmentsProblem, which trims for its own emptiness check.
    expect(sanitizeTidySegments(['${artist} ', '${album}'])).toEqual(['${artist} ', '${album}']);
    expect(sanitizeTidySegments(['${artist}', '   '])).toEqual([...DEFAULT_TIDY_SEGMENTS]);
  });
});

describe('load / remember round-trips', () => {
  it('remembers a parse pattern', () => {
    const p = '^(?P<artist>.+?) - (?P<title>.+)$';
    rememberParsePattern(p);
    expect(loadParsePattern()).toBe(p);
  });

  it('remembers a rename template', () => {
    rememberRenameTemplate('${artist} - ${title}');
    expect(loadRenameTemplate()).toBe('${artist} - ${title}');
  });

  it('remembers the tidy root and its levels independently', () => {
    // Editing the levels must not disturb the destination, and the other
    // way round — which is why these are two keys.
    rememberTidyRoot('/music/Blues');
    rememberTidySegments(['${artist}', '${year} - ${album}']);
    expect(loadTidyRoot()).toBe('/music/Blues');
    expect(loadTidySegments()).toEqual(['${artist}', '${year} - ${album}']);

    rememberTidyRoot('/music/Jazz');
    expect(loadTidySegments()).toEqual(['${artist}', '${year} - ${album}']);
  });

  it('never stores a value the loader would reject', () => {
    // Belt and braces: the loader sanitizes anyway, but a key holding only
    // values this build accepts is one thing to reason about rather than two.
    rememberParsePattern('^(?P<artist>.+?');
    rememberRenameTemplate('${artist');
    rememberTidySegments(['${artist}', '']);
    expect(loadParsePattern()).toBe('');
    expect(loadRenameTemplate()).toBe('');
    expect(loadTidySegments()).toEqual([...DEFAULT_TIDY_SEGMENTS]);
  });

  it('starts from the defaults when storage is empty', () => {
    expect(loadParsePattern()).toBe('');
    expect(loadRenameTemplate()).toBe('');
    expect(loadTidyRoot()).toBe('');
    expect(loadTidySegments()).toEqual([...DEFAULT_TIDY_SEGMENTS]);
  });

  it('survives a hand-edited key', () => {
    // Every one of these keys is readable and writable by the user, and a
    // past version of the app can leave anything behind.
    window.localStorage.setItem('workstation.tidySegments.v1', '{"not":"an array"}');
    expect(loadTidySegments()).toEqual([...DEFAULT_TIDY_SEGMENTS]);

    window.localStorage.setItem('workstation.renameTemplate.v1', 'not json at all');
    expect(loadRenameTemplate()).toBe('');

    window.localStorage.setItem('workstation.parsePattern.v1', '^(?P<retired_field>.+)$');
    expect(loadParsePattern()).toBe('');
  });
});
