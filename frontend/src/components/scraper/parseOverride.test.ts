import { describe, it, expect } from 'vitest';
import {
  PARSE_TAG_FIELDS,
  activeFields,
  buildOverrides,
  changedCellCount,
  changedRowCount,
  draftFor,
  emptyDraftRow,
  hasUnreadable,
  patternProblem,
  seedDrafts,
  type OverrideDrafts,
} from './parseOverride';
import type { ParsedPreviewRow } from '@/api/client';

function row(path: string, fields: Partial<ParsedPreviewRow> = {}): ParsedPreviewRow {
  return { path, status: 'ok', ...fields };
}

describe('emptyDraftRow', () => {
  it('starts every field blank, so blank keeps meaning "not overridden"', () => {
    const draft = emptyDraftRow();
    expect(Object.keys(draft).sort()).toEqual([...PARSE_TAG_FIELDS].sort());
    for (const f of PARSE_TAG_FIELDS) expect(draft[f]).toBe('');
  });
});

describe('seedDrafts', () => {
  it('gives every previewed path a blank row keyed by that path', () => {
    const drafts = seedDrafts([row('/m/a.flac'), row('/m/b.flac')]);
    expect(Object.keys(drafts).sort()).toEqual(['/m/a.flac', '/m/b.flac']);
    expect(drafts['/m/a.flac'].title).toBe('');
  });

  it('an empty result set seeds nothing', () => {
    expect(seedDrafts([])).toEqual({});
  });
});

describe('draftFor', () => {
  it('hands back the stored row', () => {
    const drafts = seedDrafts([row('/m/a.flac')]);
    drafts['/m/a.flac'].genre = 'Jazz';
    expect(draftFor(drafts, '/m/a.flac').genre).toBe('Jazz');
  });

  it('falls back to a blank row for an unseeded path rather than throwing', () => {
    // A re-preview can land after the seed; the late row must not crash
    // the table.
    expect(draftFor({}, '/m/late.flac').title).toBe('');
  });
});

describe('buildOverrides', () => {
  it('omits a row the person never touched, instead of sending an empty entry', () => {
    const results = [row('/m/a.flac'), row('/m/b.flac')];
    const drafts = seedDrafts(results);
    expect(buildOverrides(results, drafts)).toEqual([]);
  });

  it('carries only the fields that were typed, leaving the rest to the parser', () => {
    const results = [row('/m/a.flac', { artist: 'Parsed A', title: 'Parsed T' })];
    const drafts = seedDrafts(results);
    drafts['/m/a.flac'].album = 'Manual Album';
    expect(buildOverrides(results, drafts)).toEqual([
      { path: '/m/a.flac', album: 'Manual Album' },
    ]);
  });

  it('trims, and drops a field that is only whitespace', () => {
    const results = [row('/m/a.flac')];
    const drafts = seedDrafts(results);
    drafts['/m/a.flac'].title = '  Spaced  ';
    drafts['/m/a.flac'].genre = '   ';
    expect(buildOverrides(results, drafts)).toEqual([
      { path: '/m/a.flac', title: 'Spaced' },
    ]);
  });

  it('sends every new field the parser can fill, not just artist/title', () => {
    const results = [row('/m/a.flac')];
    const drafts = seedDrafts(results);
    const d = drafts['/m/a.flac'];
    d.album = 'A';
    d.albumartist = 'AA';
    d.genre = 'G';
    d.year = '2026';
    d.tracknumber = '3';
    d.discnumber = '1';
    expect(buildOverrides(results, drafts)).toEqual([
      {
        path: '/m/a.flac',
        album: 'A',
        albumartist: 'AA',
        genre: 'G',
        year: '2026',
        tracknumber: '3',
        discnumber: '1',
      },
    ]);
  });

  it('a row whose only cell is whitespace is not an override', () => {
    // A person who taps a cell and types nothing must not send an entry:
    // the server would read an empty entry as "leave the parsed value
    // alone" anyway, but the apply button would then claim it edited a
    // track nobody touched.
    const results = [row('/m/a.flac')];
    const drafts = seedDrafts(results);
    drafts['/m/a.flac'].genre = '   ';
    expect(buildOverrides(results, drafts)).toEqual([]);
    expect(changedRowCount(results, drafts)).toBe(0);
  });

  it('skips a result with no seeded draft rather than inventing one', () => {
    const results = [row('/m/a.flac')];
    expect(buildOverrides(results, {})).toEqual([]);
  });
});

describe('changedRowCount / changedCellCount', () => {
  it('count rows and cells separately — three fields on one track is one row', () => {
    const results = [row('/m/a.flac'), row('/m/b.flac')];
    const drafts: OverrideDrafts = seedDrafts(results);
    drafts['/m/a.flac'].title = 'T';
    drafts['/m/a.flac'].artist = 'A';
    drafts['/m/a.flac'].album = 'Al';
    drafts['/m/b.flac'].title = 'T2';
    expect(changedRowCount(results, drafts)).toBe(2);
    expect(changedCellCount(drafts)).toBe(4);
  });

  it('both are zero for an untouched grid', () => {
    const results = [row('/m/a.flac')];
    const drafts = seedDrafts(results);
    expect(changedRowCount(results, drafts)).toBe(0);
    expect(changedCellCount(drafts)).toBe(0);
  });
});

describe('activeFields', () => {
  it('hides a field nothing was read into, so a default preview is not 8 empty boxes', () => {
    const results = [
      row('/m/a.flac', { artist: 'A', title: 'T' }),
      row('/m/b.flac', { artist: 'B', title: 'T2', album: 'Alb' }),
    ];
    expect(activeFields(results)).toEqual(['title', 'artist', 'album']);
  });

  it('offers title and artist when NOTHING parsed, so "手填" points at a real input', () => {
    // Regression: the filtered list used to come back empty, the table
    // rendered zero input columns, and the hint under it told the user to
    // type the values in by hand — into nothing.
    const results = [
      row('/m/a.flac', { status: 'unparsable' }),
      row('/m/b.flac', { status: 'unparsable' }),
    ];
    expect(activeFields(results)).toEqual(['title', 'artist']);
  });

  it('does not fall back for an empty result set (no rows means no table at all)', () => {
    // The modal renders nothing when there are no results, so returning
    // two columns here would only mislead a caller that counted them.
    expect(activeFields([])).toEqual(['title', 'artist']);
  });
});

describe('hasUnreadable', () => {
  it('is true when any row is not ok', () => {
    expect(hasUnreadable([row('/m/a.flac')])).toBe(false);
    expect(hasUnreadable([row('/m/a.flac'), row('/m/b.flac', { status: 'ambiguous' })])).toBe(true);
  });
});

describe('patternProblem', () => {
  it('accepts an empty pattern, which means "use the default split"', () => {
    expect(patternProblem('')).toBeNull();
    expect(patternProblem('   ')).toBeNull();
  });

  it("accepts Go's (?P<name>...) spelling, which new RegExp alone rejects", () => {
    // Regression: compiling the raw string throws "Invalid group" in
    // V8, so an un-translated check flags every valid pattern as broken
    // and the pattern can never be submitted. The lint rule is right
    // that this is not a valid JS regex — that is the point, and the
    // throw is the assertion.
    // eslint-disable-next-line no-invalid-regexp
    expect(() => new RegExp('^(?P<artist>.+?) - (?P<title>.+)$')).toThrow();
    expect(patternProblem('^(?P<artist>.+?) - (?P<title>.+)$')).toBeNull();
  });

  it('names an unknown group and lists the real ones', () => {
    const p = patternProblem('^(?P<bob>.+)$');
    expect(p).toContain('bob');
    expect(p).toContain('albumartist');
  });

  it('accepts every field the server allows', () => {
    const p = PARSE_TAG_FIELDS.map((f) => `(?P<${f}>.+?)`).join('-');
    expect(patternProblem(`^${p}$`)).toBeNull();
  });

  it('rejects a pattern with no field name', () => {
    // `(?P<>` is not valid in Go either, so this is a compile error, not
    // a naming complaint — asserted so the two failure kinds stay
    // distinguishable.
    expect(patternProblem('^(?P<>.+)$')).toBe('正则表达式无法编译');
  });

  it('rejects an uncompilable pattern', () => {
    expect(patternProblem('^(?P<title>.+')).toBe('正则表达式无法编译');
  });

  it('a mixed pattern with one bad group is rejected wholesale', () => {
    expect(patternProblem('^(?P<title>.+?) - (?P<nope>.+)$')).toContain('nope');
  });
});
