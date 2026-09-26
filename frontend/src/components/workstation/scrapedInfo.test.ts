import { describe, it, expect } from 'vitest';
import {
  scrapedMusicInfo,
  dirOf,
  groupSelectionsByDir,
} from './scrapedInfo';

describe('scrapedMusicInfo', () => {
  it("takes the candidate's genre instead of a hardcoded one", () => {
    const info = scrapedMusicInfo(
      { name: 'Song', artist: 'A', album: 'B', genre: 'Post-Rock' },
      'fallback',
    );
    expect(info.genre).toBe('Post-Rock');
  });

  it('omits genre entirely when the provider reports none', () => {
    // The regression this module exists to prevent: Kuwo / NetEase / QQ
    // report no genre, and the old literal wrote 流行 to every one of
    // their files.
    const info = scrapedMusicInfo({ name: 'Song', artist: 'A' }, 'fallback');
    expect('genre' in info).toBe(false);
  });

  it.each(['', '   '])('omits a blank genre (%j) rather than clearing the tag', (genre) => {
    const info = scrapedMusicInfo({ name: 'Song', genre }, 'fallback');
    expect('genre' in info).toBe(false);
  });

  it('omits every field the candidate did not supply', () => {
    // A present key with an empty value tells the handler "set this tag to
    // empty", which erases whatever the file already had.
    const info = scrapedMusicInfo({ name: 'Song' }, 'fallback');
    expect(Object.keys(info)).toEqual(['title']);
  });

  it('falls back to the query when the candidate has no usable name', () => {
    expect(scrapedMusicInfo({ name: '  ' }, '01 - Song').title).toBe('01 - Song');
    expect(scrapedMusicInfo({ name: 'Real Title' }, '01 - Song').title).toBe('Real Title');
  });

  it('accepts either lyric or lyrics', () => {
    expect(scrapedMusicInfo({ name: 'S', lyric: 'la' }, 'f').lyrics).toBe('la');
    expect(scrapedMusicInfo({ name: 'S', lyrics: 'la' }, 'f').lyrics).toBe('la');
  });

  it('trims values it keeps', () => {
    const info = scrapedMusicInfo({ name: ' S ', artist: ' A ' }, 'f');
    expect(info.title).toBe('S');
    expect(info.artist).toBe('A');
  });
});

describe('dirOf', () => {
  it.each([
    ['Artist/Album/01.mp3', 'Artist/Album'],
    ['01.mp3', ''],
  ])('%s -> %j', (input, want) => {
    expect(dirOf(input)).toBe(want);
  });
});

describe('groupSelectionsByDir', () => {
  it('emits one group per directory, with base names', () => {
    const rows = [
      { fullPath: 'A/1.mp3' },
      { fullPath: 'A/2.mp3' },
      { fullPath: 'B/3.mp3' },
    ];
    const groups = groupSelectionsByDir(rows, (r) => ({ title: r.fullPath }));

    expect([...groups.keys()].sort()).toEqual(['A', 'B']);
    expect(groups.get('A')?.map((s) => s.name)).toEqual(['1.mp3', '2.mp3']);
    expect(groups.get('B')?.map((s) => s.name)).toEqual(['3.mp3']);
  });

  it('keeps each row its own tags', () => {
    // The whole reason the endpoint grew a per-row override: a shared map
    // would write track A's title onto track B.
    const groups = groupSelectionsByDir(
      [{ fullPath: 'A/1.mp3' }, { fullPath: 'A/2.mp3' }],
      (r) => ({ title: `t-${r.fullPath}` }),
    );
    expect(groups.get('A')?.map((s) => s.music_info.title)).toEqual([
      't-A/1.mp3',
      't-A/2.mp3',
    ]);
  });
});
