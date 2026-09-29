import { describe, it, expect } from 'vitest';
import {
  needsMusicInfoRefetch,
  stripHeavyMusicInfo,
} from '@/utils/persistMusicInfo';

describe('stripHeavyMusicInfo', () => {
  it('drops data-URI artwork and album_img but keeps text tags', () => {
    const out = stripHeavyMusicInfo({
      title: 'Hello',
      artist: 'World',
      artwork: 'data:image/jpeg;base64,' + 'A'.repeat(100),
      album_img: 'data:image/jpeg;base64,' + 'B'.repeat(100),
    });
    expect(out?.title).toBe('Hello');
    expect(out?.artist).toBe('World');
    expect(out?.artwork).toBeUndefined();
    expect(out?.album_img).toBeUndefined();
  });

  it('keeps short http cover URLs', () => {
    const out = stripHeavyMusicInfo({
      title: 'T',
      album_img: 'https://cdn.example/cover.jpg',
    });
    expect(out?.album_img).toBe('https://cdn.example/cover.jpg');
  });
});

describe('needsMusicInfoRefetch', () => {
  it('is true for empty / missing cache', () => {
    expect(needsMusicInfoRefetch(undefined)).toBe(true);
    expect(needsMusicInfoRefetch(null)).toBe(true);
    expect(needsMusicInfoRefetch({})).toBe(true);
  });

  it('is false once any text tag is present — the cover is not part of this cache', () => {
    // This is the load-bearing change. The refetch test used to require a
    // COVER, which was correct while the batch read asked for artwork
    // inline. It no longer does, so no row ever comes back with one, and a
    // cover-keyed test would mark every row stale — re-reading the whole
    // library on every single page load. Covers are fetched per visible row
    // from /api/album_cover/ and cached by the browser instead.
    expect(needsMusicInfoRefetch({ title: 'T' })).toBe(false);
    expect(needsMusicInfoRefetch({ artist: 'A' })).toBe(false);
    expect(needsMusicInfoRefetch({ title: 'T', year: '1999' })).toBe(false);
  });

  it('is false for a stripped-but-populated record', () => {
    expect(
      needsMusicInfoRefetch({
        title: 'T',
        artwork: 'data:image/jpeg;base64,abc',
      }),
    ).toBe(false);
    // A remote album_img alongside a tag. Note the title: a record whose
    // ONLY field is a cover is deliberately NOT considered populated — see
    // the next case.
    expect(
      needsMusicInfoRefetch({
        album: 'B',
        album_img: 'https://cdn.example/c.jpg',
      }),
    ).toBe(false);
  });

  it('is still true for a record whose only field is a cover', () => {
    // A cover says nothing about the track itself, so a row carrying only
    // one has no populated tag cache and still needs a read.
    expect(
      needsMusicInfoRefetch({ artwork: 'data:image/jpeg;base64,abc' }),
    ).toBe(true);
  });
});
