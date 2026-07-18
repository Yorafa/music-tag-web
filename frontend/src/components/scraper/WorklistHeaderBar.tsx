// Worklist header bar. Three affordances:
//
//  1. + 添加音乐 — opens the shared DirPickerDrawer with destination
//     'worklist'. Same component Plan B uses for library mode; only
//     the destination prop differs.
//
//  2. Filter (全部 / 待刮 / 已刮 / 失败) — toggles useWorklistStore.filter.
//     Each chip's count badge reflects the current row set so the user
//     sees at a glance how many candidates fall under each category.
//
//  3. 全选 — toggles the in-filter selection. Intent: selectAll()
//     picks rows that match the CURRENT filter, not the unfiltered
//     universe — when the user lands in the "失败" filter, clicking
//     全选 picks the failed rows for a targeted retry.

import { Plus, Check } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Separator } from '@/components/ui/separator';
import { cn } from '@/lib/utils';
import { useWorklistStore } from '@/store/useWorklistStore';
import type { ScrapeStatus } from '@/types';

interface FilterDef {
  id: 'all' | ScrapeStatus;
  label: string;
}

const FILTERS: FilterDef[] = [
  { id: 'all', label: '全部' },
  { id: 'pending', label: '待刮' },
  { id: 'scraped', label: '已刮' },
  { id: 'failed', label: '失败' },
];

interface Props {
  onOpenDirPicker: () => void;
}

export function WorklistHeaderBar({ onOpenDirPicker }: Props) {
  const rows = useWorklistStore((s) => s.rows);
  const filter = useWorklistStore((s) => s.filter);
  const setFilter = useWorklistStore((s) => s.setFilter);
  const selectedIds = useWorklistStore((s) => s.selectedIds);
  const selectAll = useWorklistStore((s) => s.selectAll);
  const clearSelected = useWorklistStore((s) => s.clearSelected);

  const counts: Record<FilterDef['id'], number> = {
    all: rows.length,
    pending: rows.filter((r) => r.status === 'pending').length,
    scraped: rows.filter((r) => r.status === 'scraped').length,
    failed: rows.filter((r) => r.status === 'failed').length,
  };

  // Always count visible-in-filter rows for "select all" decisions —
  // matches the spec ("selectAll selects all visible-after-filter rows").
  const visibleCount = rows.filter((r) =>
    filter === 'all' ? true : r.status === filter,
  ).length;

  // Derive the all-selected state purely from store snapshots — no
  // local mirror state needed. (selectedIds is a live subscription
  // that updates the same render; visibleCount is a derived number
  // — the comparison is dispatch-time-stable.)
  const allSelected = selectedIds.length > 0 && selectedIds.length === visibleCount;

  const onToggleAll = () => {
    if (allSelected) {
      clearSelected();
    } else {
      selectAll();
    }
  };

  return (
    <div className="flex items-center gap-2 px-3 py-2 border-b border-border bg-surface-1 shrink-0">
      <Button
        variant="outline"
        size="sm"
        onClick={onOpenDirPicker}
        aria-label="添加音乐"
        title="从目录添加待刮文件"
      >
        <Plus className="w-3.5 h-3.5 mr-1.5" />
        添加音乐
      </Button>

      <Separator orientation="vertical" className="mx-1 h-5" />

      <div className="flex items-center gap-1">
        {FILTERS.map((f) => {
          const isActive = filter === f.id;
          return (
            <button
              key={f.id}
              type="button"
              onClick={() => setFilter(f.id)}
              aria-pressed={isActive}
              title={`过滤：${f.label}`}
              className={cn(
                'inline-flex items-center gap-1 h-7 px-2 rounded-md text-xs font-medium transition-colors',
                isActive
                  ? 'bg-primary/10 text-primary'
                  : 'text-muted-foreground hover:bg-accent hover:text-foreground',
              )}
            >
              <span>{f.label}</span>
              <Badge
                variant={isActive ? 'default' : 'secondary'}
                className="text-[10px] px-1.5 py-0 h-3.5 min-w-[20px] justify-center"
              >
                {counts[f.id]}
              </Badge>
            </button>
          );
        })}
      </div>

      <Separator orientation="vertical" className="mx-1 h-5" />

      <Button
        variant={allSelected ? 'secondary' : 'ghost'}
        size="sm"
        onClick={onToggleAll}
        disabled={visibleCount === 0}
        aria-label={allSelected ? '取消全选' : '全选'}
        title={
          allSelected
            ? '清除当前过滤下的全部选中'
            : `选中当前过滤下的全部 ${visibleCount} 行`
        }
      >
        <Check className="w-3.5 h-3.5 mr-1.5" />
        {allSelected ? '取消全选' : '全选'}
      </Button>

      {/* Spacer + absolute-position total so the right edge carries the
          always-visible count regardless of viewport width. */}
      <div className="ml-auto text-xs text-muted-foreground tabular-nums">
        {selectedIds.length > 0
          ? `${selectedIds.length} / ${visibleCount}`
          : `${rows.length} 行`}
      </div>
    </div>
  );
}
