// Specs for the track-length formatter.
//
// The formatter only started mattering when /api/search_music/ began
// sending a duration at all — until then the function existed and never
// ran, so nothing had ever checked what it did with a value. These pin
// the cases that actually reach it: the same track arrives as a number
// from one source and a numeric string from another, and a source with no
// length sends nothing rather than a zero.

import { describe, it, expect } from 'vitest';
import { formatDuration, formatDurationOrDash, toSeconds } from './duration';

describe('toSeconds', () => {
  it('accepts a number', () => {
    expect(toSeconds(119)).toBe(119);
  });

  it('accepts a numeric string', () => {
    // Several sources serialise the length as a string, so this is a
    // real shape rather than a hypothetical one.
    expect(toSeconds('119')).toBe(119);
    expect(toSeconds(' 119 ')).toBe(119);
  });

  it('treats a sub-second length as unknown', () => {
    // A mis-scaled milliseconds value arrives here as 0.119. Showing
    // 0:00 would be worse than showing nothing: it looks like a real
    // answer, and 0:00 is not one.
    expect(toSeconds(0.119)).toBeNull();
  });

  it('treats zero as unknown, not as a zero-length track', () => {
    // MusicBrainz search returns no length, and plugins encode that as 0.
    expect(toSeconds(0)).toBeNull();
    expect(toSeconds('0')).toBeNull();
  });

  it('rejects absent and unparseable values', () => {
    expect(toSeconds(undefined)).toBeNull();
    expect(toSeconds(null)).toBeNull();
    expect(toSeconds('')).toBeNull();
    expect(toSeconds('abc')).toBeNull();
    expect(toSeconds(NaN)).toBeNull();
    expect(toSeconds(-5)).toBeNull();
  });

  it('keeps a fractional length, for the formatter to round', () => {
    expect(toSeconds(119.7)).toBeCloseTo(119.7, 5);
  });
});

describe('formatDuration', () => {
  it('renders m:ss', () => {
    expect(formatDuration(119)).toBe('1:59');
    expect(formatDuration(9)).toBe('0:09');
    expect(formatDuration(60)).toBe('1:00');
    expect(formatDuration(61)).toBe('1:01');
    expect(formatDuration(599)).toBe('9:59');
  });

  it('renders h:mm:ss past an hour', () => {
    expect(formatDuration(3661)).toBe('1:01:01');
    expect(formatDuration(3600)).toBe('1:00:00');
  });

  it('rounds rather than truncates', () => {
    // The listener hears 2:00 of a 119.7s track. Truncating to 1:59
    // makes the displayed length disagree with the audio.
    expect(formatDuration(119.7)).toBe('2:00');
    expect(formatDuration(119.4)).toBe('1:59');
  });

  it('renders the same string and numeric input identically', () => {
    // The two shapes must not produce two different lengths for one
    // track, which is how a source gets blamed for a wrong time.
    expect(formatDuration('119')).toBe(formatDuration(119));
  });

  it('is empty when there is no length, so interpolation adds no separator', () => {
    expect(formatDuration(undefined)).toBe('');
    expect(formatDuration(null)).toBe('');
    expect(formatDuration(0)).toBe('');
    expect(formatDuration('nonsense')).toBe('');
  });

  it('composes cleanly into the artist · album · time line', () => {
    const song = { artist: 'Adele', album: '25', duration: 0 };
    const dur = formatDuration(song.duration);
    expect(
      `${song.artist}${song.album ? ` · ${song.album}` : ''}${dur ? ` · ${dur}` : ''}`,
    ).toBe('Adele · 25');
  });

  it('exposes a 1000x-wrong length rather than hiding it', () => {
    // NetEase sends 119133 for a 1:59 track. The plugin divides it; if
    // that ever regresses the UI must be able to show the damage, so this
    // documents the magnitude — 33 hours instead of under two minutes.
    expect(formatDuration(119133)).toBe('33:05:33');
    expect(formatDuration(119133 / 1000)).toBe('1:59');
  });
});

describe('formatDurationOrDash', () => {
  it('falls back to a dash so the column stays aligned', () => {
    expect(formatDurationOrDash(119)).toBe('1:59');
    expect(formatDurationOrDash(0)).toBe('—');
    expect(formatDurationOrDash(undefined)).toBe('—');
  });
});
