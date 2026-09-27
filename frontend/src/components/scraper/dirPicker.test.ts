import { describe, it, expect } from 'vitest';
import {
  appendToPath,
  directAudioNames,
  folderRowsOf,
  isAudioName,
  parentOf,
  wholeDirLabel,
  wholeDirSelectable,
} from '@/components/scraper/dirPicker';
import type { FileNode } from '@/types';

/** One /api/file_list/ response: a single root node wrapping children.
 *  Mirrors handler/file.go — the client always reads `data[0].children`. */
function listing(children: Array<Partial<FileNode>>): FileNode[] {
  return [
    {
      id: 0,
      name: 'music',
      title: 'music',
      icon: 'icon-folder',
      state: '',
      children: children as FileNode[],
      size: 0,
      update_time: '',
      expanded: true,
    } as FileNode,
  ];
}

const folder = (name: string) => ({ name, icon: 'icon-folder' });
const audio = (name: string) => ({ name, icon: 'icon-audio' });

describe('appendToPath / parentOf', () => {
  it('never produces a double slash', () => {
    expect(appendToPath('', 'foo')).toBe('foo');
    expect(appendToPath('foo', 'bar')).toBe('foo/bar');
  });

  it('strips one segment', () => {
    expect(parentOf('foo/bar/baz')).toBe('foo/bar');
    expect(parentOf('foo')).toBe('');
    expect(parentOf('')).toBe('');
  });
});

describe('isAudioName', () => {
  it('accepts every extension the scraper collects', () => {
    // The point of importing AUDIO_EXTS rather than re-listing: a format
    // the worklist can hold must be selectable here too.
    for (const n of ['a.flac', 'a.APE', 'a.mp3', 'a.m4a', 'a.opus', 'a.dsf']) {
      expect(isAudioName(n)).toBe(true);
    }
  });

  it('rejects sidecars and images', () => {
    for (const n of ['a.lrc', 'cover.jpg', 'notes.txt', 'noext']) {
      expect(isAudioName(n)).toBe(false);
    }
  });
});

describe('folderRowsOf', () => {
  it('lists only directories, sorted, with the current dir folded in', () => {
    const rows = folderRowsOf(
      listing([folder('Zebra'), audio('x.mp3'), folder('Alpha')]),
      '华语',
    );
    expect(rows.map((r) => r.name)).toEqual(['Alpha', 'Zebra']);
    // Without the prefix these would be bare names and expandDirs
    // would fail the /api/file_list/ lookup.
    expect(rows.map((r) => r.relPath)).toEqual(['华语/Alpha', '华语/Zebra']);
  });

  it('keeps CJK directory names (order not asserted)', () => {
    // Deliberately order-insensitive: rows are sorted with localeCompare,
    // so CJK ordering follows the runtime's collation (pinyin on a full
    // ICU build, codepoint without one) and a fixed expectation would be
    // a flaky test that says nothing about the code. Both sides are
    // sorted with the SAME comparator so the check is about membership.
    const byName = (a: string, b: string) => a.localeCompare(b);
    const rows = folderRowsOf(listing([folder('摇滚'), folder('流行')]), '');
    expect(rows.map((r) => r.name).sort(byName)).toEqual(
      ['流行', '摇滚'].sort(byName),
    );
  });

  it('returns [] for an empty or missing listing', () => {
    expect(folderRowsOf(listing([]), '')).toEqual([]);
    expect(folderRowsOf(undefined, '')).toEqual([]);
  });
});

describe('directAudioNames', () => {
  // The bug: audio that lives in no artist/album folder — i.e. sitting
  // directly in /music — was unreachable, because the drawer only ever
  // listed subdirectories and never the directory you were standing in.
  it('finds audio in the browsed dir itself, ignoring subdirs', () => {
    const byName = (a: string, b: string) => a.localeCompare(b);
    const names = directAudioNames(
      listing([folder('Aphex Twin'), audio('loose track.flac'), audio('b.mp3')]),
    );
    expect([...names].sort(byName)).toEqual(
      ['b.mp3', 'loose track.flac'].sort(byName),
    );
  });

  it('does not descend into subdirectories', () => {
    expect(directAudioNames(listing([folder('Miles Davis')]))).toEqual([]);
  });

  it('excludes a DIRECTORY whose name ends in an audio extension', () => {
    // The `icon !== 'icon-folder'` test is load-bearing, not belt-and-
    // braces: a library folder named after the track it holds
    // ("Bootleg.flac/") passes isAudioName, and treating it as a file
    // would both mislabel it and enqueue a path that cannot resolve.
    expect(directAudioNames(listing([folder('Bootleg.flac')]))).toEqual([]);
    expect(wholeDirSelectable(listing([folder('Bootleg.flac')]), '')).toBe(true);
  });
});

describe('wholeDirLabel', () => {
  it('names the root via the /music alias, not an empty string', () => {
    // '' would render as a nameless row and an empty chip.
    expect(wholeDirLabel('')).toBe('整个 /music 目录（含本层文件）');
  });

  it('names a nested directory by its full path', () => {
    expect(wholeDirLabel('华语/摇滚')).toBe(
      '整个 /music/华语/摇滚 目录（含本层文件）',
    );
  });

  it('says it includes this level — it expands the whole subtree', () => {
    expect(wholeDirLabel('')).toContain('含本层文件');
  });
});

describe('wholeDirSelectable', () => {
  it('is true for a dir holding only loose audio (the reported case)', () => {
    expect(wholeDirSelectable(listing([audio('a.flac')]), '')).toBe(true);
  });

  it('is true for a dir with subdirectories', () => {
    expect(wholeDirSelectable(listing([folder('x')]), '')).toBe(true);
  });

  it('is false for an empty dir — an inert checkbox is worse than none', () => {
    expect(wholeDirSelectable(listing([]), '')).toBe(false);
    expect(wholeDirSelectable(undefined, '')).toBe(false);
  });

  it('is false for a dir holding only non-audio files', () => {
    expect(
      wholeDirSelectable(listing([{ name: 'cover.jpg', icon: 'icon-image' }]), ''),
    ).toBe(false);
  });
});
