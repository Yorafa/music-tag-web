import { useState, useMemo } from 'react';
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
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import { PlayButton } from '@/components/player/PlayButton';
import { useWorklistStore, rowMatchesFilter } from '@/store/useWorklistStore';
import { usePlayerStore } from '@/store/usePlayerStore';
import { duplicateBadgeSpec } from '@/components/workstation/duplicateBadge';
import { resolveCoverSrc, COVER_PLACEHOLDER_GRADIENTS } from '@/utils/cover';
import { buildMediaUrl } from '@/lib/mediaUrl';
import { cn } from '@/lib/utils';
import type { WorklistRow } from '@/types';

interface Props {
  activeRow: WorklistRow | null;
  onSelectRow: (row: WorklistRow) => void;
}

function gradientIdx(fullPath: string): number {
  let h = 0;
  for (let i = 0; i < fullPath.length; i++) {
    h = (h * 31 + fullPath.charCodeAt(i)) | 0;
  }
  return Math.abs(h) % COVER_PLACEHOLDER_GRADIENTS.length;
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

  const toggleGroup = (key: string) => {
    const next = new Set(collapsedGroups);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setCollapsedGroups(next);
  };

  const getStatusBadge = (status: string) => {
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
  };

  // Duplicate badge, shown next to the scrape status.
  //
  // Separate from getStatusBadge on purpose: a duplicate is a property of
  // the audio, `status` is a property of the scrape, and a file can be both
  // 已刮削 and 重复. Merging them into one badge column would force a row
  // to pick, and whichever lost would be a fact the user cannot see.
  const getDuplicateBadge = (row: WorklistRow) => {
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
  };

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

      {/* Table Rows Body */}
      <ScrollArea className="flex-1">
        <div className="divide-y divide-border/40">
          {groups.map((group) => {
            const isCollapsed = collapsedGroups.has(group.key);
            return (
              <div key={group.key}>
                {/* Group Header Row */}
                {grouping !== 'none' && (
                  <div
                    onClick={() => toggleGroup(group.key)}
                    className="flex items-center justify-between px-3 py-1.5 bg-muted/40 hover:bg-muted/70 text-xs font-semibold text-foreground cursor-pointer transition-colors"
                  >
                    <div className="flex items-center gap-1.5">
                      {isCollapsed ? (
                        <ChevronRight className="w-3.5 h-3.5 text-muted-foreground" />
                      ) : (
                        <ChevronDown className="w-3.5 h-3.5 text-muted-foreground" />
                      )}
                      <Disc className="w-3.5 h-3.5 text-primary" />
                      <span>{group.label}</span>
                    </div>
                    <Badge variant="secondary" className="text-[10px] h-4 font-mono">
                      {group.items.length} 首
                    </Badge>
                  </div>
                )}

                {/* Group Items */}
                {!isCollapsed &&
                  group.items.map((row, index) => {
                    const isSelected = selectedIds.includes(row.fullPath);
                    const isActive = activeRow?.fullPath === row.fullPath;
                    const isTrackPlaying = isPlaying && currentTrack?.id === row.fullPath;
                    const coverSrc = resolveCoverSrc(row.musicInfo);
                    const gradIdx = gradientIdx(row.fullPath);

                    return (
                      <div
                        key={row.fullPath}
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
                            toggleSelected(row.fullPath);
                          }}
                        >
                          <input
                            type="checkbox"
                            checked={isSelected}
                            onChange={() => {}}
                            className="rounded border-border text-primary focus:ring-primary h-4 w-4 sm:h-3.5 sm:w-3.5 cursor-pointer"
                          />
                          <span className="text-[10px] text-muted-foreground/60 font-mono hidden md:inline">
                            {index + 1}
                          </span>
                        </div>

                        {/* Title & Cover Thumbnail */}
                        <div className="col-span-4 lg:col-span-4 flex items-center gap-2.5 min-w-0">
                          <div className="relative w-8 h-8 rounded shrink-0 overflow-hidden bg-muted group/cover ring-1 ring-border/40">
                            {coverSrc ? (
                              <img src={coverSrc} alt="" className="w-full h-full object-cover" />
                            ) : (
                              <div
                                className="w-full h-full flex items-center justify-center text-white text-[10px] font-bold"
                                style={{ background: COVER_PLACEHOLDER_GRADIENTS[gradIdx] }}
                              >
                                {(row.musicInfo?.title || row.fileName).charAt(0)}
                              </div>
                            )}
                            <div className="absolute inset-0 bg-black/40 opacity-100 sm:opacity-0 sm:group-hover/cover:opacity-100 flex items-center justify-center transition-opacity">
                              <PlayButton
                                track={{
                                  id: row.fullPath,
                                  url: buildMediaUrl(row.fullPath),
                                  title: row.musicInfo?.title || row.fileName,
                                  artist: row.musicInfo?.artist || '',
                                  cover: coverSrc,
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
                          {getStatusBadge(row.status)}
                          {getDuplicateBadge(row)}
                        </div>

                        {/* Actions */}
                        <div className="col-span-2 lg:col-span-1 flex items-center justify-end gap-1 pr-1">
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            onClick={(e) => {
                              e.stopPropagation();
                              remove([row.fullPath]);
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
                  })}
              </div>
            );
          })}
        </div>
      </ScrollArea>
    </div>
  );
}
