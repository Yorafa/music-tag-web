import { describe, expect, it } from 'vitest';
import {
  MAX_TIDY_LEVELS,
  addLevel,
  appendField,
  canPreview,
  describeLevel,
  describeTally,
  fieldsFromLevel,
  levelProblem,
  moveLevel,
  movablePathsFromPlan,
  removeLevel,
  segmentsProblem,
  setLevel,
  statusDetail,
  statusLabel,
  TIDY_PRESETS,
  tryLevel,
  tryStructure,
  tallyPlan,
} from './tidyRule';
import type { TidyPlanRow } from '@/api/client';

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

  // Appending rather than replacing is the whole point: a level is a
  // template that may be half fixed text, and clicking 专辑 on
  // "${year} - " should not throw the separator away.
  it('appends a field to a level instead of replacing it', () => {
    expect(appendField(['${year} - '], 0, 'album')).toEqual(['${year} - ${album}']);
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

describe('canPreview', () => {
  it('needs a root, a file list, and a usable rule', () => {
    expect(canPreview(['${artist}'], '/app/media', 3)).toBe(true);
    expect(canPreview(['${artist}'], '', 3)).toBe(false);
    expect(canPreview(['${artist}'], '/app/media', 0)).toBe(false);
    expect(canPreview(['${nope}'], '/app/media', 3)).toBe(false);
  });
});

describe('local rendering', () => {
  it('reads the field order back out, for lighting up chips', () => {
    expect(fieldsFromLevel('${year} - ${album}')).toEqual(['year', 'album']);
  });

  it('drops a repeated field, so a chip is lit once', () => {
    expect(fieldsFromLevel('${artist} - ${artist}')).toEqual(['artist']);
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

  it('renders a level against the example', () => {
    expect(tryLevel('${year} - ${album}', { year: '2003', album: '叶惠美' }).name).toBe('2003 - 叶惠美');
  });

  // Honesty requirement: the local render must leave the same gap the
  // server leaves, or the two disagree the moment one is wrong.
  it('leaves the separator behind when a field is empty, and says which', () => {
    const r = tryLevel('${year} - ${album}', { album: '叶惠美' });
    expect(r.name).toBe('- 叶惠美');
    expect(r.missing).toEqual(['year']);
  });

  it('renders the whole structure top to bottom', () => {
    expect(tryStructure(['${artist}', '${year} - ${album}'])).toEqual(['周杰伦', '2003 - 叶惠美']);
  });

  // The 未知 fallback is the server's behaviour; reproducing it keeps the
  // example from showing a level that silently vanishes.
  it('shows 未知 for a level that renders to nothing', () => {
    // Explicitly no year, rather than the default example — which has one,
    // and would render "2003" and prove nothing about the fallback.
    expect(tryStructure(['${year}', '${album}'], { album: '叶惠美' })).toEqual(['未知', '叶惠美']);
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

describe('tallyPlan', () => {
  const row = (p: Partial<TidyPlanRow>): TidyPlanRow => ({
    old_path: '/a',
    new_path: '/b',
    status: 'move',
    ...p,
  });

  it('counts a row with a missing tag as a move AND as a gap', () => {
    const t = tallyPlan([row({}), row({ missing: ['year'] })]);
    expect(t.move).toBe(2);
    expect(t.withGaps).toBe(1);
  });

  it('keeps the refused buckets apart', () => {
    const t = tallyPlan([
      row({ status: 'taken' }),
      row({ status: 'duplicate' }),
      row({ status: 'blocked' }),
      row({ status: 'same' }),
    ]);
    expect(t).toMatchObject({ taken: 1, duplicate: 1, blocked: 1, same: 1, move: 0 });
  });
});

describe('describeTally', () => {
  it('names the refused rows rather than folding them into a total', () => {
    const s = describeTally({ total: 5, move: 2, same: 1, taken: 1, duplicate: 0, blocked: 1, withGaps: 0 });
    expect(s).toContain('2 首将移动');
    expect(s).toContain('目标已有文件');
    expect(s).toContain('无法处理');
  });

  // "412 首已整理" and "412 首已整理，3 首冲突未动" are very different
  // sentences about the same batch, and only the second tells the
  // operator they have work left.
  it('does not let the moved count stand in for the whole batch', () => {
    const s = describeTally({ total: 5, move: 2, same: 0, taken: 3, duplicate: 0, blocked: 0, withGaps: 0 });
    expect(s).not.toBe('5 首将移动');
    expect(s).toContain('3 首不动');
  });

  it('mentions gaps separately from moves', () => {
    const s = describeTally({ total: 3, move: 3, same: 0, taken: 0, duplicate: 0, blocked: 0, withGaps: 1 });
    expect(s).toContain('缺少标签');
  });

  it('says so when there is nothing to do', () => {
    expect(describeTally({ total: 0, move: 0, same: 0, taken: 0, duplicate: 0, blocked: 0, withGaps: 0 }))
      .toContain('没有可整理的文件');
  });
});

describe('row labels', () => {
  it('has a label for every status', () => {
    for (const s of ['move', 'same', 'taken', 'duplicate', 'blocked'] as const) {
      expect(statusLabel(s)).toBeTruthy();
    }
  });

  it('prefers the server reason, and falls back to the missing fields', () => {
    expect(statusDetail({ old_path: '', new_path: '', status: 'taken', reason: '目标位置已有同名文件' }))
      .toBe('目标位置已有同名文件');
    expect(statusDetail({ old_path: '', new_path: '', status: 'move', missing: ['year'] }))
      .toContain('年份');
  });
});

describe('movablePathsFromPlan', () => {
  const row = (p: Partial<TidyPlanRow>): TidyPlanRow => ({
    old_path: '/a',
    new_path: '/b',
    status: 'move',
    ...p,
  });

  // The refused rows were shown to the operator and accepted as "this one
  // does not move". Re-sending them would have the worker fail them at
  // os.Rename and report the same fact a second time, with no reference
  // to the plan that already explained it.
  it('excludes every row the plan refused', () => {
    const got = movablePathsFromPlan([
      row({ old_path: '/keep' }),
      row({ old_path: '/drop', status: 'taken' }),
      row({ old_path: '/drop2', status: 'duplicate' }),
      row({ old_path: '/drop3', status: 'blocked' }),
    ]);
    expect(got).toEqual(['/keep']);
  });

  // `same` is where the file already should be, and the worker's re-plan
  // turns it into a no-op. Dropping it here would silently shrink the
  // batch below what the operator saw in the plan.
  it('keeps the already-in-place rows', () => {
    expect(movablePathsFromPlan([row({ old_path: '/stay', status: 'same' })])).toEqual(['/stay']);
  });

  it('is empty when the plan refused everything', () => {
    expect(movablePathsFromPlan([row({ status: 'taken' })])).toEqual([]);
  });
});
