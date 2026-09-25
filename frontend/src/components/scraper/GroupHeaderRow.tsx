// GroupHeaderRow — inserted into the Worklist flat render when the
// user has grouped by album or artist. Visual contract (.plan § C.3 step 3):
//
//   - background: var(--surface-2) (token defined in frontend/src/index.css
//     and bound into Tailwind via the bg-surface-2 utility).
//   - chevron flips on collapse toggle; the whole row is the click target
//     so the user doesn't have to aim at the tiny icon.
//   - the label is a single-line truncate so a 200-char album name still
//     renders cleanly.
//   - count badge: total rows under this group AFTER the filter, so the
//     user can gauge scope before expanding.
//
// IMPORTANT — interaction contract (Plan C.3 step 2 invariant):
//   The GroupHeaderRow click toggles collapse / expand ONLY. It MUST NOT
//   toggle row selection. Selection is reserved for the per-row checkbox
//   and the 全选 header button. Mixing the two surfaces would let users
//   accidentally drop rows from the in-progress tag batch — one of the
//   pre-refactor failure modes that's gone since Worklist store v2, and
//   grouping shouldn't reintroduce it.
//
// Sticky allowance: the Worklist container is a flex column with
// flex-1 overflow; we DO NOT make these headers sticky for now (no
// virtualization, no large jumps in real use; could add `sticky top-0`
// later if a user reports "scrolled past 200 albums and forgot where
// they were").

import { ChevronDown, ChevronRight } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

interface Props {
  /** Stable group key — typically `<groupKind>:<label>` so adding a
   *  row with the same album merges into the existing group's
   *  collapsed-state slot. Plain label alone would collide across
   *  grouping axes (e.g. an artist named "Compilation"). */
  groupKey: string;
  /** Display name (`album`, `artist`, "unknown album" for unset). */
  label: string;
  /** Filter-aware row count under this group (e.g. when in the
   *  "失败" filter, a group's count drops to its failed rows only). */
  count: number;
  collapsed: boolean;
  onToggle: () => void;
}

export function GroupHeaderRow({
  groupKey,
  label,
  count,
  collapsed,
  onToggle,
}: Props) {
  // aria-controls points at the FIRST row's id? — we don't render
  // every row with an id (yet). For now the groupKey is unique within
  // the page; future DOM-id linking is a stable improvement.
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={!collapsed}
      aria-controls={`worklist-group-${groupKey}`}
      data-group-key={groupKey}
      title={collapsed ? `展开：${label} (${count})` : `折叠：${label} (${count})`}
      className={cn(
        'group/header w-full flex items-center gap-2 px-4 py-1.5 cursor-pointer',
        'bg-surface-2 hover:bg-surface-2/80 transition-colors text-xs font-medium',
        'border-b border-border/50 text-foreground',
      )}
    >
      {collapsed ? (
        <ChevronRight className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
      ) : (
        <ChevronDown className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
      )}
      <span className="truncate flex-1 text-left">{label}</span>
      <Badge
        variant="secondary"
        className="text-[10px] px-1.5 py-0 h-3.5 min-w-[20px] justify-center shrink-0"
      >
        {count}
      </Badge>
    </button>
  );
}
