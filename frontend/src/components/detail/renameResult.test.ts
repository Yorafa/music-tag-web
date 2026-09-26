// Specs for reading a rename out of an update_id3/ response, and for the
// store actions that follow it.
//
// A row's id in this app IS its path (`WorklistRow.id == fullPath`, and
// so is `LibraryRow.id`). If a save renames the file and the rows are not
// moved, the table keeps a path that no longer resolves: streaming it
// 404s, re-editing it fails, saving it again errors. The file has been
// dropped from the queue without anything looking broken — which is why
// this is worth pinning rather than trusting.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renamedPathFromUpdate, sidecarWarningsFromUpdate, baseNameOf, joinDir, renamedPathsFromUpdate } from './renameResult';
import { useWorklistStore } from '@/store/useWorklistStore';
import { useLibraryStore } from '@/store/useLibraryStore';
import { useDetailStore } from '@/store/useDetailStore';
import type { WorklistRow } from '@/types';

const OLD = 'Artist/Album/old.mp3';

/** The shape update_id3/ actually returns: the envelope, with `done`
 *  carrying `new_file_name` only when the call also renamed. */
function envelope(done: Array<Record<string, unknown>>) {
  return { result: true, code: '200', message: 'success', data: { done, skipped: [] } };
}

describe('renamedPathFromUpdate', () => {
  it('returns the new path when the handler renamed', () => {
    const res = envelope([
      { file_full_path: OLD, status: 'updated', new_file_name: 'new.mp3' },
    ]);
    expect(renamedPathFromUpdate(res, OLD)).toBe('Artist/Album/new.mp3');
  });

  it('returns null for a plain tag write', () => {
    // The overwhelmingly common case: the dialog pre-fills 文件名 with the
    // current name, the user edits tags, the handler resolves the target
    // to the same path and skips the rename.
    const res = envelope([{ file_full_path: OLD, status: 'updated' }]);
    expect(renamedPathFromUpdate(res, OLD)).toBeNull();
  });

  it('returns null rather than treating an empty name as a rename', () => {
    // A client reading `new_file_name: ""` as present would rewrite the
    // row to the parent directory.
    const res = envelope([
      { file_full_path: OLD, status: 'updated', new_file_name: '' },
    ]);
    expect(renamedPathFromUpdate(res, OLD)).toBeNull();
  });

  it('ignores other rows in a batch', () => {
    const res = envelope([
      { file_full_path: 'x/one.mp3', status: 'updated', new_file_name: 'r1.mp3' },
      { file_full_path: OLD, status: 'updated', new_file_name: 'r2.mp3' },
    ]);
    expect(renamedPathFromUpdate(res, OLD)).toBe('Artist/Album/r2.mp3');
  });

  it('handles a root-level file with no directory', () => {
    const res = envelope([
      { file_full_path: 'old.mp3', status: 'updated', new_file_name: 'new.mp3' },
    ]);
    expect(renamedPathFromUpdate(res, 'old.mp3')).toBe('new.mp3');
  });

  it('accepts an already-unwrapped data', () => {
    expect(
      renamedPathFromUpdate(
        { done: [{ file_full_path: OLD, new_file_name: 'new.mp3' }] },
        OLD,
      ),
    ).toBe('Artist/Album/new.mp3');
  });

  it('returns null for junk rather than throwing', () => {
    for (const res of [null, undefined, 'nope', 42, {}, { data: null }]) {
      expect(renamedPathFromUpdate(res, OLD)).toBeNull();
    }
  });
});

describe('path helpers', () => {
  it('baseNameOf strips the directory', () => {
    expect(baseNameOf('a/b/c.mp3')).toBe('c.mp3');
    expect(baseNameOf('c.mp3')).toBe('c.mp3');
  });

  it('joinDir keeps the parent and swaps the base', () => {
    expect(joinDir('a/b/c.mp3', 'd.mp3')).toBe('a/b/d.mp3');
    expect(joinDir('c.mp3', 'd.mp3')).toBe('d.mp3');
  });

  it('joinDir is what renamedPathFromUpdate uses', () => {
    // Guards the two against drifting apart: the component derives
    // fileName from the returned path with baseNameOf, so a mismatch
    // would store a fileName that disagrees with its own fullPath.
    const res = envelope([
      { file_full_path: OLD, status: 'updated', new_file_name: 'new.mp3' },
    ]);
    const p = renamedPathFromUpdate(res, OLD)!;
    expect(baseNameOf(p)).toBe(joinDir(OLD, baseNameOf(p)).split('/').pop());
  });
});

function row(over: Partial<WorklistRow> = {}): WorklistRow {
  return {
    id: OLD,
    fullPath: OLD,
    fileName: 'old.mp3',
    status: 'pending',
    ...over,
  } as WorklistRow;
}

describe('worklist renameRow', () => {
  beforeEach(() => {
    useWorklistStore.setState({ rows: [row()], selectedIds: [] });
  });

  it('moves id, fullPath and fileName together', () => {
    useWorklistStore.getState().renameRow(OLD, 'Artist/Album/new.mp3', 'new.mp3');
    const r = useWorklistStore.getState().rows[0];
    // All three move: a row whose id and fullPath disagree cannot be
    // found by setMusicInfo, streamed, or saved again.
    expect(r.id).toBe('Artist/Album/new.mp3');
    expect(r.fullPath).toBe('Artist/Album/new.mp3');
    expect(r.fileName).toBe('new.mp3');
  });

  it('carries the selection across', () => {
    // selectedIds is keyed by the same path; leaving it behind would
    // desync the checkbox from the row it was ticking.
    useWorklistStore.setState({ selectedIds: [OLD] });
    useWorklistStore.getState().renameRow(OLD, 'Artist/Album/new.mp3', 'new.mp3');
    expect(useWorklistStore.getState().selectedIds).toEqual(['Artist/Album/new.mp3']);
  });

  it('preserves the tag cache and status', () => {
    useWorklistStore.getState().setMusicInfo(OLD, { title: 'T', artist: 'A' });
    useWorklistStore.getState().renameRow(OLD, 'Artist/Album/new.mp3', 'new.mp3');
    const r = useWorklistStore.getState().rows[0];
    expect(r.musicInfo?.title).toBe('T');
    expect(r.status).toBe('pending');
  });

  it('ignores an unknown row', () => {
    useWorklistStore.getState().renameRow('nope.mp3', 'x.mp3', 'x.mp3');
    expect(useWorklistStore.getState().rows[0].fullPath).toBe(OLD);
  });

  it('refuses to merge into an existing row', () => {
    // Unreachable while the handler refuses colliding renames, but two
    // rows sharing an id would silently drop a file from the queue.
    useWorklistStore.setState({
      rows: [row(), row({ id: 'Artist/Album/new.mp3', fullPath: 'Artist/Album/new.mp3', fileName: 'new.mp3' })],
    });
    useWorklistStore.getState().renameRow(OLD, 'Artist/Album/new.mp3', 'new.mp3');
    const paths = useWorklistStore.getState().rows.map((r) => r.fullPath);
    expect(paths).toContain(OLD);
    expect(paths).toHaveLength(2);
  });
});

describe('library renameRow', () => {
  beforeEach(() => {
    useLibraryStore.setState({
      rows: [{ id: OLD, fullPath: OLD, fileName: 'old.mp3' }],
    });
  });

  it('moves the row', () => {
    useLibraryStore.getState().renameRow(OLD, 'Artist/Album/new.mp3', 'new.mp3');
    const r = useLibraryStore.getState().rows[0];
    expect(r.id).toBe('Artist/Album/new.mp3');
    expect(r.fileName).toBe('new.mp3');
  });

  it('ignores an unknown row', () => {
    useLibraryStore.getState().renameRow('nope.mp3', 'x.mp3', 'x.mp3');
    expect(useLibraryStore.getState().rows[0].fullPath).toBe(OLD);
  });
});

describe('detail renameTarget', () => {
  beforeEach(() => {
    useDetailStore.setState({ target: { fullPath: OLD, fileName: 'old.mp3' } });
  });

  it('repoints the open dialog', () => {
    // The dialog's target is a snapshot, so a rename made inside it would
    // otherwise leave every subsequent save pointing at a dead path.
    useDetailStore.getState().renameTarget(OLD, 'Artist/Album/new.mp3', 'new.mp3');
    const t = useDetailStore.getState().target!;
    expect(t.fullPath).toBe('Artist/Album/new.mp3');
    expect(t.fileName).toBe('new.mp3');
  });

  it('leaves a different open target alone', () => {
    useDetailStore.getState().renameTarget('other.mp3', 'x.mp3', 'x.mp3');
    expect(useDetailStore.getState().target!.fullPath).toBe(OLD);
  });

  it('is a no-op when nothing is open', () => {
    useDetailStore.setState({ target: null });
    expect(() =>
      useDetailStore.getState().renameTarget(OLD, 'x.mp3', 'x.mp3'),
    ).not.toThrow();
    expect(useDetailStore.getState().target).toBeNull();
  });
});

describe('the stores and the response agree', () => {
  it('a reported rename moves every store to the same path', () => {
    // End-to-end over the pieces: response → path → three stores. The
    // failure this prevents is a table row, a dialog and a cache all
    // disagreeing about where the file lives.
    vi.useRealTimers();
    useWorklistStore.setState({ rows: [row()], selectedIds: [OLD] });
    useLibraryStore.setState({ rows: [{ id: OLD, fullPath: OLD, fileName: 'old.mp3' }] });
    useDetailStore.setState({ target: { fullPath: OLD, fileName: 'old.mp3' } });

    const newPath = renamedPathFromUpdate(
      envelope([
        { file_full_path: OLD, status: 'updated', new_file_name: 'new.mp3' },
      ]),
      OLD,
    )!;
    const newName = baseNameOf(newPath);

    useWorklistStore.getState().renameRow(OLD, newPath, newName);
    useLibraryStore.getState().renameRow(OLD, newPath, newName);
    useDetailStore.getState().renameTarget(OLD, newPath, newName);

    expect(useWorklistStore.getState().rows[0].fullPath).toBe(newPath);
    expect(useWorklistStore.getState().selectedIds).toEqual([newPath]);
    expect(useLibraryStore.getState().rows[0].fullPath).toBe(newPath);
    expect(useDetailStore.getState().target!.fullPath).toBe(newPath);
  });
});

describe('sidecarWarningsFromUpdate', () => {
  /** The handler always sends `warnings`, empty on a clean save.
   *  Typed `unknown[]` on purpose: one spec feeds it shapes the handler
   *  cannot produce, which is the point. */
  function withWarnings(warnings: unknown[]) {
    return {
      result: true,
      code: '200',
      message: 'success',
      data: { done: [{ file_full_path: OLD, status: 'updated' }], skipped: [], warnings },
    };
  }

  it('is empty on a clean save', () => {
    // The overwhelmingly common case. A toast here would train the user
    // to dismiss warnings without reading them, which is exactly when a
    // real one would be missed.
    expect(sidecarWarningsFromUpdate(withWarnings([]))).toEqual([]);
  });

  it('is empty when the handler omits the key entirely', () => {
    // An older gateway, or a call that took a different path. Absent must
    // not become one empty-string toast.
    const res = { result: true, data: { done: [], skipped: [] } };
    expect(sidecarWarningsFromUpdate(res)).toEqual([]);
  });

  it('names the sidecar, the name it failed to reach and the reason', () => {
    const res = withWarnings([
      {
        file_full_path: OLD,
        sidecar: 'old.lrc',
        target: 'new.lrc',
        reason: 'rename /a/old.lrc -> /a/new.lrc: permission denied',
      },
    ]);
    expect(sidecarWarningsFromUpdate(res)).toEqual([
      'old.lrc 未能改名为 new.lrc：rename /a/old.lrc -> /a/new.lrc: permission denied',
    ]);
  });

  it('reads an already-unwrapped data as well as the envelope', () => {
    // Callers sit on both sides of the envelope boundary in this
    // codebase, and the rename reader already tolerates both.
    const data = {
      done: [],
      skipped: [],
      warnings: [{ sidecar: 'old.lrc', target: 'new.lrc', reason: 'busy' }],
    };
    expect(sidecarWarningsFromUpdate(data)).toEqual(['old.lrc 未能改名为 new.lrc：busy']);
  });

  it('survives a malformed warning rather than throwing', () => {
    // A bad shape must not blank the dialog: the save itself succeeded
    // and the user still needs their tags.
    expect(sidecarWarningsFromUpdate(withWarnings([{}, null, 7]))).toEqual([
      'sidecar 未能跟随重命名：未知原因',
      'sidecar 未能跟随重命名：未知原因',
      'sidecar 未能跟随重命名：未知原因',
    ]);
  });

  it('reports every warning, not just the first', () => {
    // An album tidy can strand a .lrc and two covers at once; showing
    // only the first would send the user looking for a problem that is
    // not there and missing one that is.
    const res = withWarnings([
      { sidecar: 'song.lrc', target: 'song.lrc', reason: 'denied' },
      { sidecar: 'cover-A.jpg', target: 'cover-A.jpg', reason: 'denied' },
    ]);
    expect(sidecarWarningsFromUpdate(res)).toHaveLength(2);
  });
});

describe('renamedPathsFromUpdate', () => {
  it('collects every moved row, not just the first', () => {
    const res = {
      data: {
        done: [
          { file_full_path: 'Album/01.mp3', new_file_name: 'A - One.mp3' },
          { file_full_path: 'Other/02.mp3', new_file_name: 'B - Two.mp3' },
          { file_full_path: 'Album/03.mp3' },
        ],
      },
    };
    const moved = renamedPathsFromUpdate(res);
    expect(moved.size).toBe(2);
    expect(moved.get('Album/01.mp3')).toBe('Album/A - One.mp3');
    // A row that did not move is absent, not mapped to itself: the caller
    // uses absence to decide whether to touch the store at all.
    expect(moved.has('Album/03.mp3')).toBe(false);
  });

  it('reads an already-unwrapped report', () => {
    const moved = renamedPathsFromUpdate({
      done: [{ file_full_path: 'a.mp3', new_file_name: 'b.mp3' }],
    });
    expect(moved.get('a.mp3')).toBe('b.mp3');
  });

  it('returns an empty map for junk rather than throwing', () => {
    for (const junk of [null, undefined, 42, 'nope', {}]) {
      expect(renamedPathsFromUpdate(junk).size).toBe(0);
    }
  });
});
