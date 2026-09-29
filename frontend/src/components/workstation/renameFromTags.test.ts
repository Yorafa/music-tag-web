import { describe, it, expect } from 'vitest';
import {
  DEFAULT_RULE_TEXT,
  RENAME_FIELDS,
  RENAME_PRESETS,
  buildTemplate,
  canPlan,
  describeTemplate,
  fieldsFromTemplate,
  tallyRename,
  templateProblem,
  type RenameField,
} from './renameFromTags';
import type { RenamePlanRow } from '@/api/client';

function row(p: Partial<RenamePlanRow>): RenamePlanRow {
  return {
    path: '/m/a.mp3',
    old_name: 'a.mp3',
    new_name: 'A.mp3',
    status: 'ok',
    ...p,
  };
}

describe('RENAME_FIELDS', () => {
  it('includes genre and year, the two the server used to lack', () => {
    // The bug this module's field list exists alongside: the permissive
    // renderer wrote the literal text "${year}" into a filename because
    // no such variable existed and nothing complained.
    expect(RENAME_FIELDS).toContain('genre');
    expect(RENAME_FIELDS).toContain('year');
  });

  it('covers all eight tag fields', () => {
    expect([...RENAME_FIELDS].sort()).toEqual(
      [
        'album',
        'albumartist',
        'artist',
        'discnumber',
        'genre',
        'title',
        'tracknumber',
        'year',
      ].sort(),
    );
  });
});

describe('buildTemplate', () => {
  it('renders the two-field case the parse dialog inverts', () => {
    expect(buildTemplate(['artist', 'title'])).toBe('${artist} - ${title}');
  });

  it('keeps the chosen order, not the field-list order', () => {
    expect(buildTemplate(['tracknumber', 'artist', 'title'])).toBe(
      '${tracknumber} - ${artist} - ${title}',
    );
  });

  it('is empty for an empty order, which the dialog reads as "no rename"', () => {
    expect(buildTemplate([])).toBe('');
  });

  it('every preset builds a template the checker accepts', () => {
    for (const p of RENAME_PRESETS) {
      const t = buildTemplate(p.fields);
      expect(templateProblem(t), `${p.id}: ${t}`).toBeNull();
      expect(fieldsFromTemplate(t), p.id).toEqual([...p.fields]);
    }
  });

  it('has no duplicate preset ids', () => {
    const ids = RENAME_PRESETS.map((p) => p.id);
    expect(new Set(ids).size).toBe(ids.length);
  });
});

describe('templateProblem', () => {
  it('accepts an empty template, which means "do nothing"', () => {
    expect(templateProblem('')).toBeNull();
    expect(templateProblem('   ')).toBeNull();
  });

  it('rejects a template with no placeholders', () => {
    // This is the one that would rename every selected file to the same
    // literal name; the batch collision check would catch it N times
    // over instead of once.
    expect(templateProblem('track01')).toContain('没有 ${字段}');
  });

  it('rejects an unterminated placeholder, which used to reach the filename', () => {
    expect(templateProblem('${artist - ${title}')).toContain('配对');
  });

  it('rejects a placeholder with no field name', () => {
    expect(templateProblem('${}')).toContain('忘了写字段名');
  });

  it('names an unknown field and lists the real ones', () => {
    const p = templateProblem('${artist} - ${album_artist}');
    expect(p).toContain('album_artist');
    expect(p).toContain('albumartist');
  });

  it('accepts every field the server allows', () => {
    expect(templateProblem(RENAME_FIELDS.map((f) => `\${${f}}`).join(' - '))).toBeNull();
  });

  it('does not police separators — that is the operator\'s business', () => {
    expect(templateProblem('${tracknumber}. ${title}')).toBeNull();
    expect(templateProblem('${artist}__${title}')).toBeNull();
  });
});

describe('fieldsFromTemplate', () => {
  it('reads the order back so chips light up for a generated template', () => {
    expect(fieldsFromTemplate('${tracknumber} - ${artist} - ${title}')).toEqual([
      'tracknumber',
      'artist',
      'title',
    ]);
  });

  it('is empty for an empty template', () => {
    expect(fieldsFromTemplate('')).toEqual([]);
  });

  it('ignores a field name the server would reject', () => {
    expect(fieldsFromTemplate('${nope}')).toEqual([]);
  });

  it('does not repeat a field listed twice', () => {
    expect(fieldsFromTemplate('${title} - ${title}')).toEqual(['title']);
  });
});

describe('describeTemplate', () => {
  it('says the empty template does nothing, rather than nothing at all', () => {
    expect(describeTemplate('')).toBe(DEFAULT_RULE_TEXT);
  });

  it('spells out a generated template as the order it came from', () => {
    expect(describeTemplate(buildTemplate(['artist', 'album', 'title']))).toBe(
      '按顺序取：艺术家 → 专辑 → 标题',
    );
  });

  it('marks a hand-typed template as custom but still names its fields', () => {
    const s = describeTemplate('${tracknumber}. ${title}');
    expect(s).toContain('自定义');
    expect(s).toContain('音轨');
  });
});

describe('tallyRename', () => {
  it('counts each status separately', () => {
    expect(
      tallyRename([
        row({ status: 'ok' }),
        row({ status: 'ok' }),
        row({ status: 'no_change' }),
        row({ status: 'taken' }),
        row({ status: 'blocked' }),
        row({ status: 'failed' }),
      ]),
    ).toMatchObject({
      total: 6,
      ok: 2,
      noChange: 1,
      taken: 1,
      blocked: 1,
      failed: 1,
    });
  });

  it('counts a row with a gap separately — it DID move', () => {
    // The regression: folding gaps into "blocked" would report fewer
    // renames than actually happen, and the plan's whole job is to be
    // readable before committing.
    const t = tallyRename([
      row({ status: 'ok', missing: ['genre'] }),
      row({ status: 'ok' }),
    ]);
    expect(t.ok).toBe(2);
    expect(t.withGaps).toBe(1);
    expect(t.blocked).toBe(0);
  });

  it('treats an empty missing array as no gap', () => {
    expect(tallyRename([row({ status: 'ok', missing: [] })]).withGaps).toBe(0);
  });

  it('handles an unknown status as failed rather than dropping it', () => {
    const t = tallyRename([{ ...row({}), status: 'wat' as never }]);
    expect(t.failed).toBe(1);
    expect(t.total).toBe(1);
  });

  it('is all zeroes for an empty plan', () => {
    expect(tallyRename([])).toEqual({
      total: 0,
      ok: 0,
      noChange: 0,
      taken: 0,
      blocked: 0,
      failed: 0,
      withGaps: 0,
    });
  });
});

describe('RENAME_PRESETS', () => {
  it('every preset names at least two fields', () => {
    for (const p of RENAME_PRESETS) {
      expect(p.fields.length, p.id).toBeGreaterThanOrEqual(2);
    }
  });

  it('every preset field is one the server allows', () => {
    for (const p of RENAME_PRESETS) {
      for (const f of p.fields) {
        expect(RENAME_FIELDS, `${p.id}: ${f}`).toContain(f as RenameField);
      }
    }
  });

  it('the first preset is the exact inverse of the parse dialog default', () => {
    // 解析文件名 splits on the first separator into artist + title, so
    // this preset must round-trip it or the two dialogs contradict.
    expect(RENAME_PRESETS[0].fields).toEqual(['artist', 'title']);
  });
});

describe('canPlan', () => {
  // An empty template is NOT a default rule here the way it is in
  // 解析文件名 — there empty means "split on separators", a real rule the
  // server accepts; here it means "no rule", and `template` carries
  // `binding:"required"`, so the server answers 400 with a raw Go
  // validation dump. The dialog must not offer the button in that state.

  it('refuses an empty template', () => {
    expect(canPlan('', null, 5)).toBe(false);
  });

  it('refuses a whitespace-only template', () => {
    expect(canPlan('   ', null, 5)).toBe(false);
  });

  it('refuses when nothing is selected', () => {
    expect(canPlan('${artist} - ${title}', null, 0)).toBe(false);
  });

  it('refuses a template the checker already rejected', () => {
    expect(canPlan('${nope}', '未知字段 nope', 5)).toBe(false);
  });

  it('allows a real template over a real selection', () => {
    expect(canPlan('${artist} - ${title}', null, 5)).toBe(true);
  });

  it('agrees with templateProblem on every preset', () => {
    for (const p of RENAME_PRESETS) {
      const t = buildTemplate(p.fields);
      expect(canPlan(t, templateProblem(t), 3), p.id).toBe(true);
    }
  });
});
