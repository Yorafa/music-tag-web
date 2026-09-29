// The regression tests for 「添加目录」 at scale.
//
// What this file used to cover, and where those tests went:
//
//   - "expansion is serial, N requests for N directories" and the ordering
//     guarantees that came with the client-side walk. Those are now
//     IMPOSSIBLE to regress in the client: expansion is one
//     /api/file_list_recursive/ call that walks the tree server-side. The
//     invariant that replaced them lives in Go, in
//     internal/gateway/handler/file_recursive_test.go — including the
//     arbitrary-depth case and the traversal guard.
//
// What remains here is what is still the frontend's responsibility:
//   1. The post-enqueue persistence must stay linear in queue size. The
//      per-row writer re-serializes the WHOLE table on every call, so
//      fanning N files through it is O(N²) — measured at 4000 songs as
//      4001 whole-table writes totalling 2.6 GB, blocking the main thread
//      ~8.4 s. That is what 「点了确认没反应」 actually was.
//   2. A refused persist must be reported, not swallowed.
//   3. Hydration must actually deliver every row's tags — chunking the
//      writes must not cost correctness.

import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('@/api/client', () => ({
  getFileList: vi.fn(),
  fileListRecursive: vi.fn(),
  getAlbumCoverUrl: vi.fn(() => '/api/album_cover/'),
}));
vi.mock('@/lib/id3Reader', () => ({
  readTagsFromPath: vi.fn(async () => ({
    title: 'T',
    artist: 'A',
    album: 'Al',
    year: '2001',
  })),
}));

import { fileListRecursive } from '@/api/client';
import { useWorklistStore } from '@/store/useWorklistStore';

const fileListRecursiveMock = vi.mocked(fileListRecursive);

function serveFiles(count: number, source = 'Music') {
  fileListRecursiveMock.mockResolvedValue({
    files: Array.from({ length: count }, (_, i) => ({
      path: `${source}/Album ${i % 40}/track${i}.flac`,
      name: `track${i}.flac`,
      size: 1_000_000,
      source,
    })),
    truncated: false,
    dirsVisited: 40,
  });
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
  fileListRecursiveMock.mockReset();
});

// ─── 1. the quadratic persist ──────────────────────────────────────────
describe('enqueueDirs: persistence cost stays linear', () => {
  it('does not re-persist the whole queue once per hydrated file', async () => {
    const songs = 2000;
    serveFiles(songs);

    const spy = watchStorage();
    try {
      await useWorklistStore.getState().enqueueDirs(['Music']);
      // Drain the fire-and-forget hydrator.
      await new Promise((r) => setTimeout(r, 100));

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
    serveFiles(20);
    await useWorklistStore.getState().enqueueDirs(['Music']);
    await new Promise((r) => setTimeout(r, 100));
    const rows = useWorklistStore.getState().rows;
    expect(rows).toHaveLength(20);
    expect(rows.every((r) => r.musicInfo?.title === 'T')).toBe(true);
  });
});

// ─── 2. the silent quota failure ───────────────────────────────────────
describe('enqueueDirs: a refused persist is reported, not swallowed', () => {
  it('returns persisted=false with a quota reason when the write is refused', async () => {
    serveFiles(4);
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
    serveFiles(4);
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
    serveFiles(4);
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
    // And no HTTP call — an empty selection is answered locally.
    expect(fileListRecursiveMock).not.toHaveBeenCalled();
  });
});

// ─── 3. expansion is one request, whatever the tree looks like ─────────
describe('enqueueDirs: expansion asks the server once', () => {
  it('issues exactly one call regardless of how many tracks come back', async () => {
    // The property the client-side walk could not have: 500 tracks behind
    // 2500 directories used to be 2500 round trips, and no concurrency
    // setting fixes that, because BFS serialises on depth. The server
    // walks the subtree in one pass, so the client asks once.
    serveFiles(500);
    await useWorklistStore.getState().enqueueDirs(['Music']);
    expect(fileListRecursiveMock).toHaveBeenCalledTimes(1);
  });

  it('passes the whole selection through in that one call', async () => {
    serveFiles(3);
    await useWorklistStore.getState().enqueueDirs(['A', 'B']);
    expect(fileListRecursiveMock).toHaveBeenCalledTimes(1);
    expect(fileListRecursiveMock.mock.calls[0][0]).toEqual(['A', 'B']);
  });

  it('turns each returned path into a queued row', async () => {
    serveFiles(3);
    const result = await useWorklistStore.getState().enqueueDirs(['Music']);
    const rows = useWorklistStore.getState().rows;
    expect(result.files).toBe(3);
    expect(rows.map((r) => r.id)).toEqual(
      rows.map((r) => `Music/Album ${rows.indexOf(r) % 40}/track${rows.indexOf(r)}.flac`),
    );
    expect(rows.every((r) => r.status === 'pending')).toBe(true);
  });
});
