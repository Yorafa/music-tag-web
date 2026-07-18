// Vitest for hydrateTagsBatched.
//
// Locks in the non-empty-write contract: write the row's musicInfo
// ONLY when the readTagsFromPath call resolves to ≥1 non-null tag
// field. Empty results and thrown errors both defer to the click
// path's `cacheHasAnyValue` retry — that's the property the
// integration depends on, so it's important to keep it from silently
// regressing under a future "always write" refactor.
//
// Mocking shape changed from the original POST /api/music_id3/
// envelope (`{result, code, message, data}`) to the direct return
// value of the in-browser reader (`Partial<MusicTagInfo>`) — same
// semantics, simpler mock bodies. Tests that previously asserted
// "Failure envelope absent" now assert "thrown error" since the
// in-browser reader has no envelope concept.

import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('@/lib/id3Reader', () => ({
  readTagsFromPath: vi.fn(),
}));

import { hydrateTagsBatched } from '@/lib/hydrateTags';
import { readTagsFromPath } from '@/lib/id3Reader';

const mockedReadTagsFromPath = vi.mocked(readTagsFromPath);

beforeEach(() => {
  mockedReadTagsFromPath.mockReset();
  vi.spyOn(console, 'debug').mockImplementation(() => {});
});

describe('hydrateTagsBatched - non-empty-write contract', () => {
  it('writes row.musicInfo on success-with-data', async () => {
    mockedReadTagsFromPath.mockResolvedValueOnce({
      title: 'X',
      artist: 'Y',
    });
    const setMusicInfo = vi.fn();
    await hydrateTagsBatched(
      [{ id: 'a', fullPath: 'a.flac' }],
      setMusicInfo,
      { concurrency: 1 },
    );
    expect(setMusicInfo).toHaveBeenCalledTimes(1);
    expect(setMusicInfo).toHaveBeenCalledWith('a', { title: 'X', artist: 'Y' });
  });

  it('does NOT write on success-with-empty-data (preserves click retry)', async () => {
    mockedReadTagsFromPath.mockResolvedValueOnce({});
    const setMusicInfo = vi.fn();
    await hydrateTagsBatched(
      [{ id: 'a', fullPath: 'a.flac' }],
      setMusicInfo,
    );
    expect(setMusicInfo).not.toHaveBeenCalled();
    // And the dev-only diagnostic fires once so emptiness is debuggable.
    expect(console.debug).toHaveBeenCalled();
  });

  it('does NOT write when every response field is null', async () => {
    mockedReadTagsFromPath.mockResolvedValueOnce({
      title: null as unknown as undefined,
      artist: null as unknown as undefined,
    });
    const setMusicInfo = vi.fn();
    await hydrateTagsBatched(
      [{ id: 'a', fullPath: 'a.flac' }],
      setMusicInfo,
    );
    expect(setMusicInfo).not.toHaveBeenCalled();
  });

  it('does NOT write on thrown tag-parse error AND does NOT throw', async () => {
    mockedReadTagsFromPath.mockRejectedValueOnce(new Error('parse failed'));
    const setMusicInfo = vi.fn();
    await expect(
      hydrateTagsBatched([{ id: 'a', fullPath: 'a.flac' }], setMusicInfo),
    ).resolves.toBeUndefined();
    expect(setMusicInfo).not.toHaveBeenCalled();
    expect(console.debug).toHaveBeenCalled();
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
    const writes: Array<[string, unknown]> = [];
    await hydrateTagsBatched(
      [
        { id: 'good', fullPath: 'a/good.flac' },
        { id: 'empty', fullPath: 'b/empty' },
        { id: 'bad', fullPath: 'c/bad' },
      ],
      (id, info) => writes.push([id, info]),
    );
    expect(writes.map(([id]) => id)).toEqual(['good']);
  });
});
