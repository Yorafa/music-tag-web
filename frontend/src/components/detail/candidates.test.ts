import { describe, it, expect } from 'vitest';
import {
  CANDIDATES_PER_PAGE,
  bestCandidate,
  clampPage,
  mergeCandidates,
  normalizePageSize,
  paginateCandidates,
  withSource,
} from './candidates';
import type { SongInfo } from '@/types';

function song(partial: Partial<SongInfo> & { name: string }): SongInfo {
  return {
    id: partial.id ?? partial.name,
    artist: '',
    artist_id: '',
    album: '',
    album_id: '',
    album_img: '',
    year: '',
    ...partial,
  } as SongInfo;
}

describe('mergeCandidates', () => {
  it('interleaves by rank instead of concatenating by source', () => {
    // The bug this guards: netease answers with 3 rows and qqmusic with 1,
    // and a plain concat shows all three netease rows before qqmusic's —
    // so a source the user explicitly ticked is invisible below the fold.
    const merged = mergeCandidates([
      [song({ name: 'A1' }), song({ name: 'A2' }), song({ name: 'A3' })],
      [song({ name: 'B1' })],
    ]);
    expect(merged.map((c) => c.name)).toEqual(['A1', 'B1', 'A2', 'A3']);
  });

  it('keeps one copy of a song two sources both returned', () => {
    const merged = mergeCandidates([
      [song({ name: 'Same Song', artist: 'Artist' })],
      [song({ name: 'same song', artist: 'ARTIST' })],
    ]);
    expect(merged).toHaveLength(1);
  });

  it('keeps same-titled songs by different artists apart', () => {
    // The dedupe key is title+artist, so a cover version and the original
    // are two candidates the user may genuinely want to choose between.
    const merged = mergeCandidates([
      [song({ name: 'Yesterday', artist: 'The Beatles' })],
      [song({ name: 'Yesterday', artist: '某翻唱' })],
    ]);
    expect(merged).toHaveLength(2);
  });

  it('stops at the limit', () => {
    const merged = mergeCandidates(
      [[song({ name: 'A' }), song({ name: 'B' })], [song({ name: 'C' })]],
      2,
    );
    expect(merged.map((c) => c.name)).toEqual(['A', 'C']);
  });

  it('tolerates an empty or absent source', () => {
    expect(mergeCandidates([])).toEqual([]);
    expect(mergeCandidates([[], [song({ name: 'A' })]]).map((c) => c.name)).toEqual(['A']);
  });
});

describe('bestCandidate', () => {  it('is the head of the merged ranking', () => {
    expect(bestCandidate([song({ name: 'A' }), song({ name: 'B' })])?.name).toBe('A');
  });

  it('is null rather than a fabricated candidate', () => {
    expect(bestCandidate([])).toBeNull();
  });
});

describe('clampPage', () => {
  it('has no page when there is nothing to page', () => {
    expect(clampPage(3, 0, 5)).toBe(0);
  });

  it('pulls an out-of-range page back into range', () => {
    // A re-search that returns fewer rows leaves the old page number in
    // state; rendering it raw shows an empty page with no way back.
    expect(clampPage(9, 7, 5)).toBe(2);
    expect(clampPage(0, 7, 5)).toBe(1);
  });
});

describe('withSource', () => {
  // Without this the batch report could not say WHICH source a row's tags
  // came from: a single-source lookup returns whatever the plugin put on
  // the wire, and most leave `source` empty, so every card and every
  // report line read "cloud".
  it('stamps the source it asked', () => {
    const stamped = withSource([song({ name: 'A' })], 'netease');
    expect(stamped[0].source).toBe('netease');
  });

  it('keeps a more specific id the source did supply', () => {
    const stamped = withSource([song({ name: 'A', source: 'musicbrainz-mbid' })], 'netease');
    expect(stamped[0].source).toBe('musicbrainz-mbid');
  });

  it('does not mutate the input', () => {
    const list = [song({ name: 'A' })];
    withSource(list, 'netease');
    expect(list[0].source).toBeUndefined();
  });
});

describe('normalizePageSize', () => {
  it('accepts every offered size', () => {
    for (const n of [5, 10, 20]) {
      expect(normalizePageSize(n)).toBe(n);
    }
  });

  it('falls back to the default for anything else', () => {
    // The input is a remembered number, so it can be a size this build
    // dropped, a zero, or not a number at all.
    for (const junk of [0, -5, 7, 1000, null, undefined, '10', NaN]) {
      expect(normalizePageSize(junk)).toBe(CANDIDATES_PER_PAGE);
    }
  });
});

describe('paginateCandidates', () => {
  const many = Array.from({ length: 12 }, (_, i) => song({ name: `S${i}` }));

  it('slices one page and counts the rest', () => {
    const page = paginateCandidates(many, 1, 5);
    expect(page.items.map((c) => c.name)).toEqual(['S0', 'S1', 'S2', 'S3', 'S4']);
    expect(page.pageCount).toBe(3);
    expect(page.total).toBe(12);
  });

  it('returns a short last page rather than padding it', () => {
    const page = paginateCandidates(many, 3, 5);
    expect(page.items).toHaveLength(2);
    expect(page.page).toBe(3);
  });

  it('defaults to five per page', () => {
    expect(CANDIDATES_PER_PAGE).toBe(5);
    expect(paginateCandidates(many, 1).items).toHaveLength(5);
  });

  it('honours a larger page size', () => {
    const page = paginateCandidates(many, 1, 10);
    expect(page.items).toHaveLength(10);
    expect(page.pageCount).toBe(2);
  });

  it('has no page at all for an empty list', () => {
    const page = paginateCandidates([], 1);
    expect(page).toMatchObject({ page: 0, pageCount: 0, total: 0 });
    expect(page.items).toEqual([]);
  });
});
