// Unit tests for the flattening the virtualizer consumes.
//
// The property that matters most here is `indexInGroup`. The table used to
// render `index + 1` from its own map over `group.items`, so the `#` column
// showed a position within the group. Once rows are windowed, the array
// index IS a scroll position — reading a track number off it would renumber
// every visible row as the user scrolls. These tests pin that the number
// travels with the row instead.

import { describe, it, expect } from 'vitest';
import { flattenTableItems, type TableGroup } from './tableItems';
import type { WorklistRow } from '@/types';

function row(path: string, album = 'A1', artist = 'X'): WorklistRow {
  return {
    id: path,
    fullPath: path,
    fileName: path.split('/').pop()!,
    status: 'pending',
    musicInfo: { title: path, album, artist },
  };
}

function group(key: string, items: WorklistRow[]): TableGroup {
  return { key, label: key, items };
}

describe('flattenTableItems — grouping: none', () => {
  it('emits no header entries', () => {
    const out = flattenTableItems([group('__all__', [row('a.flac'), row('b.flac')])], {
      grouping: 'none',
      collapsedGroups: new Set(),
    });
    expect(out.map((i) => i.kind)).toEqual(['row', 'row']);
  });

  it('numbers rows from 1', () => {
    const out = flattenTableItems([group('__all__', [row('a.flac'), row('b.flac')])], {
      grouping: 'none',
      collapsedGroups: new Set(),
    });
    expect(out.map((i) => (i.kind === 'row' ? i.indexInGroup : null))).toEqual([1, 2]);
  });

  it('still hides rows of a collapsed group, even ungrouped', () => {
    // `__all__` is the only key a caller can collapse when grouping is
    // 'none', and nothing surfaces to collapse it — but the guard has to
    // hold so a stale collapsedGroups set cannot blank the whole table.
    const out = flattenTableItems([group('__all__', [row('a.flac')])], {
      grouping: 'none',
      collapsedGroups: new Set(['__all__']),
    });
    expect(out).toEqual([]);
  });
});

describe('flattenTableItems — grouping: album/artist', () => {
  const groups = [
    group('A1', [row('1.flac'), row('2.flac')]),
    group('A2', [row('3.flac')]),
  ];

  it('emits a header before each group', () => {
    const out = flattenTableItems(groups, {
      grouping: 'album',
      collapsedGroups: new Set(),
    });
    expect(out.map((i) => i.kind)).toEqual(['header', 'row', 'row', 'header', 'row']);
  });

  it('carries the group size on the header, not the visible count', () => {
    const out = flattenTableItems(groups, {
      grouping: 'album',
      collapsedGroups: new Set(),
    });
    const headers = out.filter((i) => i.kind === 'header');
    expect(headers.map((h) => (h.kind === 'header' ? h.count : 0))).toEqual([2, 1]);
    expect(headers.map((h) => (h.kind === 'header' ? h.label : ''))).toEqual(['A1', 'A2']);
  });

  it('restarts numbering at 1 in each group', () => {
    // This is the regression the `#` column would hit if it read the
    // flattened array index: A2's only row would show as 3.
    const out = flattenTableItems(groups, {
      grouping: 'album',
      collapsedGroups: new Set(),
    });
    const numbered = out
      .filter((i) => i.kind === 'row')
      .map((i) => (i.kind === 'row' ? [i.row.fullPath, i.indexInGroup] : null));
    expect(numbered).toEqual([
      ['1.flac', 1],
      ['2.flac', 2],
      ['3.flac', 1],
    ]);
  });

  it('omits the rows of a collapsed group but keeps its header', () => {
    const out = flattenTableItems(groups, {
      grouping: 'album',
      collapsedGroups: new Set(['A1']),
    });
    expect(out.map((i) => i.kind)).toEqual(['header', 'header', 'row']);
    const headers = out.filter((i) => i.kind === 'header');
    // Still "2 首" — the badge counts the group, not what is on screen.
    expect(headers[0]).toMatchObject({ label: 'A1', count: 2 });
  });

  it('reports collapsed state per group so the chevron can differ', () => {
    const out = flattenTableItems(groups, {
      grouping: 'artist',
      collapsedGroups: new Set(['A2']),
    });
    expect(out.filter((i) => i.kind === 'header').map((h) => h.groupKey)).toEqual([
      'A1',
      'A2',
    ]);
  });
});

describe('flattenTableItems — keys', () => {
  it('namespaces header and row keys so they cannot collide', () => {
    // A group could be named after a path; without the `g:`/`r:` prefix
    // React would see duplicate keys and silently drop a row.
    const out = flattenTableItems([group('g:x.flac', [row('x.flac')])], {
      grouping: 'album',
      collapsedGroups: new Set(),
    });
    const keys = out.map((i) => i.key);
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys).toEqual(['g:g:x.flac', 'r:x.flac']);
  });

  it('gives every row a unique key', () => {
    const items = Array.from({ length: 50 }, (_, i) => row(`${i}.flac`));
    const out = flattenTableItems([group('__all__', items)], {
      grouping: 'none',
      collapsedGroups: new Set(),
    });
    const keys = out.map((i) => i.key);
    expect(new Set(keys).size).toBe(50);
  });
});

describe('flattenTableItems — degenerate input', () => {
  it('returns an empty list for no groups', () => {
    expect(
      flattenTableItems([], { grouping: 'album', collapsedGroups: new Set() }),
    ).toEqual([]);
  });

  it('keeps a header for a group with no rows', () => {
    // 未知专辑 collects rows whose tags have not loaded yet; a header with
    // a "0 首" badge is the honest rendering, and dropping it would make
    // the group look like it never existed.
    const out = flattenTableItems([group('未知专辑', [])], {
      grouping: 'album',
      collapsedGroups: new Set(),
    });
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ kind: 'header', count: 0 });
  });

  it('handles a collapsed empty group', () => {
    const out = flattenTableItems([group('未知专辑', [])], {
      grouping: 'album',
      collapsedGroups: new Set(['未知专辑']),
    });
    expect(out).toHaveLength(1);
  });
});
