// Flattening the grouped queue into the single ordered list the virtualizer
// consumes.
//
// Why this is separate from the component: virtualizing means the component
// indexes ONE array, but the queue is a two-level structure (groups of
// rows). Flattening in the render body would make it untestable — there is
// no @testing-library/react in this project, so component-internal logic can
// only be covered by driving react-dom by hand. As a pure function it gets
// ordinary unit tests, and the component keeps only the mechanical work.
//
// The flattening also decides two things that are easy to get wrong when
// done inline:
//
//   - `indexInGroup` is the row's position WITHIN ITS GROUP, which is what
//     the `#` column has always shown. Once rows are windowed, the array
//     index is a scroll position, not a track number — reading it from
//     there would renumber every visible row as the user scrolls.
//   - A collapsed group contributes its header and nothing else, so the
//     list shrinks when a group collapses and the scroll position has to be
//     reconciled (the component does that with scrollToOffset).

import type { WorklistGrouping } from '@/store/useWorklistStore';
import type { WorklistRow } from '@/types';

export interface TableGroup {
  key: string;
  label: string;
  items: WorklistRow[];
}

/** One entry in the virtualized list. Exactly one of the two shapes. */
export type TableItem =
  | {
      kind: 'header';
      /** Stable across re-renders and unique in the list. */
      key: string;
      groupKey: string;
      label: string;
      /** Rows in the group, INCLUDING collapsed ones — the badge count is
       *  a property of the group, not of what is currently expanded. */
      count: number;
    }
  | {
      kind: 'row';
      key: string;
      groupKey: string;
      row: WorklistRow;
      /** 1-based position within the group; what the `#` column renders. */
      indexInGroup: number;
    };

/** Flatten grouped rows into the list the virtualizer windows over.
 *
 *  With `grouping === 'none'` the caller passes a single synthetic group
 *  (`{ key: '__all__' }`) and gets its rows back with NO header entry — that
 *  is what makes the header conditional in the old inline code, expressed
 *  here as a property of the output list rather than a second render path. */
export function flattenTableItems(
  groups: TableGroup[],
  opts: { grouping: WorklistGrouping; collapsedGroups: Set<string> },
): TableItem[] {
  const out: TableItem[] = [];
  const showHeaders = opts.grouping !== 'none';

  for (const group of groups) {
    if (showHeaders) {
      out.push({
        kind: 'header',
        // Namespace the key so a group literally named like another
        // entry's row path can never collide.
        key: `g:${group.key}`,
        groupKey: group.key,
        label: group.label,
        count: group.items.length,
      });
    }
    if (opts.collapsedGroups.has(group.key)) continue;
    group.items.forEach((row, i) => {
      out.push({
        kind: 'row',
        key: `r:${row.fullPath}`,
        groupKey: group.key,
        row,
        indexInGroup: i + 1,
      });
    });
  }
  return out;
}
