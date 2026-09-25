// Play-mode top action bar — the single horizontal strip header above the
// PlayView main area. The layout matches the scraper's worklist toolbar
// (Plan B decision #9: independent bars, same layout tokens — `flex
// items-center gap-2 px-3 py-2 border-b border-border bg-surface-2
// shrink-0`) so both modes share a visual rhythm at the top of the
// screen even though their action sets are disjoint.
//
// Action set (Plan B decision #3 — Navidrome-style single main area, no
// sidebar):
//   [添加音乐]  → opens the DirPickerDrawer with destination='library'.
//                That drawer is owned by Plan A; until Plan A's actual
//                DirPickerDrawer lands on the base branch I expose the
//                click through `onAddMusic` so PlayView can mount a stub
//                drawer (see PlayView). Once Plan A ships, swap in
//                `<DirPickerDrawer open onOpenChange destination="library"
//                existingDirs={useLibraryStore.getState().dirs}/>` here.
//   [搜索音乐]  → toggles the inline search input. The input writes to
//                `useLibraryStore.search`, which PlayView uses via
//                `getFiltered()`. Toggling hides the input but keeps the
//                query value — pressing Escape or un-toggling resets the
//                query to '' so the list springs back to full visibility.
//
// Reset/清空 button: only shown when `useLibraryStore.rows.length > 0`
// — clears the entire trial-listen library (localStorage purge). This is
// the explicit escape hatch documented in useLibraryStore.clear() and is
// how the user frees localStorage quota after a big tagging session.

import { useState, useCallback, type KeyboardEvent } from 'react';
import { Plus, Search, X, Eraser } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useLibraryStore } from '@/store/useLibraryStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { cn } from '@/lib/utils';

interface Props {
  /** Called when the user clicks 「添加音乐」. PlayView owns the drawer
   *  open state so it can mount the placeholder drawer OR Plan A's real
   *  DirPickerDrawer depending on integration stage. */
  onAddMusic: () => void;
}

export function PlayTopBar({ onAddMusic }: Props) {
  const rowcount = useLibraryStore((s) => s.rows.length);
  const query = useLibraryStore((s) => s.query);
  const clear = useLibraryStore((s) => s.clear);

  const [searchOpen, setSearchOpen] = useState(false);

  const toggleSearch = useCallback(() => {
    setSearchOpen((v) => {
      // Closing the search clears the query — drop back to full list.
      if (v) {
        useLibraryStore.getState().search('');
      }
      return !v;
    });
  }, []);

  const onSearchKey = useCallback((e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') {
      useLibraryStore.getState().search('');
      setSearchOpen(false);
    }
  }, []);

  const handleClear = useCallback(() => {
    clear();
    useNoticeStore.getState().push('已清空本地试听列表', 'info');
  }, [clear]);

  return (
    <div
      className={cn(
        'flex items-center gap-2 px-3 py-2 border-b border-border bg-surface-2 shrink-0',
      )}
    >
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={onAddMusic}
        disabled={false}
        className="gap-1.5"
        title="选择一个或多个目录，扁平为音频文件加入本地列表"
      >
        <Plus className="w-3.5 h-3.5" />
        添加音乐
      </Button>

      <Button
        type="button"
        variant={searchOpen ? 'default' : 'outline'}
        size="sm"
        onClick={toggleSearch}
        className="gap-1.5"
        title="在已添加的本地文件里过滤显示"
        aria-pressed={searchOpen}
      >
        <Search className="w-3.5 h-3.5" />
        搜索本地音乐
      </Button>

      {searchOpen && (
        <Input
          type="text"
          // controlled via store + local open; query emitted from store
          // so PlayView's getFiltered() is consistent on the same render
          // cycle as this input.
          value={query}
          onChange={(e) => useLibraryStore.getState().search(e.target.value)}
          onKeyDown={onSearchKey}
          placeholder="过滤本地音乐..."
          className="h-8 max-w-[16rem] text-xs"
          aria-label="过滤本地音乐"
          autoFocus
        />
      )}

      {searchOpen && query && (
        <button
          type="button"
          onClick={() => useLibraryStore.getState().search('')}
          className="text-muted-foreground hover:text-foreground transition-colors"
          title="清除过滤"
          aria-label="清除过滤"
        >
          <X className="w-3.5 h-3.5" />
        </button>
      )}

      <span className="ml-auto text-xs text-muted-foreground tabular-nums">
        {rowcount > 0 ? `共 ${rowcount} 首` : ''}
      </span>

      {rowcount > 0 && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleClear}
          className="gap-1.5 text-muted-foreground hover:text-foreground"
          title="清空整个本地列表（不可撤销）"
        >
          <Eraser className="w-3.5 h-3.5" />
          清空
        </Button>
      )}
    </div>
  );
}
