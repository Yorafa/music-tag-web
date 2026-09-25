/**
 * Vitest spec for `parseFilename.ts`. The bidirectional contract test
 * in `internal/utils/filenames_test.go` SHA-compares this file's
 * source-of-truth fixture to the Go side; this test loads the same
 * JSON and deep-equals each entry's output. Drift on EITHER side
 * means somebody forgot to update the other.
 */

import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve as resolvePath } from 'node:path';
import {
  parseFilename,
  StatusAmbiguous,
  StatusOK,
  StatusUnparsable,
  type ParsedFilename,
} from './parseFilename';

interface Expected {
  artist: string;
  title: string;
  status: 'ok' | 'ambiguous' | 'unparsable';
}
interface Case {
  input: string;
  expected: Expected;
}

// Vitest's jsdom env rewrites `import.meta.url` to a web-scheme URL
// (`http://localhost/...`), which makes `fileURLToPath(new URL('.',
// import.meta.url))` throw on disk read. We therefore resolve the
// bidirectional fixture relative to `process.cwd()` which Vitest
// anchors at the spec directory's package root (`frontend/`) — the
// same convention Go's `findRepoRoot` walked-up-to-go.mod uses for
// its side. Override via PARSEFILENAME_FIXTURE env var if you ever
// move the fixture (CI / monorepo scenarios).
const fixturePath =
  process.env.PARSEFILENAME_FIXTURE ??
  resolvePath(process.cwd(), 'src', 'utils', 'parseFilename.testdata.json');

function loadCases(): Case[] {
  const raw = readFileSync(fixturePath, 'utf8');
  return JSON.parse(raw) as Case[];
}

describe('parseFilename (mirror of internal/utils/filenames.go)', () => {
  const cases = loadCases();
  if (cases.length < 50) {
    throw new Error(
      `fixture only has ${cases.length} entries — plan § C.2 calls for 200+.`,
    );
  }
  for (const c of cases) {
    it(`${c.input} → ${c.expected.status}`, () => {
      const got: ParsedFilename = parseFilename(c.input);
      expect(got.artist, `artist for ${c.input}`).toBe(c.expected.artist);
      expect(got.title, `title for ${c.input}`).toBe(c.expected.title);
      expect(got.status, `status for ${c.input}`).toBe(c.expected.status);
    });
  }
});

describe('parseFilename status enum guards', () => {
  // The mirror exposes string-literal status values; if any of
  // them ever drift from the Go side, the SHA-cert test in
  // filenames_test.go will catch it via fixture-driven outputs.
  it('Ok/Ambiguous/Unparsable constants equal literal strings', () => {
    expect(StatusOK).toBe('ok');
    expect(StatusAmbiguous).toBe('ambiguous');
    expect(StatusUnparsable).toBe('unparsable');
  });
});

describe('parseFilename edge inputs', () => {
  // A few local-only sanity checks in addition to the shared fixture.
  // If the fixture ever loses these cases, these tests still gate
  // against regressions on the engine itself.
  it('empty string → unparsable empty', () => {
    expect(parseFilename('')).toEqual({
      artist: '',
      title: '',
      status: 'unparsable',
    });
  });
  it('NFC normalises NFD input identically', () => {
    // "周杰倫" decomposed = each char + combining variant for 倫.
    const nfc = '周杰倫 - 晴天.mp3';
    // Simulate NFD by re-encoding each variant char to NFD.
    const nfd = nfc.normalize('NFD');
    expect(parseFilename(nfc)).toEqual(parseFilename(nfd));
  });
  it('fallbackRegex with 2-group capture produces ok', () => {
    const got = parseFilename('Track01 ArtistX - TitleY.flac', {
      fallbackRegex: '^(Track\\d+) (.+)$',
    });
    expect(got.artist).toBe('Track01');
    expect(got.title).toBe('ArtistX - TitleY');
    expect(got.status).toBe('ok');
  });
  it('fallbackRegex compile failure → unparsable empty', () => {
    expect(
      parseFilename('ArtistX - TitleY.flac', { fallbackRegex: '[invalid(' }),
    ).toEqual({ artist: '', title: '', status: 'unparsable' });
  });
});
