// Vitest for lib/id3Reader.ts.
//
// Locks in the two cross-stack contracts that hydrateTags / Worklist
// openEditor / PlayView.openEditorFor depend on but which musn't
// regress silently:
//
//   1. JWT Authorization header: `authedFetch` MUST inject the
//      current accessToken from useAuthStore into every fetch() it
//      makes against /media/*. Without this the gateway's authed
//      group returns 401 and the user sees a fake "tag fetch
//      failed" toast that masks the auth-regression.
//
//   2. ID3v1 tail fallback: when the front-chunk parse yields 0
//      common tags, readTagsFromPath MUST issue a second Range
//      bytes=-4096 fetch and merge the result. Otherwise legacy
//      CD-rip MP3s (ID3v1 only) infinitely loop on the
//      cacheHasAnyValue retry path.
//
// Mocks fetch via vi.stubGlobal — vi.spyOn(global, ...) leaves
// jsdom's URL parser as the res­olver, which then chokes on the
// relative /media/* paths id3Reader hit. Stubbing globalThis.fetch
// replaces BOTH the lookup AND the parser semantics for the call.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('music-metadata', () => ({
  parseBuffer: vi.fn(),
}));

vi.mock('@/store/useAuthStore', () => ({
  useAuthStore: {
    getState: vi.fn(),
  },
}));

import { readTagsFromPath } from '@/lib/id3Reader';
import { parseBuffer } from 'music-metadata';
import { useAuthStore } from '@/store/useAuthStore';

const mockedParseBuffer = vi.mocked(parseBuffer);
const mockedGetState = vi.mocked(useAuthStore.getState);

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedParseBuffer.mockReset();
  mockedGetState.mockReset();
  // Default token; per-test override available below.
  mockedGetState.mockReturnValue({ accessToken: 'TESTTOKEN' } as never);

  // Stub globalThis.fetch for the test. The mock returns a fresh
  // 206 Partial Content with empty bytes for every call — using
  // mockImplementation (not mockResolvedValue) is critical because
  // a Response body can only be consumed once. mockResolvedValue
  // hands back the SAME Response instance on every call, so the
  // second authedFetch in the ID3v1 fallback path trips
  // "Body is unusable: Body has already been read". Per-test
  // overrides use mockResolvedValueOnce.
  fetchMock = vi.fn().mockImplementation(() =>
    Promise.resolve(
      new Response(new Uint8Array([]), {
        status: 206,
        headers: { 'Content-Range': 'bytes 0-0/1' },
      }),
    ),
  );
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('readTagsFromPath - JWT auth', () => {
  it('injects Authorization: JWT <token> from useAuthStore on Range fetch', async () => {
    mockedParseBuffer.mockResolvedValueOnce({
      common: { title: 'X' },
    } as never);

    await readTagsFromPath('dir/song.flac');

    expect(fetchMock).toHaveBeenCalled();
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toContain('/media/');
    const headers = (init?.headers ?? {}) as Record<string, string>;
    expect(headers.Authorization).toBe('JWT TESTTOKEN');
    expect(headers.Range).toMatch(/^bytes=0-/);
  });

  it('omits Authorization header when no accessToken', async () => {
    mockedGetState.mockReturnValue({ accessToken: null } as never);
    mockedParseBuffer.mockResolvedValueOnce({
      common: { title: 'X' },
    } as never);

    await readTagsFromPath('dir/song.flac');

    const [, init] = fetchMock.mock.calls[0];
    const headers = (init?.headers ?? {}) as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
    expect(headers.Range).toMatch(/^bytes=0-/);
  });
});

describe('readTagsFromPath - ID3v1 tail fallback', () => {
  it('skips tail fetch when front common has any populated tag', async () => {
    mockedParseBuffer.mockResolvedValueOnce({
      common: { title: 'HasTags' },
    } as never);

    const result = await readTagsFromPath('legacy.mp3');

    expect(result.title).toBe('HasTags');
    // Front chunk only — no second fetch.
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('falls back to tail Range when front common is empty', async () => {
    // Front parseBuffer: empty common → triggers fallback.
    mockedParseBuffer.mockResolvedValueOnce({
      common: {},
    } as never);

    // Tail parseBuffer: real ID3v1 title + artist.
    mockedParseBuffer.mockResolvedValueOnce({
      common: { title: 'ID3v1Only', artist: 'Anon' },
    } as never);

    const result = await readTagsFromPath('legacy.mp3');

    expect(result.title).toBe('ID3v1Only');
    expect(result.artist).toBe('Anon');
    // Two fetches: front 256KB window + tail 4KB.
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const secondCallHeaders = (fetchMock.mock.calls[1][1]?.headers ??
      {}) as Record<string, string>;
    expect(secondCallHeaders.Range).toBe('bytes=-4096');
  });

  it('falls back gracefully when tail parse throws (returns front verdict)', async () => {
    mockedParseBuffer
      .mockResolvedValueOnce({ common: {} } as never)
      .mockRejectedValueOnce(new Error('tail parse failed'));

    const result = await readTagsFromPath('partial.flac');

    // Front was empty, tail threw → result remains empty.
    expect(result).toEqual({});
  });

  it('throws on transport failure from the front fetch (no silent swallow)', async () => {
    fetchMock.mockReset();
    fetchMock.mockImplementation(() =>
      Promise.resolve(
        new Response(null, { status: 500, statusText: 'boom' }),
      ),
    );

    await expect(readTagsFromPath('broken.mp3')).rejects.toThrow(
      /fetch failed: 500/,
    );
  });

  it('returns empty Partial on fetch success but parseBuffer produces no common', async () => {
    // Front fetch succeeds with 200 (server ignored Range), parseBuffer
    // returns empty common, tail fallback attempted but ALSO empty
    // common — final result is empty Partial, not null.
    mockedParseBuffer.mockResolvedValue({ common: {} } as never);

    const result = await readTagsFromPath('untagged.mp3');

    expect(result).toEqual({});
    // Both fetches fired (front + tail fallback attempt).
    expect(fetchMock).toHaveBeenCalled();
  });
});
