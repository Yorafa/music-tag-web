import { describe, expect, it } from 'vitest';
import {
  MAX_TIDY_LEVELS,
  addLevel,
  canTidy,
  describeLevel,
  fieldsFromLevel,
  levelProblem,
  moveLevel,
  removeLevel,
  segmentsProblem,
  setLevel,
  TIDY_PRESETS,
  toggleField,
} from './tidyRule';

describe('level list surgery', () => {
  it('appends an empty level', () => {
    expect(addLevel(['${artist}'])).toEqual(['${artist}', '']);
  });

  // The cap is a promise to the operator that "add" means a directory
  // they have to picture. Past six levels, adding more means the rule was
  // meant to be a filename template.
  it('refuses to grow past the cap', () => {
    let segs: string[] = [];
    for (let i = 0; i < MAX_TIDY_LEVELS + 4; i++) segs = addLevel(segs);
    expect(segs).toHaveLength(MAX_TIDY_LEVELS);
  });

  it('removes a level by index', () => {
    expect(removeLevel(['a', 'b', 'c'], 1)).toEqual(['a', 'c']);
  });

  it('ignores an out-of-range remove rather than shifting the list', () => {
    expect(removeLevel(['a', 'b'], 5)).toEqual(['a', 'b']);
  });

  it('moves a level up and down', () => {
    expect(moveLevel(['a', 'b', 'c'], 2, -1)).toEqual(['a', 'c', 'b']);
    expect(moveLevel(['a', 'b', 'c'], 0, 1)).toEqual(['b', 'a', 'c']);
  });

  // A reorder button that silently moves the wrong row is worse than one
  // that does nothing: the operator is looking at a list they just built
  // and a row that jumped is a row they have to re-read from scratch.
  it('treats an out-of-range move as a no-op, not a clamp', () => {
    expect(moveLevel(['a', 'b'], 0, -1)).toEqual(['a', 'b']);
    expect(moveLevel(['a', 'b'], 1, 1)).toEqual(['a', 'b']);
  });

  it('replaces a level in place', () => {
    expect(setLevel(['a', 'b'], 1, 'z')).toEqual(['a', 'z']);
  });

  // The add case: a level is a template that may be half fixed text, so
  // clicking 专辑 on "${year} - " must not throw the separator away.
  it('appends a field rather than replacing the level', () => {
    expect(toggleField(['${year} - '], 0, 'album')).toEqual(['${year} - ${album}']);
  });

  // The remove case, and the bug it fixes: the chip renders as PRESSED
  // once the field is in the level, and appending on a second click gave
  // ${artist}${artist} — then a third time, and a fourth.
  it('removes a field that is already there, so a second click undoes the first', () => {
    expect(toggleField(['${artist}'], 0, 'artist')).toEqual(['']);
  });

  it('takes out ONE occurrence, so a level that really named it twice keeps the other', () => {
    expect(toggleField(['${artist} - ${artist}'], 0, 'artist')).toEqual([' - ${artist}']);
  });

  it('is a no-op for an out-of-range level', () => {
    expect(toggleField(['${artist}'], 3, 'album')).toEqual(['${artist}']);
  });
});

describe('levelProblem', () => {
  it('accepts a plain field', () => {
    expect(levelProblem('${artist}')).toBeNull();
  });

  // The one rule deliberately NOT inherited from the rename dialogs. A
  // filename template with no placeholder renames every file to the same
  // literal name; a directory level has no such failure, and 「Live」 is
  // a directory people actually keep music in.
  it('accepts a level with no placeholder at all', () => {
    expect(levelProblem('合辑')).toBeNull();
  });

  it('accepts a field mixed with fixed text', () => {
    expect(levelProblem('${year} - ${album}')).toBeNull();
  });

  it('rejects an empty level', () => {
    expect(levelProblem('   ')).toBeTruthy();
  });

  it('rejects an unterminated placeholder', () => {
    expect(levelProblem('${artist')).toBeTruthy();
  });

  it('rejects an empty placeholder', () => {
    expect(levelProblem('${}')).toBeTruthy();
  });

  // The typo that a permissive renderer would turn into a directory
  // literally named "${albmu}".
  it('names the offending field when it is misspelled', () => {
    expect(levelProblem('${albmu}')).toContain('albmu');
  });
});

describe('segmentsProblem', () => {
  it('needs at least one level', () => {
    expect(segmentsProblem([])).toBeTruthy();
  });

  it('reports the first bad level by its position in the list', () => {
    expect(segmentsProblem(['${artist}', '', '${album}'])).toContain('第 2 层');
  });

  it('accepts the classic shape', () => {
    expect(segmentsProblem(['${artist}', '${album}'])).toBeNull();
  });

  it('accepts arbitrary depth', () => {
    expect(segmentsProblem(['a', 'b', 'c', 'd', 'e', 'f'])).toBeNull();
  });
});

describe('canTidy', () => {
  it('needs a selection and a usable rule', () => {
    expect(canTidy(['${artist}'], 3)).toBe(true);
    expect(canTidy(['${artist}'], 0)).toBe(false);
    expect(canTidy(['${nope}'], 3)).toBe(false);
  });
});

describe('level description and chips', () => {
  it('reads the field order back out, for lighting up chips', () => {
    expect(fieldsFromLevel('${year} - ${album}')).toEqual(['year', 'album']);
  });

  it('drops a repeated field, so a chip is lit once', () => {
    expect(fieldsFromLevel('${artist} - ${artist}')).toEqual(['artist']);
  });

  it('ignores a chip row for a level with no placeholders', () => {
    expect(fieldsFromLevel('合辑')).toEqual([]);
  });

  it('describes a pure-field level without hedging', () => {
    expect(describeLevel('${artist}')).toBe('艺术家');
  });

  // The operator should know their separator is part of the directory
  // name, because " - " survives when a field is empty.
  it('calls out a level that mixes fields with fixed text', () => {
    expect(describeLevel('${year} - ${album}')).toContain('含固定文字');
  });

  it('describes a literal level as such', () => {
    expect(describeLevel('合辑')).toContain('固定目录名');
  });
});

describe('presets', () => {
  // Every preset must be a legal rule. A preset that trips its own
  // validator is a dialog that opens already broken.
  it('are all usable as-is', () => {
    for (const p of TIDY_PRESETS) {
      expect(segmentsProblem(p.segments), `${p.id} is not a valid rule`).toBeNull();
    }
  });

  it('cover depths the old two-field form could not offer', () => {
    const depths = TIDY_PRESETS.map((p) => p.segments.length);
    expect(Math.max(...depths)).toBeGreaterThan(2);
    expect(Math.min(...depths)).toBe(1);
  });
});
