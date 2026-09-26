// Store-level tests for the duplicate plane: verdicts, the 「只看重复」
// filter, and the invariant that 全选 agrees with what the table shows.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  useWorklistStore,
  rowMatchesFilter,
} from '@/store/useWorklistStore';
import type { WorklistRow } from '@/types';

vi.mock('@/api/client', () => ({
  getFileList: vi.fn(async () => ({ result: true, code: '200', data: [], message: 'success' })),
}));

vi.mock('@/lib/id3Reader', () => ({
  readTagsFromPath: vi.fn(async () => ({})),
}));

function seed(paths: string[]) {
  useWorklistStore.setState({
    rows: paths.map((p) => ({
      id: p,
      fullPath: p,
      fileName: p.split('/').pop() ?? p,
      status: 'pending' as const,
    })),
    selectedIds: [],
    filter: 'all',
  });
}

const verdict = (v: WorklistRow['duplicate']) => ({ fileFullPath: 'a.mp3', ...v } as never);

beforeEach(() => {
  localStorage.clear();
  seed([]);
});

describe('setDuplicates', () => {
  it('records a verdict on the matching row', () => {
    seed(['a.mp3', 'b.mp3']);
    useWorklistStore.getState().setDuplicates([
      verdict({ verdict: 'duplicate', duplicatePath: 'b.mp3' }),
    ]);
    const rows = useWorklistStore.getState().rows;
    expect(rows[0].duplicate?.verdict).toBe('duplicate');
    expect(rows[1].duplicate).toBeUndefined();
  });

  it('does not resurrect a row for an unknown path', () => {
    // The server echoes the paths it was handed, so a stale id (a file
    // renamed mid-check) is expected. Inventing a row for it would put a
    // badge on a file that isn't in the worklist.
    seed(['a.mp3']);
    useWorklistStore.getState().setDuplicates([
      { fileFullPath: 'gone.mp3', verdict: 'duplicate' } as never,
    ]);
    expect(useWorklistStore.getState().rows).toHaveLength(1);
  });

  // A partial response must update exactly the rows it spoke about.
  //
  // c.mp3 starts with a verdict ON PURPOSE. Asserting only that it ends up
  // undefined would pass just as happily against an implementation that
  // wipes every row it was not told about — the two behaviours are
  // indistinguishable unless the row already had something to lose.
  it('leaves unchecked rows alone', () => {
    seed(['a.mp3', 'b.mp3', 'c.mp3']);
    useWorklistStore.getState().setDuplicates([
      { fileFullPath: 'c.mp3', verdict: 'unique' } as never,
    ]);
    useWorklistStore.getState().setDuplicates([
      { fileFullPath: 'a.mp3', verdict: 'duplicate', duplicatePath: 'b.mp3' } as never,
      { fileFullPath: 'b.mp3', verdict: 'skipped' } as never,
    ]);
    const rows = useWorklistStore.getState().rows;
    expect(rows[2].duplicate?.verdict).toBe('unique');
  });

  it('normalises an unrecognised verdict to error', () => {
    // A future server verdict must not reach the filter's switch as an
    // unhandled string.
    seed(['a.mp3']);
    useWorklistStore.getState().setDuplicates([
      { fileFullPath: 'a.mp3', verdict: 'quantum' } as never,
    ]);
    expect(useWorklistStore.getState().rows[0].duplicate?.verdict).toBe('error');
  });

  it('does not persist the verdict to localStorage', () => {
    // A verdict is a point-in-time fact about a file that may since have
    // been replaced; a badge restored from storage asserts something
    // nobody re-checked.
    seed(['a.mp3']);
    useWorklistStore.getState().setDuplicates([
      { fileFullPath: 'a.mp3', verdict: 'duplicate', duplicatePath: 'z.mp3' } as never,
    ]);
    const raw = localStorage.getItem('worklist.v1') ?? '';
    expect(raw).not.toContain('duplicate');
  });
});

describe('clearDuplicates', () => {
  it('sweeps every verdict', () => {
    seed(['a.mp3', 'b.mp3']);
    useWorklistStore.getState().setDuplicates([
      { fileFullPath: 'a.mp3', verdict: 'duplicate' } as never,
      { fileFullPath: 'b.mp3', verdict: 'unique' } as never,
    ]);
    useWorklistStore.getState().clearDuplicates();
    expect(useWorklistStore.getState().rows.every((r) => r.duplicate === undefined)).toBe(true);
  });
});

describe('rowMatchesFilter / the duplicate filter', () => {
  it('shows only content-level duplicates', () => {
    const r = (v: WorklistRow['duplicate']): WorklistRow => ({
      id: 'x', fullPath: 'x', fileName: 'x', status: 'pending', duplicate: v,
    });
    expect(rowMatchesFilter(r({ verdict: 'duplicate' }), 'duplicate')).toBe(true);
    expect(rowMatchesFilter(r({ verdict: 'likely_duplicate' }), 'duplicate')).toBe(false);
    expect(rowMatchesFilter(r({ verdict: 'unique' }), 'duplicate')).toBe(false);
    expect(rowMatchesFilter(r(undefined), 'duplicate')).toBe(false);
  });

  // Duplicate is its own axis, not a scrape status: a file can be both
  // 已刮削 and 重复, and 「只看重复」 must find it either way.
  it('finds a row that is also scraped', () => {
    const r: WorklistRow = {
      id: 'x', fullPath: 'x', fileName: 'x', status: 'scraped',
      duplicate: { verdict: 'duplicate' },
    };
    expect(rowMatchesFilter(r, 'duplicate')).toBe(true);
    expect(rowMatchesFilter(r, 'scraped')).toBe(true);
  });

  // The reason rowMatchesFilter exists as a shared helper: when the table
  // and selectAll each had their own filter, 全选 under 「只看重复」 picked
  // rows the user could not see.
  it('keeps 全选 in agreement with the visible set', () => {
    seed(['a.mp3', 'b.mp3', 'c.mp3']);
    useWorklistStore.getState().setDuplicates([
      { fileFullPath: 'a.mp3', verdict: 'duplicate', duplicatePath: 'b.mp3' } as never,
      { fileFullPath: 'b.mp3', verdict: 'unique' } as never,
    ]);
    useWorklistStore.getState().setFilter('duplicate');

    const visible = useWorklistStore
      .getState()
      .rows.filter((r) => rowMatchesFilter(r, 'duplicate'));
    const n = useWorklistStore.getState().selectAll();

    expect(n).toBe(visible.length);
    expect(useWorklistStore.getState().selectedIds).toEqual(visible.map((r) => r.id));
    expect(useWorklistStore.getState().selectedIds).toEqual(['a.mp3']);
  });
});
