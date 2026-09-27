import { describe, it, expect } from 'vitest';
import { selectionKind } from './expandDirs';

// The picker can now tick an individual audio file, and a selection entry
// therefore is not necessarily a directory. Getting this wrong is silent:
// expandDirs calls /api/file_list/ on the entry, a file path lists nothing,
// and the user watches a checked file add zero rows.
describe('selectionKind', () => {
  it('treats an audio file as a file', () => {
    expect(selectionKind('loose track.flac')).toBe('file');
    expect(selectionKind('华语/摇滚/ao zhen.flac')).toBe('file');
  });

  it('accepts every extension the worklist collects', () => {
    for (const n of ['a.flac', 'a.APE', 'a.mp3', 'a.m4a', 'a.opus', 'a.dsf', 'a.mp4']) {
      expect(selectionKind(n)).toBe('file');
    }
  });

  it('treats a directory as a directory', () => {
    expect(selectionKind('')).toBe('dir');
    expect(selectionKind('Miles Davis')).toBe('dir');
    expect(selectionKind('华语/流行')).toBe('dir');
  });

  it('looks only at the last segment', () => {
    // 'releases.flac' is a directory in any sane library, and the picker's
    // own FolderRow/FileRow split is what distinguishes the two — the name
    // alone cannot, which is why the drawer renders them differently even
    // though this function can only guess from the string.
    expect(selectionKind('Bootleg.flac/')).toBe('dir');
  });
});
