// The regression tests for 「添加目录 → 点确认 → 界面没反应」.
//
// The symptom was reported as "large directories do nothing when confirmed".
// It was never a failed request — the work completed, but the main thread
// was welded shut while it did, and the user saw a dead button. Two
// independent causes, both measured here:
//
//   1. O(N²) persistence. The post-enqueue tag hydrator called the store's
//      per-row writer once per file, and that writer re-serializes and
//      re-writes the ENTIRE queue to localStorage on every call. At 4000
//      songs that measured 4001 whole-table writes totalling 2.6 GB, with
//      the main thread blocked ~8.4 s.
//
//   2. Serial expansion. expandDirsToAudioFiles did one blocking HTTP round
//      trip per directory, depth-first. A 200-album selection is 201
//      sequential calls (~10 s at 50 ms RTT) with no progress indicator.
//
// Both are silent by construction: no exception, no failed request, no
// console output. The only way to catch a regression is to count the
// operations, which is what these tests do.

import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('@/api/client', () => ({
  getFileList: vi.fn(),
}));
vi.mock('@/lib/id3Reader', () => ({
  readTagsFromPath: vi.fn(async () => ({
    title: 'T',
    artist: 'A',
    album: 'Al',
    year: '2001',
  })),
}));

import { getFileList } from '@/api/client';
import { useWorklistStore } from '@/store/useWorklistStore';
import { expandDirsToAudioFiles } from '@/utils/expandDirs';

const getFileListMock = vi.mocked(getFileList);

type Node = {
  id: number;
  name: string;
  icon: string;
  state: string;
  children?: Node[];
};

/** Serve `albums` subdirectories, each holding `perAlbum` audio files. */
function serveTree(albums: number, perAlbum: number, rttMs = 0): { calls: string[] } {
  const calls: string[] = [];
  getFileListMock.mockImplementation(async (p: string) => {
    calls.push(p);
    if (rttMs) await new Promise((r) => setTimeout(r, rttMs));
    const parts = p.split('/').filter(Boolean);
    const children: Node[] =
      parts.length <= 1
        ? Array.from({ length: albums }, (_, i) => ({
            id: i,
            name: `Album ${i}`,
            icon: 'icon-folder',
            state: 'null',
          }))
        : Array.from({ length: perAlbum }, (_, i) => ({
            id: i,
            name: `track${String(i).padStart(2, '0')}.flac`,
            icon: 'icon-script-files',
            state: 'null',
          }));
    return {
      result: true,
      code: '200',
      message: 'ok',
      data: [{ id: 0, name: '', icon: 'icon-folder', state: 'null', children }],
    };
  });
  return { calls };
}

/** Count localStorage writes and their total size, so an O(N²) regression
 *  shows up as a byte count rather than as a slow test. */
function watchStorage() {
  const stats = { calls: 0, bytes: 0 };
  const real = window.localStorage.setItem.bind(window.localStorage);
  window.localStorage.setItem = ((k: string, v: string) => {
    if (k === 'worklist.v1') {
      stats.calls += 1;
      stats.bytes += v.length;
    }
    real(k, v);
  }) as typeof window.localStorage.setItem;
  return {
    stats,
    restore: () => {
      window.localStorage.setItem = real;
    },
  };
}

beforeEach(() => {
  localStorage.clear();
  useWorklistStore.setState({ rows: [], selectedIds: [] });
  getFileListMock.mockReset();
});

// ─── 1. the quadratic persist ──────────────────────────────────────────
describe('enqueueDirs: persistence cost stays linear', () => {
  it('does not re-persist the whole queue once per hydrated file', async () => {
    const albums = 100;
    const perAlbum = 20;
    const songs = albums * perAlbum;
    serveTree(albums, perAlbum);

    const spy = watchStorage();
    try {
      await useWorklistStore.getState().enqueueDirs(['Music']);
      // Drain the fire-and-forget hydrator.
      await new Promise((r) => setTimeout(r, 50));

      expect(useWorklistStore.getState().rows).toHaveLength(songs);

      // One write per chunk, not one per file. The old code wrote 2001
      // times for this tree (~2000 × growing table). Allowing a generous
      // multiple of the chunk count keeps this from being brittle about
      // the exact chunk size while still failing loudly at O(N²).
      expect(spy.stats.calls).toBeLessThan(songs / 20);

      // The byte total is the sharper assertion: the quadratic version
      // wrote >100 MB here because each write carried the whole table.
      // Linear amortization writes the table a handful of times, so the
      // total stays a small multiple of the final size.
      const finalBytes = (localStorage.getItem('worklist.v1') ?? '').length;
      expect(spy.stats.bytes).toBeLessThan(finalBytes * 20);
    } finally {
      spy.restore();
    }
  });

  it('still applies every hydrated tag to its row', async () => {
    // The amortization must not cost correctness: a chunked writer that
    // dropped the tail would leave rows showing bare filenames forever.
    serveTree(5, 4);
    await useWorklistStore.getState().enqueueDirs(['Music']);
    await new Promise((r) => setTimeout(r, 50));
    const rows = useWorklistStore.getState().rows;
    expect(rows).toHaveLength(20);
    expect(rows.every((r) => r.musicInfo?.title === 'T')).toBe(true);
  });
});

// ─── 2. the serial expansion ───────────────────────────────────────────
describe('expandDirsToAudioFiles: bounded concurrency, deterministic order', () => {
  it('keeps more than one listing in flight', async () => {
    let inFlight = 0;
    let peak = 0;
    const { calls } = serveTree(0, 0);
    void calls;
    getFileListMock.mockImplementation(async (p: string) => {
      const parts = p.split('/').filter(Boolean);
      if (parts.length <= 1) {
        return {
          result: true,
          code: '200',
          message: 'ok',
          data: [
            {
              id: 0,
              name: '',
              icon: 'icon-folder',
              state: 'null',
              children: Array.from({ length: 40 }, (_, i) => ({
                id: i,
                name: `Album ${String(i).padStart(2, '0')}`,
                icon: 'icon-folder',
                state: 'null',
              })),
            },
          ],
        };
      }
      inFlight++;
      peak = Math.max(peak, inFlight);
      // Yield so overlapping awaits are actually observable.
      await new Promise((r) => setTimeout(r, 2));
      inFlight--;
      return {
        result: true,
        code: '200',
        message: 'ok',
        data: [
          {
            id: 0,
            name: '',
            icon: 'icon-folder',
            state: 'null',
            children: [
              {
                id: 0,
                name: 'track.flac',
                icon: 'icon-script-files',
                state: 'null',
              },
            ],
          },
        ],
      };
    });

    await expandDirsToAudioFiles(['Music']);
    expect(peak).toBeGreaterThan(1);
  });

  it('never exceeds the concurrency bound', async () => {
    let inFlight = 0;
    let peak = 0;
    getFileListMock.mockImplementation(async (p: string) => {
      const parts = p.split('/').filter(Boolean);
      if (parts.length <= 1) {
        return {
          result: true,
          code: '200',
          message: 'ok',
          data: [
            {
              id: 0,
              name: '',
              icon: 'icon-folder',
              state: 'null',
              children: Array.from({ length: 60 }, (_, i) => ({
                id: i,
                name: `Album ${i}`,
                icon: 'icon-folder',
                state: 'null',
              })),
            },
          ],
        };
      }
      inFlight++;
      peak = Math.max(peak, inFlight);
      await new Promise((r) => setTimeout(r, 2));
      inFlight--;
      return { result: true, code: '200', message: 'ok', data: [] };
    });

    await expandDirsToAudioFiles(['Music']);
    // The bound exists so adding a directory cannot turn into an I/O storm
    // on the user's music volume. Unbounded fan-out would pass the test
    // above trivially.
    expect(peak).toBeLessThanOrEqual(6);
  });

  it('emits results in directory order, not completion order', async () => {
    // Concurrency must not make the worklist's row order depend on which
    // HTTP call happened to finish first — the same selection has to
    // produce the same rows every time.
    getFileListMock.mockImplementation(async (p: string) => {
      const parts = p.split('/').filter(Boolean);
      if (parts.length <= 1) {
        return {
          result: true,
          code: '200',
          message: 'ok',
          data: [
            {
              id: 0,
              name: '',
              icon: 'icon-folder',
              state: 'null',
              children: ['a', 'b', 'c', 'd'].map((n, i) => ({
                id: i,
                name: n,
                icon: 'icon-folder',
                state: 'null',
              })),
            },
          ],
        };
      }
      // Reverse the delay by name so completion order is the REVERSE of
      // directory order. Only index-ordered results survive this.
      const delay = { a: 20, b: 14, c: 8, d: 2 }[parts[1]] ?? 0;
      await new Promise((r) => setTimeout(r, delay));
      return {
        result: true,
        code: '200',
        message: 'ok',
        data: [
          {
            id: 0,
            name: '',
            icon: 'icon-folder',
            state: 'null',
            children: [
              {
                id: 0,
                name: `${parts[1]}.flac`,
                icon: 'icon-script-files',
                state: 'null',
              },
            ],
          },
        ],
      };
    });

    const out = await expandDirsToAudioFiles(['Music']);
    expect(out.map((e) => e.fullPath)).toEqual([
      'Music/a/a.flac',
      'Music/b/b.flac',
      'Music/c/c.flac',
      'Music/d/d.flac',
    ]);
  });

  it('visits subdirectories one level at a time', async () => {
    // Level order is the documented contract: a level's files are emitted
    // before its grandchildren's. Depth-first used to interleave them.
    getFileListMock.mockImplementation(async (p: string) => {
      const parts = p.split('/').filter(Boolean);
      if (parts.length === 1) {
        return {
          result: true,
          code: '200',
          message: 'ok',
          data: [
            {
              id: 0,
              name: '',
              icon: 'icon-folder',
              state: 'null',
              children: [
                {
                  id: 0,
                  name: 'deep.flac',
                  icon: 'icon-script-files',
                  state: 'null',
                },
                {
                  id: 1,
                  name: 'sub',
                  icon: 'icon-folder',
                  state: 'null',
                },
              ],
            },
          ],
        };
      }
      return {
        result: true,
        code: '200',
        message: 'ok',
        data: [
          {
            id: 0,
            name: '',
            icon: 'icon-folder',
            state: 'null',
            children: [
              {
                id: 0,
                name: 'deeper.flac',
                icon: 'icon-script-files',
                state: 'null',
              },
            ],
          },
        ],
      };
    });

    const out = await expandDirsToAudioFiles(['Music']);
    expect(out.map((e) => e.fullPath)).toEqual([
      'Music/deep.flac',
      'Music/sub/deeper.flac',
    ]);
  });

  it('skips an unreadable directory without losing its siblings', async () => {
    getFileListMock.mockImplementation(async (p: string) => {
      const parts = p.split('/').filter(Boolean);
      if (parts.length === 1) {
        return {
          result: true,
          code: '200',
          message: 'ok',
          data: [
            {
              id: 0,
              name: '',
              icon: 'icon-folder',
              state: 'null',
              children: [
                { id: 0, name: 'ok', icon: 'icon-folder', state: 'null' },
                { id: 1, name: 'bad', icon: 'icon-folder', state: 'null' },
              ],
            },
          ],
        };
      }
      if (parts[1] === 'bad') throw new Error('500');
      return {
        result: true,
        code: '200',
        message: 'ok',
        data: [
          {
            id: 0,
            name: '',
            icon: 'icon-folder',
            state: 'null',
            children: [
              {
                id: 0,
                name: `${parts[1]}.flac`,
                icon: 'icon-script-files',
                state: 'null',
              },
            ],
          },
        ],
      };
    });

    const out = await expandDirsToAudioFiles(['Music']);
    // 'bad' throws and contributes nothing; its sibling 'ok' still lands.
    expect(out.map((e) => e.fullPath)).toEqual(['Music/ok/ok.flac']);
  });
});

// ─── 3. the silent quota failure ───────────────────────────────────────
describe('enqueueDirs: a refused persist is reported, not swallowed', () => {
  it('returns persisted=false with a quota reason when the write is refused', async () => {
    serveTree(2, 2);
    const real = window.localStorage.setItem.bind(window.localStorage);
    window.localStorage.setItem = (() => {
      const err = new Error('quota');
      err.name = 'QuotaExceededError';
      throw err;
    }) as typeof window.localStorage.setItem;

    try {
      const result = await useWorklistStore.getState().enqueueDirs(['Music']);
      // The rows ARE in memory — only the save failed. Reporting that
      // honestly is the whole point: the old code swallowed it and the
      // user found out at the next reload.
      expect(result.files).toBe(4);
      expect(useWorklistStore.getState().rows).toHaveLength(4);
      expect(result.persisted).toBe(false);
      expect(result.persistError).toBe('quota');
    } finally {
      window.localStorage.setItem = real;
    }
  });

  it('distinguishes "storage unavailable" from "too big"', async () => {
    serveTree(2, 2);
    const real = window.localStorage.setItem.bind(window.localStorage);
    window.localStorage.setItem = (() => {
      throw new Error('SecurityError: storage is disabled');
    }) as typeof window.localStorage.setItem;

    try {
      const result = await useWorklistStore.getState().enqueueDirs(['Music']);
      expect(result.persisted).toBe(false);
      // Not 'quota': telling a private-browsing user their queue is "too
      // big" sends them to delete music that was never the problem.
      expect(result.persistError).toBe('unavailable');
    } finally {
      window.localStorage.setItem = real;
    }
  });

  it('reports success when the write lands', async () => {
    serveTree(2, 2);
    const result = await useWorklistStore.getState().enqueueDirs(['Music']);
    expect(result.persisted).toBe(true);
    expect(result.persistError).toBeNull();
  });

  it('reports success for an empty selection without touching storage', async () => {
    const result = await useWorklistStore.getState().enqueueDirs([]);
    expect(result).toEqual({
      added: 0,
      skipped: 0,
      files: 0,
      persisted: true,
      persistError: null,
    });
  });
});
