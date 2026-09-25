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

  it('is true when title/artist exist but cover was stripped', () => {
    expect(
      needsMusicInfoRefetch({ title: 'T', artist: 'A' }),
    ).toBe(true);
  });

  it('is false when a usable cover is present', () => {
    expect(
      needsMusicInfoRefetch({
        title: 'T',
        artwork: 'data:image/jpeg;base64,abc',
      }),
    ).toBe(false);
    expect(
      needsMusicInfoRefetch({
        album_img: 'https://cdn.example/c.jpg',
      }),
    ).toBe(false);
  });

  it('is true for empty data-URI sentinel', () => {
    expect(
      needsMusicInfoRefetch({ artwork: 'data:image/jpeg;base64,' }),
    ).toBe(true);
  });
});
