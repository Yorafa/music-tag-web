import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  DEFAULT_RULE_TEXT,
  PATTERN_PRESETS,
  buildPattern,
  describeRule,
  fieldsFromPattern,
  findUnsupportedPerlForTest,
  firstParsedExample,
  patternHelp,
  presetById,
  tryPattern,
  type ParseTagField,
} from './parseAssist';

// The generator's whole reason to exist is that it produces patterns the
// SERVER can run. If buildPattern emitted something V8 accepts and Go's
// RE2 rejects, or emitted a pattern that parses differently from the
// order the chips promised, the dialog would be confidently wrong. So the
// generated patterns are run against the same fixture the Go contract
// test uses, and any preset whose example does not come out as advertised
// fails here.
interface FixtureCase {
  input: string;
  pattern?: string;
  expected: Record<string, string> & { status: string };
}
const FIXTURE: FixtureCase[] = JSON.parse(
  readFileSync(
    resolve(__dirname, '../../utils/parseFilename.testdata.json'),
    'utf8',
  ),
);

describe('buildPattern', () => {
  it('makes the two-field default read as artist then title', () => {
    expect(buildPattern(['artist', 'title'])).toBe(
      '^(?P<artist>.+?) - (?P<title>.+)$',
    );
  });

  it('joins every field with " - " so the box text matches the separators people see', () => {
    expect(buildPattern(['artist', 'album', 'title'])).toBe(
      '^(?P<artist>.+?) - (?P<album>.+?) - (?P<title>.+)$',
    );
  });

  it('constrains year to four digits and track/disc to digits', () => {
    // Without this a "year" group happily captures "Deluxe" and the tag
    // write turns it into year 0.
    const p = buildPattern(['artist', 'year', 'tracknumber', 'title']);
    expect(p).toContain('(?P<year>\\d{4})');
    expect(p).toContain('(?P<tracknumber>\\d+)');
  });

  it('makes the LAST field greedy so the title is not truncated', () => {
    // `.+?` in the final position stops at the first separator, turning
    // "A - B - C" into title "B" and losing the rest.
    const p = buildPattern(['artist', 'album', 'title']);
    expect(p.endsWith('(?P<title>.+)$')).toBe(true);
  });

  it('makes every non-final field lazy so the first one does not eat the name', () => {
    const p = buildPattern(['artist', 'album', 'tracknumber', 'title']);
    expect(p).toContain('(?P<artist>.+?)');
    expect(p).toContain('(?P<album>.+?)');
  });

  it('is empty for an empty order, which the dialog reads as "use the default"', () => {
    expect(buildPattern([])).toBe('');
  });

  it('every pattern it can produce passes the shared client-side check', () => {
    const pool: ParseTagField[] = ['artist', 'album', 'title', 'tracknumber'];
    for (let i = 1; i <= 4; i++) {
      for (const c of combosOf(pool, i)) {
        const p = buildPattern(c);
        expect(patternHelp(p).problem, `pattern ${p}`).toBeNull();
      }
    }
  });
});

/** Every ordered subset of `pool` of size `n`. Small enough to enumerate. */
function combosOf<T>(pool: T[], n: number): T[][] {
  if (n === 0) return [[]];
  const out: T[][] = [];
  for (let i = 0; i <= pool.length - n; i++) {
    for (const rest of combosOf(pool.slice(i + 1), n - 1)) {
      out.push([pool[i], ...rest]);
    }
  }
  return out;
}

describe('generated patterns agree with the Go parser on the shared fixture', () => {
  // Each preset claims an example filename and a field order. Run the
  // preset's OWN generated pattern against the fixture and require the
  // advertised result — this is the check that makes the chips
  // trustworthy rather than decorative.
  for (const preset of PATTERN_PRESETS) {
    it(`preset「${preset.label}」parses its own example as advertised`, () => {
      const pattern = buildPattern(preset.fields);
      const got = tryPattern(preset.example, pattern);
      expect(got.status, `${preset.id}: ${preset.example}`).toBe('ok');

      const byField = new Map(got.results.map((r) => [r.field, r.value]));
      // Split the example on " - " and expect field i to be segment i.
      const segments = preset.example.replace(/\.[^.]+$/, '').split(' - ');
      expect(segments).toHaveLength(preset.fields.length);
      preset.fields.forEach((f, i) => {
        expect(byField.get(f), `${preset.id} field ${f}`).toBe(segments[i]);
      });
    });
  }

  it('reproduces a real fixture case end to end', () => {
    // A fixture entry that carries a pattern, so this exercises the same
    // shape the generator emits rather than a hand-written one.
    const c = FIXTURE.find(
      (x) => x.pattern && (x.pattern.includes('(?P<') || x.pattern.includes('(?P<')) && x.expected.status === 'ok',
    );
    expect(c, 'fixture has at least one named-group ok case').toBeDefined();
    const got = tryPattern(c!.input, c!.pattern!);
    expect(got.status).toBe('ok');
    for (const [field, value] of Object.entries(c!.expected)) {
      if (field === 'status') continue;
      if (value === '') continue;
      const hit = got.results.find((r) => r.field === field);
      expect(hit?.value, `${field} of ${c!.input}`).toBe(value);
    }
  });
});

describe('fieldsFromPattern', () => {
  it('reads the order back out, so chips light up for a generated pattern', () => {
    expect(fieldsFromPattern('^(?P<artist>.+?) - (?P<title>.+)$')).toEqual([
      'artist',
      'title',
    ]);
  });

  it('is empty for the default (no pattern)', () => {
    expect(fieldsFromPattern('')).toEqual([]);
  });

  it('ignores a group name that is not a real field', () => {
    expect(fieldsFromPattern('^(?P<bob>.+)$')).toEqual([]);
  });

  it('does not repeat a field listed twice', () => {
    expect(fieldsFromPattern('^(?P<title>.+?)(?P<title>.+)$')).toEqual(['title']);
  });

  it('round-trips with buildPattern', () => {
    for (const f of [
      ['artist', 'title'],
      ['artist', 'album', 'title'],
      ['tracknumber', 'artist', 'year', 'title'],
    ] as ParseTagField[][]) {
      expect(fieldsFromPattern(buildPattern(f))).toEqual(f);
    }
  });
});

describe('describeRule', () => {
  it('states the default rule in full rather than saying nothing', () => {
    // The whole point: an empty box must not leave the user guessing what
    // is about to happen to their filenames.
    expect(describeRule('')).toBe(DEFAULT_RULE_TEXT);
    expect(describeRule('   ')).toBe(DEFAULT_RULE_TEXT);
  });

  it('spells out a generated pattern as the field order it came from', () => {
    // The order is the one the chips were clicked in, which is the order
    // the groups appear in — not PARSE_TAG_FIELDS order.
    expect(describeRule(buildPattern(['artist', 'album', 'title']))).toBe(
      '按顺序取：艺术家 → 专辑 → 标题',
    );
  });

  it('says "custom" for a hand-typed pattern it cannot claim to have generated', () => {
    const s = describeRule('^(?P<title>[^/]+)$');
    expect(s).toContain('自定义');
  });

  it('names the fields it does recognise even in a hand-typed pattern', () => {
    expect(describeRule('artist=(?P<artist>.+?) title=(?P<title>.+)')).toContain(
      '艺术家',
    );
  });
});

describe('tryPattern', () => {
  it('runs the DEFAULT split when the pattern is empty, matching the server', () => {
    const got = tryPattern('周杰倫 - 晴天.flac', '');
    expect(got.status).toBe('ok');
    expect(got.results).toEqual([
      { field: 'artist', value: '周杰倫' },
      { field: 'title', value: '晴天' },
    ]);
  });

  it('flags 3+ segments as ambiguous, and rejoins the tail like the server does', () => {
    const got = tryPattern('A - B - C.flac', '');
    expect(got.status).toBe('ambiguous');
    expect(got.results).toEqual([
      { field: 'artist', value: 'A' },
      { field: 'title', value: 'B - C' },
    ]);
  });

  it('accepts every separator the server splits on', () => {
    for (const sep of ['-', '_', '/', '\\', '|', '·']) {
      const got = tryPattern(`Artist${sep}Title.mp3`, '');
      expect(got.status, `sep ${sep}`).toBe('ok');
      expect(got.results.map((r) => r.value)).toEqual(['Artist', 'Title']);
    }
  });

  it('strips only the LAST extension, so a dot inside the name survives', () => {
    // Checked against the Go side, not assumed: for "Mr. A - B - C.mp3"
    // the fixture records artist "Mr. A", title "B - C". Cutting at the
    // FIRST dot would give stem "Mr" and lose the rest.
    const got = tryPattern('Mr. A - B - C.mp3', '');
    expect(got.status).toBe('ambiguous');
    expect(got.results).toEqual([
      { field: 'artist', value: 'Mr. A' },
      { field: 'title', value: 'B - C' },
    ]);
  });

  it('is unparsable for a name with no extension, like the server', () => {
    // "A - B" and "Mr. A - B" both have a dot in the wrong place (or no
    // extension at all), so the server's stripExt yields "A"/"Mr" — a
    // single segment, hence unparsable. Matching that here matters: a
    // try-it box that read those correctly while the server did not would
    // be showing the user a preview that will not happen.
    expect(tryPattern('A - B', '').status).toBe('unparsable');
    expect(tryPattern('Mr. A - B', '').status).toBe('unparsable');
  });

  it('treats a dotfile as unparsable, like the server', () => {
    expect(tryPattern('.hidden', '').status).toBe('unparsable');
  });

  it('omits a group that did not participate, rather than reporting it as empty', () => {
    const got = tryPattern('Artist - Title.flac', buildPattern(['artist', 'album', 'title']));
    expect(got.status).toBe('unparsable');
  });

  it('reports a pattern that matches nothing as unparsable', () => {
    expect(tryPattern('whatever.mp3', buildPattern(['artist', 'album', 'title'])).status).toBe(
      'unparsable',
    );
  });

  it('is unparsable rather than throwing on an invalid pattern', () => {
    expect(tryPattern('a - b.flac', '^(?P<title>.+').status).toBe('unparsable');
  });

  it('does not count a group inside a character class as a capture', () => {
    // `[(?=]` is a literal bracket in both engines. A naive scan would
    // read the "(?=" as a group and shift every later index by one,
    // attributing values to the wrong fields.
    const got = tryPattern('a[(?=]b - c.flac', '^(?P<title>.+?) - (?P<artist>.+)$');
    expect(got.results).toEqual([
      { field: 'title', value: 'a[(?=]b' },
      { field: 'artist', value: 'c' },
    ]);
  });
});

describe('patternHelp / RE2 limits', () => {
  it('accepts the default (empty) pattern', () => {
    expect(patternHelp('').problem).toBeNull();
  });

  it('accepts a generated pattern', () => {
    expect(patternHelp(buildPattern(['artist', 'title'])).problem).toBeNull();
  });

  it('rejects lookahead, which Go RE2 refuses but V8 accepts', () => {
    // Verified against Go's regexp.Compile: "invalid or unsupported
    // Perl syntax: (?=". Without this check the box says the pattern is
    // fine and the server 400s on the next click.
    const h = patternHelp('^(?P<title>.+?)(?= - )');
    expect(h.problem).toContain('前瞻');
    expect(h.problem).toContain('RE2');
  });

  it('rejects negative lookahead and lookbehind too', () => {
    expect(patternHelp('^(?!x)(?P<title>.+)$').problem).toContain('前瞻');
    expect(patternHelp('(?<=a)(?P<title>.+)$').problem).toContain('反向引用');
  });

  it('rejects a backreference', () => {
    expect(patternHelp('^(?P<title>.+)\\1$').problem).toContain('反向引用');
  });

  it('does not flag a literal "(?=" inside a character class', () => {
    expect(findUnsupportedPerlForTest('[(?=]')).toBeNull();
  });

  it('does not flag an escaped backslash followed by a digit', () => {
    // `\\1` is a literal backslash then a one, not a backreference. The
    // scan has to notice the escaping; a whole-string /\\[1-9]/ test
    // cannot and reported this as unsupported.
    expect(findUnsupportedPerlForTest('\\\\1')).toBeNull();
  });

  it('still flags a real backreference after other escapes', () => {
    expect(findUnsupportedPerlForTest('^(?P<title>.+)\\1$')).toContain('反向引用');
  });

  it('reports the unknown field name with the list of real ones', () => {
    const p = patternHelp('^(?P<bob>.+)$').problem;
    expect(p).toContain('bob');
    expect(p).toContain('albumartist');
  });
});

describe('presets', () => {
  it('every preset generates a pattern the client check accepts', () => {
    for (const p of PATTERN_PRESETS) {
      expect(patternHelp(buildPattern(p.fields)).problem, p.id).toBeNull();
    }
  });

  it('every preset example is actually split into its advertised field count', () => {
    for (const p of PATTERN_PRESETS) {
      const segs = p.example.replace(/\.[^.]+$/, '').split(' - ');
      expect(segs.length, `${p.id}: ${p.example}`).toBe(p.fields.length);
    }
  });

  it('presetById finds a preset and returns undefined otherwise', () => {
    expect(presetById('artist-title')?.label).toBe('艺术家 - 标题');
    expect(presetById('nope')).toBeUndefined();
  });

  it('has no duplicate ids', () => {
    const ids = PATTERN_PRESETS.map((p) => p.id);
    expect(new Set(ids).size).toBe(ids.length);
  });
});

describe('firstParsedExample', () => {
  it('pulls a basename out of the first previewed path', () => {
    expect(
      firstParsedExample([{ path: '/music/A/B - C.flac', status: 'ok' }]),
    ).toBe('B - C.flac');
  });

  it('is empty when nothing was previewed', () => {
    expect(firstParsedExample([])).toBe('');
  });
});
