// Vitest for lib/id3Reader.ts — backend music_id3 path.
//
// Locks contracts hydrateTags / Worklist openEditor / PlayView depend on:
//
//   1. fullPath is split into {file_path, file_name} for getMusicId3
//   2. result:false or transport failure throws (no silent empty)
//   3. empty / array data → {} (preserves cacheHasAnyValue click retry)
//   4. numeric year/size from backend are stringified for MusicTagInfo

import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('@/api/client', () => ({
  getMusicId3: vi.fn(),
}));

import { readTagsFromPath, splitFullPath } from '@/lib/id3Reader';
import { getMusicId3 } from '@/api/client';

const mockedGetMusicId3 = vi.mocked(getMusicId3);

beforeEach(() => {
  mockedGetMusicId3.mockReset();
});

describe('splitFullPath', () => {
  it('splits nested path into dir + basename', () => {
    expect(splitFullPath('17/XXXTENTACION - Jocelyn Flores.ogg')).toEqual({
      filePath: '17',
      fileName: 'XXXTENTACION - Jocelyn Flores.ogg',
    });
  });

  it('uses empty file_path for root-level files', () => {
    expect(splitFullPath('song.flac')).toEqual({
      filePath: '',
      fileName: 'song.flac',
    });
  });
});

describe('readTagsFromPath - music_id3 API', () => {
  it('calls getMusicId3 with split path and maps tags', async () => {
    mockedGetMusicId3.mockResolvedValueOnce({
      result: true,
      code: '200',
      message: 'success',
      data: {
        title: 'Jocelyn Flores',
        artist: 'XXXTENTACION',
        year: 2017,
        size: 12345,
        filename: 'XXXTENTACION - Jocelyn Flores.ogg',
        artwork: 'data:image/jpeg;base64,abc',
        artwork_w: 600,
      },
    });

    const result = await readTagsFromPath('17/XXXTENTACION - Jocelyn Flores.ogg');

    expect(mockedGetMusicId3).toHaveBeenCalledWith(
      '17',
      'XXXTENTACION - Jocelyn Flores.ogg',
    );
    expect(result.title).toBe('Jocelyn Flores');
    expect(result.artist).toBe('XXXTENTACION');
    expect(result.year).toBe('2017');
    expect(result.size).toBe('12345');
    expect(result.artwork).toBe('data:image/jpeg;base64,abc');
    expect(result.album_img).toBe('data:image/jpeg;base64,abc');
    expect(result.artwork_w).toBe(600);
  });

  it('returns {} when Success nil serialises as empty array', async () => {
    mockedGetMusicId3.mockResolvedValueOnce({
      result: true,
      code: '200',
      message: 'success',
      data: [],
    });
    await expect(readTagsFromPath('x.mp3')).resolves.toEqual({});
  });

  it('throws on result:false so click path can toast', async () => {
    mockedGetMusicId3.mockResolvedValueOnce({
      result: false,
      code: '400',
      message: '路径不安全: escape',
      data: [],
    });
    await expect(readTagsFromPath('../etc/passwd')).rejects.toThrow(/路径不安全/);
  });

  it('throws when axios/getMusicId3 rejects', async () => {
    mockedGetMusicId3.mockRejectedValueOnce(new Error('network down'));
    await expect(readTagsFromPath('a.mp3')).rejects.toThrow(/network down/);
  });
});
