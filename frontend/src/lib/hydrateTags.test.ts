// Vitest for hydrateTagsBatched.
//
// Locks in the non-empty-write contract: write the row's musicInfo
// ONLY when the readTagsFromPath call resolves to ≥1 non-null tag
// field. Empty results and thrown errors both defer to the click
// path's `cacheHasAnyValue` retry — that's the property the
// integration depends on, so it's important to keep it from silently
// regressing under a future "always write" refactor.
//
// readTagsFromPath is the sole I/O boundary (POST /api/music_id3/);
// these tests mock it directly so concurrency / write-policy stay
// independent of axios envelope details.

import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('@/lib/id3Reader', () => ({
  readTagsFromPath: vi.fn(),
}));

import { hydrateTagsBatched, type HydratedTag } from '@/lib/hydrateTags';
import { readTagsFromPath } from '@/lib/id3Reader';

const mockedReadTagsFromPath = vi.mocked(readTagsFromPath);

beforeEach(() => {
  mockedReadTagsFromPath.mockReset();
  vi.spyOn(console, 'debug').mockImplementation(() => {});
});

/** Flatten a chunk-recording mock into `[id, info]` pairs, so an assertion
 *  about "which rows were written" reads the same whether the run produced
 *  one chunk of three or three chunks of one. */
function recorder(): {
  applyBatch: (rows: HydratedTag[]) => void;
  pairs: () => Array<[string, unknown]>;
  chunks: () => number;
} {
  const chunks: HydratedTag[][] = [];
  return {
    applyBatch: (rows) => {
      chunks.push(rows);
    },
    pairs: () => chunks.flat().map((r) => [r.id, r.info]),
    chunks: () => chunks.length,
  };
}

describe('hydrateTagsBatched - non-empty-write contract', () => {
  it('writes row.musicInfo on success-with-data', async () => {
    mockedReadTagsFromPath.mockResolvedValueOnce({
      title: 'X',
      artist: 'Y',
    });
    const rec = recorder();
    await hydrateTagsBatched(
      [{ id: 'a', fullPath: 'a.flac' }],
      rec.applyBatch,
      { concurrency: 1 },
    );
    expect(rec.pairs()).toEqual([['a', { title: 'X', artist: 'Y' }]]);
  });

  it('does NOT write on success-with-empty-data (preserves click retry)', async () => {
    mockedReadTagsFromPath.mockResolvedValueOnce({});
    const rec = recorder();
    await hydrateTagsBatched(
      [{ id: 'a', fullPath: 'a.flac' }],
      rec.applyBatch,
    );
    expect(rec.pairs()).toEqual([]);
    // And the dev-only diagnostic fires once so emptiness is debuggable.
    expect(console.debug).toHaveBeenCalled();
  });

  it('does NOT write when every response field is null', async () => {
    mockedReadTagsFromPath.mockResolvedValueOnce({
      title: null as unknown as undefined,
      artist: null as unknown as undefined,
    });
    const rec = recorder();
    await hydrateTagsBatched(
      [{ id: 'a', fullPath: 'a.flac' }],
      rec.applyBatch,
    );
    expect(rec.pairs()).toEqual([]);
  });

  it('does NOT write on thrown tag-parse error AND does NOT throw', async () => {
    mockedReadTagsFromPath.mockRejectedValueOnce(new Error('parse failed'));
    const rec = recorder();
    await expect(
      hydrateTagsBatched([{ id: 'a', fullPath: 'a.flac' }], rec.applyBatch),
    ).resolves.toBeUndefined();
    expect(rec.pairs()).toEqual([]);
    expect(console.debug).toHaveBeenCalled();
  });
});

// This is the regression test for the bug behind 「点确认没反应」: the
// writer used to be called once per FILE, and the store's per-row writer
// re-persists the entire table, so 4000 songs cost 4001 whole-table
// localStorage writes (2.6 GB measured) and froze the main thread for
// seconds. The fix is to hand the caller chunks instead, which makes the
// store commit count a function of chunkSize, not of library size.
describe('hydrateTagsBatched - write amortization', () => {
  it('never calls the writer at all when nothing was written', async () => {
    mockedReadTagsFromPath.mockImplementation(async () => ({}));
    const rec = recorder();
    await hydrateTagsBatched(
      Array.from({ length: 10 }, (_, i) => ({ id: `i${i}`, fullPath: `i${i}` })),
      rec.applyBatch,
    );
    expect(rec.chunks()).toBe(0);
  });

  it('flushes every written row, including the trailing partial chunk', async () => {
    mockedReadTagsFromPath.mockImplementation(async () => ({ title: 'X' }));
    const n = 250; // deliberately not a multiple of the chunk size
    const rec = recorder();
    await hydrateTagsBatched(
      Array.from({ length: n }, (_, i) => ({ id: `i${i}`, fullPath: `i${i}` })),
      rec.applyBatch,
      { concurrency: 1, chunkSize: 100 },
    );
    // Every id appears exactly once — a dropped tail chunk would lose the
    // last rows' tags forever.
    const ids = rec.pairs().map(([id]) => id);
    expect(ids).toHaveLength(n);
    expect(new Set(ids).size).toBe(n);
  });

  it('writes ceil(N / chunkSize) chunks, not N calls', async () => {
    mockedReadTagsFromPath.mockImplementation(async () => ({ title: 'X' }));
    const rec = recorder();
    await hydrateTagsBatched(
      Array.from({ length: 400 }, (_, i) => ({ id: `i${i}`, fullPath: `i${i}` })),
      rec.applyBatch,
      { concurrency: 1, chunkSize: 100 },
    );
    expect(rec.chunks()).toBe(4);
  });

  it('emits no chunk while the buffer is still below chunkSize mid-run', async () => {
    // The trailing flush is what guarantees no row is stranded; before it,
    // a batch smaller than one chunk must not have written anything yet.
    // A promise the test resolves by hand, so the read is genuinely still
    // in flight when we assert that nothing has been flushed.
    let release = () => {};
    mockedReadTagsFromPath.mockImplementation(
      async () =>
        new Promise<{ title: string }>((resolve) => {
          release = () => resolve({ title: 'X' });
        }),
    );
    const rec = recorder();
    const run = hydrateTagsBatched(
      [{ id: 'a', fullPath: 'a' }],
      rec.applyBatch,
      { concurrency: 1, chunkSize: 200 },
    );
    await new Promise((r) => setTimeout(r, 0));
    expect(rec.chunks()).toBe(0);
    release();
    await run;
    expect(rec.chunks()).toBe(1);
  });
});

describe('hydrateTagsBatched - shape & concurrency', () => {
  it('returns immediately for an empty item list', async () => {
    const setMusicInfo = vi.fn();
    await hydrateTagsBatched([], setMusicInfo);
    expect(setMusicInfo).not.toHaveBeenCalled();
    expect(mockedReadTagsFromPath).not.toHaveBeenCalled();
  });

  it('caps peak concurrency at the configured bound', async () => {
    let inFlight = 0;
    let peak = 0;
    mockedReadTagsFromPath.mockImplementation(async () => {
      inFlight++;
      peak = Math.max(peak, inFlight);
      // Force a microtask yield so the awaits actually overlap.
      // Without this the bodies all complete in the same tick and
      // `peak` is stuck at 1, masking a future regression in the
      // worker's `next++` claim semantics. The hydrator caps in-flight
      // awaiting, not synchronous body execution — write the harness
      // to measure what the contract actually says.
      await new Promise((r) => setTimeout(r, 5));
      inFlight--;
      return { title: 'X' };
    });
    const items = Array.from({ length: 12 }, (_, i) => ({
      id: `i${i}`,
      fullPath: `i${i}.flac`,
    }));
    await hydrateTagsBatched(items, () => {}, { concurrency: 4 });
    expect(peak).toBeLessThanOrEqual(4);
    // Sanity: not strictly serial. Two or more must overlap.
    expect(peak).toBeGreaterThanOrEqual(2);
  });

  it('processes every item regardless of mixed success/failure outcomes', async () => {
    mockedReadTagsFromPath.mockImplementation(async (fullPath: string) => {
      if (fullPath.endsWith('empty')) return {};
      if (fullPath.endsWith('bad')) throw new Error('parse fail');
      return { title: 'X' };
    });
    const rec = recorder();
    await hydrateTagsBatched(
      [
        { id: 'good', fullPath: 'a/good.flac' },
        { id: 'empty', fullPath: 'b/empty' },
        { id: 'bad', fullPath: 'c/bad' },
      ],
      rec.applyBatch,
    );
    expect(rec.pairs().map(([id]) => id)).toEqual(['good']);
  });
});
