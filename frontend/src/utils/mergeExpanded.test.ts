import { describe, it, expect } from 'vitest';
import { mergeExpandedDirs } from './mergeExpanded';

interface E {
  fullPath: string;
  sourceDir: string;
}

const f = (fullPath: string, sourceDir = ''): E => ({ fullPath, sourceDir });

describe('mergeExpandedDirs', () => {
  // The bug this exists for. A youtube download lands in the library root; the
  // root was already added once; re-adding it matched old files and the whole
  // batch — new file included — was dropped. Reproduced in a browser before
  // the fix: 4 rows queued, a 5th file downloaded into the same directory,
  // re-add → still 4 rows.
  it('keeps a newly downloaded file in an already-added directory', () => {
    const queued = [
      'XuLiangMusic - 徐良 - 後會未期.ogg',
      '未知/ILLIT - 10 Minutes.ogg',
      'XXXTENTACION/17/Jocelyn Flores.ogg',
    ];
    const onDisk = [
      ...queued,
      'ZZNewDownload - 新下载的歌.ogg', // <-- the new download
    ].map((p) => f(p, '')); // all from the root, one sourceDir

    const r = mergeExpandedDirs(onDisk, queued);

    expect(r.fresh.map((x) => x.fullPath)).toEqual(['ZZNewDownload - 新下载的歌.ogg']);
    expect(r.addedDirs).toBe(1);
    expect(r.skippedDirs).toBe(0);
  });

  // The no-new-files case must still read as "skipped", so the user gets the
  // same "跳过 1 个重复" they got before rather than a silent success.
  it('reports an unchanged directory as skipped, adding nothing', () => {
    const known = ['a/one.ogg', 'a/two.ogg'];
    const r = mergeExpandedDirs(known.map((p) => f(p, 'a')), known);

    expect(r.fresh).toEqual([]);
    expect(r.addedDirs).toBe(0);
    expect(r.skippedDirs).toBe(1);
    expect(r.addedDirNames).toEqual([]);
  });

  it('counts a directory as added only when it contributed something new', () => {
    const r = mergeExpandedDirs(
      [f('old/a.ogg', 'old'), f('new/b.ogg', 'new'), f('old/c.ogg', 'old')],
      // Both of `old`'s files are already held, so it contributes nothing.
      ['old/a.ogg', 'old/c.ogg'],
    );
    // `old` was touched but every one of its files is known → skipped.
    expect(r.addedDirNames).toEqual(['new']);
    expect(r.addedDirs).toBe(1);
    expect(r.skippedDirs).toBe(1);
  });

  // Two ticked directories can share a child; without this the batch itself
  // would produce a duplicate row, which is the one duplicate source that
  // has nothing to do with what the store already held.
  it('collapses a path reachable from two source directories', () => {
    const r = mergeExpandedDirs(
      [f('shared/x.ogg', 'a'), f('shared/x.ogg', 'b')],
      [],
    );
    expect(r.fresh).toHaveLength(1);
    expect(r.addedDirNames).toEqual(['a']);
    expect(r.skippedDirs).toBe(1);
  });

  // One ticked directory contributing five new files is still ONE added
  // directory. The notice says 目录, and the user ticked one box — counting
  // files here would report "已收录 5 个新增目录" for a single selection.
  it('counts a directory once no matter how many files it contributes', () => {
    const r = mergeExpandedDirs(
      ['a/1.ogg', 'a/2.ogg', 'a/3.ogg', 'a/4.ogg', 'a/5.ogg'].map((p) => f(p, 'a')),
      [],
    );
    expect(r.fresh).toHaveLength(5);
    expect(r.addedDirNames).toEqual(['a']);
    expect(r.addedDirs).toBe(1);
    expect(r.skippedDirs).toBe(0);
  });

  it('treats an empty expansion as nothing added and nothing skipped', () => {
    const r = mergeExpandedDirs([], ['a.ogg']);
    expect(r).toEqual({ fresh: [], addedDirs: 0, skippedDirs: 0, addedDirNames: [] });
  });

  it('never returns a file the store already holds, even when it is the only one', () => {
    const r = mergeExpandedDirs([f('a.ogg', 'a')], ['a.ogg']);
    expect(r.fresh).toEqual([]);
    expect(r.skippedDirs).toBe(1);
  });
});
