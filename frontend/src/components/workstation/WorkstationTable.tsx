import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Music,
  Check,
  Clock,
  AlertCircle,
  Sparkles,
  Trash2,
  ChevronDown,
  ChevronRight,
  Disc,
  Copy,
  CopyCheck,
} from 'lucide-react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import { PlayButton } from '@/components/player/PlayButton';
import { useWorklistStore, rowMatchesFilter } from '@/store/useWorklistStore';
import { usePlayerStore } from '@/store/usePlayerStore';
import { duplicateBadgeSpec } from '@/components/workstation/duplicateBadge';
import { flattenTableItems } from '@/components/workstation/tableItems';
import { RowCover } from '@/components/workstation/RowCover';
import { resolveCoverSrc, COVER_PLACEHOLDER_GRADIENTS } from '@/utils/cover';
import { buildMediaUrl } from '@/lib/mediaUrl';
import { cn } from '@/lib/utils';
import type { WorklistRow } from '@/types';

interface Props {
  activeRow: WorklistRow | null;
  onSelectRow: (row: WorklistRow) => void;
}

/** Row height estimate fed to the virtualizer before a row is measured.
 *
 *  Measured from the CSS: py-2 (16px) + the 32px cover thumbnail = 48px.
 *  A row ALSO carrying a duplicate badge is 50px, because the status column
 *  stacks two 16px badges with a 2px gap. The estimate does not have to be
 *  right for every row — `measureElement` corrects each one as it mounts —
 *  but it must be in the right neighbourhood or the scrollbar jumps on the
 *  first paint. */
const ESTIMATED_ROW_HEIGHT = 48;

/** Group header height, for the same reason: an estimate, corrected on
 *  measure. py-1.5 (12px) + a 16px line. */
const ESTIMATED_HEADER_HEIGHT = 28;

/** How many items to render beyond the viewport, in each direction.
 *
 *  Large enough that a fast flick does not reveal blank space before the
 *  next frame paints, small enough that the overscan stays cheaper than the
 *  win. 12 rows is roughly two and a half screens at a typical row height.
 *
 *  This is the single knob that trades scroll smoothness against DOM size:
 *  the window is `viewport / rowHeight + 2 * OVERSCAN` items, so at 4000
 *  rows the list holds ~40 rows instead of 4000. */
const OVERSCAN = 12;

function gradientIdx(fullPath: string): number {
  let h = 0;
  for (let i = 0; i < fullPath.length; i++) {
    h = (h * 31 + fullPath.charCodeAt(i)) | 0;
  }
  return Math.abs(h) % COVER_PLACEHOLDER_GRADIENTS.length;
}

function StatusBadge({ status }: { status: string }) {
  switch (status) {
    case 'scraped':
      return (
        <Badge variant="outline" className="text-[10px] px-1.5 py-0 h-4 text-emerald-500 border-emerald-500/40 bg-emerald-500/10">
          <Check className="w-2.5 h-2.5 mr-0.5" /> 已刮削
        </Badge>
      );
    case 'failed':
      return (
        <Badge variant="outline" className="text-[10px] px-1.5 py-0 h-4 text-destructive border-destructive/40 bg-destructive/10">
          <AlertCircle className="w-2.5 h-2.5 mr-0.5" /> 失败
        </Badge>
      );
    case 'processing':
      return (
        <Badge variant="outline" className="text-[10px] px-1.5 py-0 h-4 text-primary border-primary/40 bg-primary/10 animate-pulse">
          <Sparkles className="w-2.5 h-2.5 mr-0.5" /> 刮削中
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className="text-[10px] px-1.5 py-0 h-4 text-amber-500 border-amber-500/40 bg-amber-500/10">
          <Clock className="w-2.5 h-2.5 mr-0.5" /> 待刮削
        </Badge>
      );
  }
}

// Duplicate badge, shown next to the scrape status.
//
// Separate from StatusBadge on purpose: a duplicate is a property of
// the audio, `status` is a property of the scrape, and a file can be both
// 已刮削 and 重复. Merging them into one badge column would force a row
// to pick, and whichever lost would be a fact the user cannot see.
function DuplicateBadge({ row }: { row: WorklistRow }) {
  const spec = duplicateBadgeSpec(row.duplicate);
  if (!spec) return null;
  const styles = {
    duplicate:
      'text-destructive border-destructive/40 bg-destructive/10',
    likely: 'text-amber-500 border-amber-500/40 bg-amber-500/10',
    checked: 'text-muted-foreground border-border/60 bg-muted/30',
  }[spec.tone];
  const Icon =
    spec.tone === 'duplicate' ? Copy : spec.tone === 'likely' ? AlertCircle : CopyCheck;
  return (
    <Badge
      variant="outline"
      className={`text-[10px] px-1.5 py-0 h-4 ${styles}`}
      title={spec.title}
    >
      <Icon className="w-2.5 h-2.5 mr-0.5" /> {spec.label}
    </Badge>
  );
}

interface RowProps {
  row: WorklistRow;
  /** 1-based position within the group — NOT the virtual window index. */
  indexInGroup: number;
  isSelected: boolean;
  isActive: boolean;
  isTrackPlaying: boolean;
  onSelectRow: (row: WorklistRow) => void;
  onToggle: (fullPath: string) => void;
  onRemove: (ids: string[]) => void;
}

/** One queue row.
 *
 *  Memoized because the virtualizer re-renders the window on every scroll
 *  frame: without this, scrolling would re-render all ~40 visible rows even
 *  though only their POSITIONS changed. The callbacks are stable (the parent
 *  wraps them in useCallback) so a scroll does not invalidate the memo for
 *  rows whose data did not change. */
const Row = memo(function Row({
  row,
  indexInGroup,
  isSelected,
  isActive,
  isTrackPlaying,
  onSelectRow,
  onToggle,
  onRemove,
}: RowProps) {
  const gradIdx = gradientIdx(row.fullPath);

  return (
    <div
      onClick={() => onSelectRow(row)}
      className={cn(
        'grid grid-cols-12 gap-2 px-3 py-2 items-center text-xs transition-colors cursor-pointer group',
        isActive
          ? 'bg-primary/10 hover:bg-primary/15'
          : isSelected
            ? 'bg-accent/40 hover:bg-accent/60'
            : 'hover:bg-muted/40',
      )}
    >
      {/* Checkbox & Index. The negative margin plus padding
          makes the CELL the tap target rather than the 16px
          box inside it — the cell's own onClick already
          toggles the row, so on a phone a thumb lands on
          ~28px of column instead of a checkbox square. */}
      <div
        className="col-span-1 flex items-center gap-2 py-1.5 -my-1.5"
        onClick={(e) => {
          e.stopPropagation();
          onToggle(row.fullPath);
        }}
      >
        <input
          type="checkbox"
          checked={isSelected}
          onChange={() => {}}
          aria-label={`选择 ${row.musicInfo?.title || row.fileName}`}
          className="rounded border-border text-primary focus:ring-primary h-4 w-4 sm:h-3.5 sm:w-3.5 cursor-pointer"
        />
        <span className="text-[10px] text-muted-foreground/60 font-mono hidden md:inline">
          {indexInGroup}
        </span>
      </div>

      {/* Title & Cover Thumbnail */}
      <div className="col-span-4 lg:col-span-4 flex items-center gap-2.5 min-w-0">
        <div className="relative w-8 h-8 rounded shrink-0 overflow-hidden bg-muted group/cover ring-1 ring-border/40">
          {/* Cover bytes come from /api/album_cover/ per visible row, not
              from the batch hydrate. See RowCover for why. */}
          <RowCover
            fullPath={row.fullPath}
            fileName={row.fileName}
            fallbackTitle={row.musicInfo?.title}
            gradient={COVER_PLACEHOLDER_GRADIENTS[gradIdx]}
          />
          <div className="absolute inset-0 bg-black/40 opacity-100 sm:opacity-0 sm:group-hover/cover:opacity-100 flex items-center justify-center transition-opacity">
            <PlayButton
              track={{
                id: row.fullPath,
                url: buildMediaUrl(row.fullPath),
                title: row.musicInfo?.title || row.fileName,
                artist: row.musicInfo?.artist || '',
                // The player may want the cover for its own UI, and it plays
                // the whole track rather than a thumbnail. Reuse the
                // hydrated inline artwork when the editor put it there, else
                // leave it undefined — the player's own fallback is
                // correct, and fetching a 3–15 MB scan for it would undo
                // the entire point of the light batch read.
                cover: resolveCoverSrc(row.musicInfo),
                lyrics: row.musicInfo?.lyrics || undefined,
                source: {
                  kind: 'local',
                  fileName: row.fileName,
                  filePath: row.fullPath.substring(0, row.fullPath.lastIndexOf('/')) || '',
                },
              }}
            />
          </div>
        </div>

        <div className="min-w-0 flex-1">
          <p className={cn('font-medium truncate', isTrackPlaying ? 'text-primary font-semibold' : 'text-foreground')}>
            {row.musicInfo?.title || row.fileName}
          </p>
          <p className="text-[10px] text-muted-foreground truncate font-mono">
            {row.fileName}
          </p>
        </div>
      </div>

      {/* Artist */}
      <div className="col-span-3 lg:col-span-3 truncate text-muted-foreground">
        {row.musicInfo?.artist || '—'}
      </div>

      {/* Album */}
      <div className="hidden sm:block col-span-2 truncate text-muted-foreground">
        {row.musicInfo?.album || '—'}
      </div>

      {/* Status */}
      <div className="col-span-2 sm:col-span-1 flex flex-col items-center gap-0.5">
        <StatusBadge status={row.status} />
        <DuplicateBadge row={row} />
      </div>

      {/* Actions */}
      <div className="col-span-2 lg:col-span-1 flex items-center justify-end gap-1 pr-1">
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={(e) => {
            e.stopPropagation();
            onRemove([row.fullPath]);
          }}
          // Always visible on a phone. `opacity-0 group-hover:opacity-100` alone
          // leaves the button invisible there — no hover means no reveal —
          // while still hit-testable, so a blind tap in that spot
          // removes a row. sm: puts the hover behaviour back where hover
          // exists.
          className="text-muted-foreground hover:text-destructive h-7 w-7 sm:h-6 sm:w-6 sm:opacity-0 sm:group-hover:opacity-100 sm:focus-visible:opacity-100"
          title="从列表中移除"
        >
          <Trash2 className="w-3 h-3" />
        </Button>
      </div>
    </div>
  );
});

function GroupHeader({
  label,
  count,
  collapsed,
  onToggle,
}: {
  label: string;
  count: number;
  collapsed: boolean;
  onToggle: () => void;
}) {
  return (
    <div
      onClick={onToggle}
      className="flex items-center justify-between px-3 py-1.5 bg-muted/40 hover:bg-muted/70 text-xs font-semibold text-foreground cursor-pointer transition-colors"
    >
      <div className="flex items-center gap-1.5">
        {collapsed ? (
          <ChevronRight className="w-3.5 h-3.5 text-muted-foreground" />
        ) : (
          <ChevronDown className="w-3.5 h-3.5 text-muted-foreground" />
        )}
        <Disc className="w-3.5 h-3.5 text-primary" />
        <span>{label}</span>
      </div>
      <Badge variant="secondary" className="text-[10px] h-4 font-mono">
        {count} 首
      </Badge>
    </div>
  );
}

export function WorkstationTable({ activeRow, onSelectRow }: Props) {
  const rows = useWorklistStore((s) => s.rows);
  const filter = useWorklistStore((s) => s.filter);
  const grouping = useWorklistStore((s) => s.grouping);
  const selectedIds = useWorklistStore((s) => s.selectedIds);
  const toggleSelected = useWorklistStore((s) => s.toggleSelected);
  const remove = useWorklistStore((s) => s.remove);
  const isPlaying = usePlayerStore((s) => s.isPlaying);
  const currentTrack = usePlayerStore((s) => s.currentTrack);

  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set());

  // Filter rows. rowMatchesFilter is shared with selectAll() so 「全选」
  // and the visible set cannot disagree — see its comment.
  const filteredRows = useMemo(
    () => rows.filter((r) => rowMatchesFilter(r, filter)),
    [rows, filter],
  );

  // Group rows if grouping !== 'none'
  const groups = useMemo(() => {
    if (grouping === 'none') {
      return [{ key: '__all__', label: '', items: filteredRows }];
    }
    const map = new Map<string, WorklistRow[]>();
    for (const r of filteredRows) {
      let gKey = '未知';
      if (grouping === 'album') {
        gKey = r.musicInfo?.album || '未知专辑';
      } else if (grouping === 'artist') {
        gKey = r.musicInfo?.artist || '未知艺术家';
      }
      if (!map.has(gKey)) map.set(gKey, []);
      map.get(gKey)!.push(r);
    }
    return Array.from(map.entries()).map(([key, items]) => ({
      key,
      label: key,
      items,
    }));
  }, [filteredRows, grouping]);

  // One flat, ordered list for the virtualizer. See tableItems.ts for why
  // the row's own number comes from here and not from the window index.
  const items = useMemo(
    () => flattenTableItems(groups, { grouping, collapsedGroups }),
    [groups, grouping, collapsedGroups],
  );

  // `selectedIds` is an array and `includes` is linear, so a membership test
  // per row made selection state cost O(rows × selected). A Set makes the
  // per-row lookup O(1), which matters more now that the window re-renders
  // on every scroll frame.
  const selectedSet = useMemo(() => new Set(selectedIds), [selectedIds]);

  const scrollRef = useRef<HTMLDivElement | null>(null);

  // Why the `incompatible-library` warning below is suppressed:
  //
  // The rule warns that useVirtualizer's return "cannot be memoized safely"
  // because its methods close over a mutable instance that React Compiler
  // cannot prove stable. Two things make it safe HERE:
  //
  //  1. React Compiler is not enabled in this app (no babel/react-compiler
  //     plugin in vite.config.ts), so nothing is auto-memoizing this
  //     component. The warning describes a hazard that does not currently
  //     exist. If the compiler is ever turned on, this suppression is the
  //     line to revisit — and the correct fix then is to stop spreading
  //     `virtualizer` across the render and pull only the two values used
  //     (getVirtualItems / getTotalSize) into locals.
  //  2. `virtualizer.measureElement` is a stable closure created once per
  //     virtualizer instance (virtual-core binds it in the constructor),
  //     so passing it as a ref does not thrash on every render — which is
  //     the actual way a bad ref identity would break measurement.
  //
  // The memoization that DOES matter here is explicit: `Row` is wrapped in
  // memo() so scrolling does not re-render the ~40 visible rows, and the
  // three callbacks it receives are useCallback-wrapped above.
  //
  // eslint-disable-next-line react-hooks/incompatible-library -- see above
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: (i) =>
      items[i]?.kind === 'header' ? ESTIMATED_HEADER_HEIGHT : ESTIMATED_ROW_HEIGHT,
    overscan: OVERSCAN,
    // Keyed by row path so a row keeps its measured height, its DOM node
    // and its scroll position across a re-render. Index keys would make
    // every row after an insertion a different row.
    getItemKey: (i) => items[i]?.key ?? i,
  });

  // A filter or grouping change can leave the viewport scrolled past the end
  // of a now-shorter list, which renders as an empty table with no way to
  // tell it from "the filter matched nothing". Clamping to the new end is
  // the difference between "here are fewer rows" and "the app broke".
  useEffect(() => {
    virtualizer.scrollToOffset(0);
  }, [filter, grouping, virtualizer]);

  const handleToggleGroup = useCallback((key: string) => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  // Stable identities so scrolling does not re-render memoized rows: the
  // virtualizer re-renders the window on every scroll frame, and a fresh
  // callback each render would defeat Row's memo entirely.
  const handleSelectRow = useCallback(
    (row: WorklistRow) => onSelectRow(row),
    [onSelectRow],
  );
  const handleToggle = useCallback(
    (fullPath: string) => toggleSelected(fullPath),
    [toggleSelected],
  );
  const handleRemove = useCallback(
    (ids: string[]) => remove(ids),
    [remove],
  );

  if (rows.length === 0) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center p-8 text-center space-y-3 bg-surface-1 select-none">
        <div className="w-14 h-14 rounded-2xl bg-primary/10 text-primary flex items-center justify-center">
          <Music className="w-7 h-7" />
        </div>
        <div className="space-y-1">
          <p className="text-sm font-semibold text-foreground">当前曲目队列为空</p>
          <p className="text-xs text-muted-foreground max-w-sm">
            点击左侧目录树的「+」或上方「添加目录」载入本地/NAS 音乐文件，开启一键自动批量刮削与标签整理
          </p>
        </div>
      </div>
    );
  }

  const virtualItems = virtualizer.getVirtualItems();

  return (
    <div className="flex-1 flex flex-col min-h-0 bg-surface-1 overflow-hidden select-none">
      {/* Table Header Row */}
      <div className="grid grid-cols-12 gap-2 px-3 py-2 border-b border-border bg-surface-2/40 text-[11px] font-semibold text-muted-foreground shrink-0">
        <div className="col-span-1 flex items-center gap-2">
          <span>#</span>
        </div>
        <div className="col-span-4 lg:col-span-4 truncate">歌曲 / 标题</div>
        <div className="col-span-3 lg:col-span-3 truncate">艺术家 / 歌手</div>
        <div className="hidden sm:block col-span-2 truncate">专辑</div>
        <div className="col-span-2 sm:col-span-1 truncate text-center">状态</div>
        <div className="col-span-2 lg:col-span-1 text-right pr-2">操作</div>
      </div>

      {/* Table Rows Body.
          The Viewport is the element that actually scrolls, so the
          virtualizer measures against it (see ScrollArea's viewportRef).
          The inner spacer carries the full list height while only the
          visible window is in the DOM — that is what makes the scrollbar
          represent all N rows. Rows are absolutely positioned at their
          measured offset instead of stacked, so dropping the off-screen
          ones cannot shift anything that is still visible. */}
      <ScrollArea className="flex-1" viewportRef={scrollRef}>
        <div
          style={{
            height: virtualizer.getTotalSize(),
            width: '100%',
            position: 'relative',
          }}
        >
          {virtualItems.map((vi) => {
            const item = items[vi.index];
            if (!item) return null;
            return (
              <div
                key={vi.key}
                data-index={vi.index}
                // measureElement reports the real height (48px, or 50px with
                // a duplicate badge) back to the virtualizer, so the scroll
                // length stays honest instead of drifting.
                ref={virtualizer.measureElement}
                className="absolute top-0 left-0 w-full"
                style={{ transform: `translateY(${vi.start}px)` }}
              >
                {item.kind === 'header' ? (
                  <GroupHeader
                    label={item.label}
                    count={item.count}
                    collapsed={collapsedGroups.has(item.groupKey)}
                    onToggle={() => handleToggleGroup(item.groupKey)}
                  />
                ) : (
                  <Row
                    row={item.row}
                    indexInGroup={item.indexInGroup}
                    isSelected={selectedSet.has(item.row.fullPath)}
                    isActive={activeRow?.fullPath === item.row.fullPath}
                    isTrackPlaying={isPlaying && currentTrack?.id === item.row.fullPath}
                    onSelectRow={handleSelectRow}
                    onToggle={handleToggle}
                    onRemove={handleRemove}
                  />
                )}
              </div>
            );
          })}
        </div>
      </ScrollArea>
    </div>
  );
}
