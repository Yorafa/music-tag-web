import { describe, it, expect } from 'vitest';
import { parseLyrics, activeLyricIndex } from './lyrics';

describe('parseLyrics', () => {
  it('marks a plain-text body as unsynced and keeps every non-blank line', () => {
    const { lines, synced } = parseLyrics('第一行\n第二行\n\n第三行');
    expect(synced).toBe(false);
    expect(lines.map((l) => l.text)).toEqual(['第一行', '第二行', '第三行']);
    expect(lines.every((l) => l.time === null)).toBe(true);
  });

  it('parses LRC time tags into seconds and flags the body as synced', () => {
    const { lines, synced } = parseLyrics('[00:12.50]hello\n[01:05.00]world');
    expect(synced).toBe(true);
    expect(lines).toEqual([
      { time: 12.5, text: 'hello' },
      { time: 65, text: 'world' },
    ]);
  });

  it('expands a line carrying multiple timestamps into one entry per time', () => {
    const { lines } = parseLyrics('[00:10.00][01:30.00]repeated hook');
    expect(lines).toEqual([
      { time: 10, text: 'repeated hook' },
      { time: 90, text: 'repeated hook' },
    ]);
  });

  it('drops metadata-only tags but keeps timed lines', () => {
    const { lines, synced } = parseLyrics('[ti:Song]\n[ar:Artist]\n[by:tagger]\n[00:01.00]lyric');
    expect(synced).toBe(true);
    expect(lines).toEqual([{ time: 1, text: 'lyric' }]);
  });

  it('sorts synced lines by time even when the source is out of order', () => {
    const { lines } = parseLyrics('[01:00.00]late\n[00:30.00]early');
    expect(lines.map((l) => l.text)).toEqual(['early', 'late']);
  });

  it('accepts a two-digit fraction and a comma-free colon fraction', () => {
    const { lines } = parseLyrics('[00:03.4]a\n[00:05:20]b');
    expect(lines[0].time).toBeCloseTo(3.4);
    expect(lines[1].time).toBeCloseTo(5.2);
  });

  it('returns an empty line set for an empty body', () => {
    expect(parseLyrics('')).toEqual({ lines: [], synced: false });
    expect(parseLyrics('   \n\n')).toEqual({ lines: [], synced: false });
  });
});

describe('activeLyricIndex', () => {
  const lines = parseLyrics('[00:00.00]a\n[00:10.00]b\n[00:20.00]c').lines;

  it('is -1 before the first timestamp', () => {
    // first line is at t=0, so anything below 0 is pre-roll
    expect(activeLyricIndex(lines, -1)).toBe(-1);
  });

  it('returns the last line whose time is <= current time', () => {
    expect(activeLyricIndex(lines, 0)).toBe(0);
    expect(activeLyricIndex(lines, 9.9)).toBe(0);
    expect(activeLyricIndex(lines, 10)).toBe(1);
    expect(activeLyricIndex(lines, 25)).toBe(2);
  });

  it('skips plain lines with no timestamp when scanning', () => {
    const mixed = parseLyrics('intro line\n[00:05.00]sung').lines;
    // the plain line sorts first (time null → 0 in sort key) but is skipped
    expect(activeLyricIndex(mixed, 6)).toBe(mixed.findIndex((l) => l.text === 'sung'));
  });
});
