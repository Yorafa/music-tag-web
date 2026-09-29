import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('@/api/client', () => ({
  fetchId3ByTitle: vi.fn(),
  fetchLyric: vi.fn(),
}));

import { fetchId3ByTitle, fetchLyric } from '@/api/client';
import {
  isRealLyric,
  fetchLyricForSong,
  fetchLyricAcross,
  LYRIC_NOT_FOUND_PREFIX,
} from '@/api/lyrics';
import type { MusicSource } from '@/types';

const mockedSearch = vi.mocked(fetchId3ByTitle);
const mockedLyric = vi.mocked(fetchLyric);

function envelope<T>(data: T) {
  return { result: true, code: '200', data, message: 'success' };
}

beforeEach(() => {
  mockedSearch.mockReset();
  mockedLyric.mockReset();
});

describe('isRealLyric', () => {
  it('accepts a non-empty LRC string', () => {
    expect(isRealLyric('[00:00.00] hi')).toBe(true);
  });

  it('rejects the not-found sentinel the backend returns on the success path', () => {
    expect(isRealLyric(`${LYRIC_NOT_FOUND_PREFIX} rpc error`)).toBe(false);
  });

  it('rejects empty, whitespace-only, and non-string payloads', () => {
    expect(isRealLyric('')).toBe(false);
    expect(isRealLyric('   \n ')).toBe(false);
    expect(isRealLyric(null)).toBe(false);
    expect(isRealLyric(undefined)).toBe(false);
    expect(isRealLyric(42)).toBe(false);
  });
});

describe('fetchLyricForSong', () => {
  it('returns the lyric string when the source has one', async () => {
    mockedLyric.mockResolvedValueOnce(envelope('[00:01.00] la'));
    await expect(fetchLyricForSong('123', 'netease')).resolves.toBe('[00:01.00] la');
    expect(mockedLyric).toHaveBeenCalledWith('123', 'netease');
  });

  it('returns null on the not-found sentinel', async () => {
    mockedLyric.mockResolvedValueOnce(envelope(`${LYRIC_NOT_FOUND_PREFIX} nope`));
    await expect(fetchLyricForSong('123', 'netease')).resolves.toBeNull();
  });

  it('returns null (not throw) when the request rejects', async () => {
    mockedLyric.mockRejectedValueOnce(new Error('network'));
    await expect(fetchLyricForSong('123', 'kuwo')).resolves.toBeNull();
  });

  it('short-circuits without a request when id or source is missing', async () => {
    await expect(fetchLyricForSong('', 'netease')).resolves.toBeNull();
    await expect(fetchLyricForSong('123', '')).resolves.toBeNull();
    expect(mockedLyric).not.toHaveBeenCalled();
  });
});

describe('fetchLyricAcross', () => {
  const sources: MusicSource[] = ['netease', 'kuwo', 'qmusic'];

  it('resolves song_id by searching, then fetches that id — not the title', async () => {
    mockedSearch.mockResolvedValueOnce(envelope([{ id: '567', name: '歌' }]));
    mockedLyric.mockResolvedValueOnce(envelope('[00:00.00] found'));

    const hit = await fetchLyricAcross('歌名', ['netease'], '/m/a.mp3');
    expect(hit).toEqual({ lyric: '[00:00.00] found', source: 'netease' });
    // song_id passed to FetchLyric is the search result's id, not the query.
    expect(mockedSearch).toHaveBeenCalledWith('歌名', 'netease', '/m/a.mp3', 1);
    expect(mockedLyric).toHaveBeenCalledWith('567', 'netease');
  });

  it('falls through to the next source when the first has no lyric', async () => {
    // netease: has a candidate but no lyric → fall through
    mockedSearch.mockResolvedValueOnce(envelope([{ id: '1', name: 'a' }]));
    mockedLyric.mockResolvedValueOnce(envelope(''));
    // kuwo: candidate + lyric → win
    mockedSearch.mockResolvedValueOnce(envelope([{ id: '2', name: 'a' }]));
    mockedLyric.mockResolvedValueOnce(envelope('[00:00.00] kuwo lyric'));

    const hit = await fetchLyricAcross('a', sources);
    expect(hit).toEqual({ lyric: '[00:00.00] kuwo lyric', source: 'kuwo' });
    expect(mockedLyric).toHaveBeenCalledTimes(2);
  });

  it('stops at the first hit and does not ask later sources', async () => {
    mockedSearch.mockResolvedValueOnce(envelope([{ id: '1', name: 'a' }]));
    mockedLyric.mockResolvedValueOnce(envelope('[00:00.00] first'));

    const hit = await fetchLyricAcross('a', sources);
    expect(hit?.source).toBe('netease');
    // Only the winning source was searched.
    expect(mockedSearch).toHaveBeenCalledTimes(1);
    expect(mockedLyric).toHaveBeenCalledTimes(1);
  });

  it('skips a source whose search returns no candidate', async () => {
    mockedSearch.mockResolvedValueOnce(envelope([])); // netease: empty
    mockedSearch.mockResolvedValueOnce(envelope([{ id: '9', name: 'a' }])); // kuwo
    mockedLyric.mockResolvedValueOnce(envelope('[00:00.00] kuwo'));

    const hit = await fetchLyricAcross('a', sources);
    expect(hit?.source).toBe('kuwo');
    expect(mockedLyric).toHaveBeenCalledTimes(1);
    expect(mockedLyric).toHaveBeenCalledWith('9', 'kuwo');
  });

  it('continues past a source whose search throws', async () => {
    mockedSearch.mockRejectedValueOnce(new Error('down')); // netease
    mockedSearch.mockResolvedValueOnce(envelope([{ id: '3', name: 'a' }])); // kuwo
    mockedLyric.mockResolvedValueOnce(envelope('[00:00.00] ok'));

    const hit = await fetchLyricAcross('a', sources);
    expect(hit?.source).toBe('kuwo');
  });

  it('returns null when no source yields a lyric', async () => {
    mockedSearch.mockResolvedValue(envelope([{ id: '1', name: 'a' }]));
    mockedLyric.mockResolvedValue(envelope(''));

    await expect(fetchLyricAcross('a', sources)).resolves.toBeNull();
  });

  it('returns null on empty query without any request', async () => {
    await expect(fetchLyricAcross('', sources)).resolves.toBeNull();
    expect(mockedSearch).not.toHaveBeenCalled();
  });
});
