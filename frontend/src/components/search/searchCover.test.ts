// Specs for reading a cover off a search result.
//
// The bug these pin: both search surfaces read `song.cover`, which no
// tag-source plugin populates — they send `album_img`. Every result
// therefore rendered the letter placeholder while a working cover URL sat
// in the same JSON object. Nothing failed; the image was simply never
// looked at.

import { describe, it, expect } from 'vitest';
import { searchCoverSrc, searchCoverForTrack } from './searchCover';
import type { SearchResult } from '@/types';

function song(over: Partial<SearchResult> = {}): SearchResult {
  return { id: '1', source: 'netease', name: 'x', artist: 'y', ...over };
}

describe('searchCoverSrc', () => {
  it('reads album_img, which is what the plugins send', () => {
    // Captured from a live search: every tag source returns album_img and
    // no cover key at all.
    const s = song({
      album_img: 'http://p1.music.126.net/abc/109951168609412948.jpg',
    } as Partial<SearchResult>);
    expect(searchCoverSrc(s)).toBe('http://p1.music.126.net/abc/109951168609412948.jpg');
  });

  it('still reads cover, for a source that sends it', () => {
    const s = song({ cover: 'https://i.ytimg.com/vi/x/hq.jpg' });
    expect(searchCoverSrc(s)).toBe('https://i.ytimg.com/vi/x/hq.jpg');
  });

  it('prefers album_img when a result somehow carries both', () => {
    const s = song({
      album_img: 'http://a/1.jpg',
      cover: 'http://b/2.jpg',
    } as Partial<SearchResult>);
    expect(searchCoverSrc(s)).toBe('http://a/1.jpg');
  });

  it('falls through an empty album_img to cover', () => {
    // Kuwo sends album_img:"" on rows where its CDN field is unset, so a
    // blank first candidate must not shadow a usable second one.
    const s = song({ album_img: '', cover: 'http://b/2.jpg' } as Partial<SearchResult>);
    expect(searchCoverSrc(s)).toBe('http://b/2.jpg');
  });

  it('is undefined when there is no cover at all', () => {
    // MusicBrainz rows, and any source whose cover field came back
    // empty, must render the gradient placeholder rather than <img src="">.
    expect(searchCoverSrc(song())).toBeUndefined();
    expect(searchCoverSrc(song({ album_img: '' } as Partial<SearchResult>))).toBeUndefined();
    expect(searchCoverSrc(song({ album_img: '', cover: '' } as Partial<SearchResult>))).toBeUndefined();
  });

  it('rejects the empty-payload sentinel', () => {
    // mutagen emits a bare `data:image/jpeg,` for "no artwork embedded".
    // That is a valid-looking URL to the DOM and renders as a broken
    // image, so it must be treated as absent.
    const s = song({ album_img: 'data:image/jpeg,' } as Partial<SearchResult>);
    expect(searchCoverSrc(s)).toBeUndefined();
  });

  it('survives a nullish or malformed result', () => {
    expect(searchCoverSrc(null)).toBeUndefined();
    expect(searchCoverSrc(undefined)).toBeUndefined();
    // A non-string where a URL was expected must not become "undefined"
    // stringified into an <img src>.
    const s = song({ album_img: 42 } as unknown as Partial<SearchResult>);
    expect(searchCoverSrc(s)).toBeUndefined();
  });
});

describe('searchCoverForTrack', () => {
  it('defaults to empty string for PlayerTrack.cover', () => {
    // PlayerTrack.cover is typed `string`, not `string | undefined`, so
    // the player mapping needs a total function.
    expect(searchCoverForTrack(song({ album_img: 'http://a/1.jpg' } as Partial<SearchResult>))).toBe(
      'http://a/1.jpg',
    );
    expect(searchCoverForTrack(song())).toBe('');
    expect(searchCoverForTrack(null)).toBe('');
  });
});
