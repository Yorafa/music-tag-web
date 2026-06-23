import type { SortDir, SortField } from '@/types';

const COLLATOR = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' });

/**
 * Build a comparator function for the given sort field + direction.
 *
 * Gracefully degrades when the data shape lacks a requested field:
 * - Files (FileNode): name / size / update_time all present.
 * - Songs (SongInfo): only `name` (title) is meaningful; missing size /
 *   update_time return 0 so the songs preserve the backend-returned order.
 */
/**
 * Generic constraint: input shape must at least optionally declare the four
 * sort fields. Looser than Record<string, unknown> so interfaces without
 * index signatures (FileNode, SongInfo) still satisfy the bound, while
 * keeping member access type-checked.
 */
type SortableShape = {
  name?: unknown;
  title?: unknown;
  size?: unknown;
  update_time?: unknown;
};

export function makeCompareFn<T extends SortableShape>(
  field: SortField,
  dir: SortDir,
): (a: T, b: T) => number {
  const sign = dir === 'desc' ? -1 : 1;
  return (a, b) => {
    if (field === 'name') {
      const aName = String(a.name ?? a.title ?? '');
      const bName = String(b.name ?? b.title ?? '');
      return sign * COLLATOR.compare(aName, bName);
    }
    if (field === 'size') {
      const av = Number(a.size ?? 0);
      const bv = Number(b.size ?? 0);
      return sign * (av - bv);
    }
    if (field === 'update_time') {
      const av = String(a.update_time ?? '');
      const bv = String(b.update_time ?? '');
      return sign * av.localeCompare(bv);
    }
    return 0;
  };
}
