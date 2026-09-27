import { describe, it, expect } from 'vitest';
import {
  scrapedMusicInfo,
  candidateTagValue,
  fieldLabel,
  APPLYABLE_FIELDS,
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

describe('scrapedMusicInfo with an explicit field list', () => {
  const candidate = {
    name: 'Jocelyn Flores',
    artist: 'XXXTENTACION',
    album: '17',
    year: '2019',
    genre: 'Hip Hop',
    album_img: 'http://x/cover.jpg',
  };

  it('writes only the fields asked for', () => {
    // Taking one source's album and another's genre is a normal thing to
    // want; "apply everything" is why it could not be done before.
    const info = scrapedMusicInfo(candidate, 'f', ['album']);
    expect(info).toEqual({ album: '17' });
  });

  it('does not fall back to the query title when the title is not wanted', () => {
    // Asking for the genre must not also rewrite the title with the
    // filename — that is a change the user did not ask for.
    const info = scrapedMusicInfo(candidate, '01 - Jocelyn', ['genre']);
    expect(info).toEqual({ genre: 'Hip Hop' });
    expect('title' in info).toBe(false);
  });

  it('omits an asked-for field the candidate does not carry', () => {
    // "Apply the year" on a source with no year must leave the tag alone
    // rather than clearing it.
    const info = scrapedMusicInfo({ name: 'S' }, 'f', ['year', 'title']);
    expect(info).toEqual({ title: 'S' });
  });

  it('still reads lyrics from either spelling', () => {
    expect(scrapedMusicInfo({ name: 'S', lyric: 'la' }, 'f', ['lyrics'])).toEqual({
      lyrics: 'la',
    });
  });
});

describe('candidateTagValue', () => {
  it('reads one field, so a card can offer only what exists', () => {
    expect(candidateTagValue({ artist: 'A' }, 'artist')).toBe('A');
    expect(candidateTagValue({ artist: '  ' }, 'artist')).toBeUndefined();
  });

  it('has no value for the ids a file cannot store', () => {
    // artist_id / album_id are wire-only; APPLYABLE_FIELDS is what a tag
    // write can express, and this asserts the two stay in step.
    expect(APPLYABLE_FIELDS).not.toContain('artist_id' as never);
    expect(APPLYABLE_FIELDS).toContain('album_img');
  });
});

describe('fieldLabel', () => {
  it('names every applyable field', () => {
    for (const f of APPLYABLE_FIELDS) {
      expect(fieldLabel(f)).toBeTruthy();
    }
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
