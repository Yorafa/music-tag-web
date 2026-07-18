// CloudSearchDialog — Play 模式下「搜索云音乐」弹层。
//
// 经典音乐搜索样式：歌名输入框 + 音乐源单选下拉 + 搜索按钮 + 结果列表。
// 与 SearchPanel.tsx（全页多源同搜）刻意保持单源语义，避免在 Play
// 模式下还要先勾选 1+ 个源这种额外认知成本。每个 Dialog 实例都是单源
// 查询——多源并搜请走全局 SearchPanel。
//
// 设计要点：
//   - 输入：Dialog 打开时 autoFocus。
//   - 源：useSourceStore.sources 中 kind==='tag' 子集；用 useMemo 派生
//     `effectiveSource`（而非 useEffect + setState），规避
//     react-hooks/set-state-in-effect —— base-ui 的 Select.Root 在无值
//     时会传 null 给 onValueChange，用 wrapper 跳过 null。
//   - 搜索：searchMusic({query, sources:[effectiveSource], pages:{},
//     limit:20})。单源单页，不做「加载更多」分页——经典搜索 UX。
//   - 结果：每条复用 PlayButton 做试听，<a target="_blank"> 做下载。
//   - 状态全本地，不入 store、不持久化——打开关闭自动清空。
//
// 与 PlayView 的契约：open + onOpenChange。

import { useEffect, useMemo, useState, useCallback } from 'react';
import { searchMusic } from '@/api/client';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { ScrollArea } from '@/components/ui/scroll-area';
import { PlayButton } from '@/components/player/PlayButton';
import { useSourceStore } from '@/store/useSourceStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import type { SearchResult } from '@/types';
import {
  Search,
  Loader2,
  Music,
  Download,
  XIcon,
} from 'lucide-react';
import { cn } from '@/lib/utils';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/** 当 useSourceStore 还未 hydrate 出 tag 源时的兜底值。这个名字被有意
 *  设成「绝大多数 backend 注册的常见 plugin 名」，这样 effectiveSource
 *  在源未到达前用一个大概率合法的占位，源到达后会被 default_on 或第一
 * 个 tag 源覆盖。 */
const SOURCE_FALLBACK = 'netease';

export function CloudSearchDialog({ open, onOpenChange }: Props) {
  const sourceList = useSourceStore((s) => s.sources);
  const sourcesLoaded = useSourceStore((s) => s.loaded);
  const loadSources = useSourceStore((s) => s.loadSources);

  const [query, setQuery] = useState('');
  const [source, setSource] = useState<string>(SOURCE_FALLBACK);
  const [results, setResults] = useState<SearchResult[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasSearched, setHasSearched] = useState(false);

  // 一次性源拉取：store 内部已 loaded === true 时再次调用是 noop。
  useEffect(() => {
    void loadSources();
  }, [loadSources]);

  // 派生 effectiveSource：源未就绪或没有 tag 源 → fallback；用户已选过
  // 合法源 → 沿用；否则挑 default_on，再否则第一个。注意：派生值而不是
  // 写 state，规避 react-hooks/set-state-in-effect。
  const tagSources = useMemo(
    () => sourceList.filter((s) => s.kind === 'tag'),
    [sourceList],
  );
  const effectiveSource = useMemo(() => {
    if (!sourcesLoaded || tagSources.length === 0) return SOURCE_FALLBACK;
    if (tagSources.some((s) => s.name === source)) return source;
    return tagSources.find((s) => s.default_on)?.name ?? tagSources[0].name;
  }, [sourcesLoaded, tagSources, source]);

  // base-ui 的 Select.Root 在无值时会向 onValueChange 传 null。包一层
  // 跳过 null，避免 setSource(null) 类型不兼容 + 静默丢用户选值两种
  // 隐患。
  const onSelectSource = useCallback((v: string | null) => {
    if (v != null) setSource(v);
  }, []);

  const handleSearch = async () => {
    const trimmed = query.trim();
    if (!trimmed || !sourcesLoaded) return;
    if (tagSources.length === 0) {
      useNoticeStore.getState().push('没有可用的云音乐源', 'warn');
      return;
    }
    setLoading(true);
    setHasSearched(true);
    setResults([]);
    try {
      const res = await searchMusic({
        query: trimmed,
        sources: [effectiveSource],
        pages: {},
        limit: 20,
      });
      if (res && res.result === true) {
        const songs = Array.isArray(res.data?.songs)
          ? (res.data.songs as SearchResult[])
          : [];
        setResults(songs);
      } else if (res && res.result === false) {
        useNoticeStore
          .getState()
          .push(`搜索失败: ${res.message ?? 'unknown'}`, 'warn');
        setResults([]);
      } else {
        setResults([]);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      useNoticeStore.getState().push(`搜索失败: ${msg}`, 'warn');
    } finally {
      setLoading(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      void handleSearch();
    }
  };

  const handleClose = () => onOpenChange(false);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl max-h-[85vh] flex flex-col p-0 gap-0 overflow-hidden">
        <DialogHeader className="px-4 pt-4 pb-2 border-b border-border shrink-0">
          <DialogTitle className="text-base">搜索云音乐</DialogTitle>
        </DialogHeader>

        {/* 搜索栏：input + source Select + search */}
        <div className="flex items-center gap-2 px-4 py-3 border-b border-border shrink-0">
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="输入歌名..."
            className="h-9 text-sm flex-1"
            autoFocus
            aria-label="搜索歌名"
          />
          <Select value={effectiveSource} onValueChange={onSelectSource}>
            <SelectTrigger
              className="h-9 text-sm w-32 shrink-0"
              aria-label="选择音乐源"
              title="选择音乐源"
            >
              <SelectValue placeholder="选择源" />
            </SelectTrigger>
            <SelectContent>
              {tagSources.length === 0 ? (
                <SelectItem value={SOURCE_FALLBACK} disabled>
                  无可用源
                </SelectItem>
              ) : (
                tagSources.map((s) => (
                  <SelectItem key={s.name} value={s.name}>
                    {s.display_name}
                  </SelectItem>
                ))
              )}
            </SelectContent>
          </Select>
          <Button
            type="button"
            onClick={() => void handleSearch()}
            disabled={
              loading ||
              !query.trim() ||
              !sourcesLoaded ||
              tagSources.length === 0
            }
            className="h-9 px-3 gap-1.5"
          >
            {loading ? (
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
            ) : (
              <Search className="w-3.5 h-3.5" />
            )}
            搜索
          </Button>
        </div>

        {/* 结果区 */}
        <ScrollArea className="flex-1 min-h-0">
          {!hasSearched ? (
            <div className="h-full flex items-center justify-center text-muted-foreground p-8 text-center">
              <div className="space-y-2">
                <Search className="w-10 h-10 mx-auto opacity-30" />
                <p className="text-sm">输入歌名 + 选择音乐源，开始搜索</p>
                <p className="text-xs opacity-70">
                  单源查询；多源并搜请使用顶栏全局搜索入口。
                </p>
              </div>
            </div>
          ) : results.length === 0 && !loading ? (
            <div className="h-full flex items-center justify-center text-muted-foreground p-8 text-center">
              <div className="space-y-1">
                <Music className="w-10 h-10 mx-auto opacity-30" />
                <p className="text-sm">未找到匹配结果</p>
                <p className="text-xs opacity-70">换个源或换个关键词试试</p>
              </div>
            </div>
          ) : (
            <div className="p-3 space-y-2">
              {results.map((song, idx) => (
                <ResultRow key={`${song.source}-${song.id}-${idx}`} song={song} />
              ))}
              {loading && (
                <div className="flex items-center justify-center py-4 text-muted-foreground">
                  <Loader2 className="w-4 h-4 animate-spin mr-2" />
                  <span className="text-sm">搜索中...</span>
                </div>
              )}
            </div>
          )}
        </ScrollArea>

        <DialogFooter className="px-4 py-3 border-t border-border shrink-0 flex-row justify-between gap-2">
          <span className="text-xs text-muted-foreground tabular-nums self-center">
            {hasSearched && !loading && results.length > 0
              ? `共 ${results.length} 条`
              : ''}
          </span>
          <Button type="button" variant="ghost" size="sm" onClick={handleClose}>
            <XIcon className="w-3.5 h-3.5 mr-1.5" />
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** 单条结果。提到外层让主组件短一点 + 复用 PlayButton/Download 这种
 *  内联 JSX 块之间的样式保持一致。 */
function ResultRow({ song }: { song: SearchResult }) {
  return (
    <div
      className={cn(
        'border border-border rounded-md p-2.5 bg-card/50 hover:bg-card transition-colors',
      )}
    >
      <div className="flex items-start gap-2.5">
        <div className="w-10 h-10 rounded overflow-hidden bg-muted shrink-0 ring-1 ring-border/50">
          {song.cover ? (
            <img
              src={song.cover}
              alt={song.title || song.name}
              className="w-full h-full object-cover"
              onError={(e) => {
                (e.target as HTMLImageElement).style.display = 'none';
              }}
            />
          ) : (
            <div className="w-full h-full flex items-center justify-center text-muted-foreground">
              <Music className="w-4 h-4" />
            </div>
          )}
        </div>

        <div className="flex-1 min-w-0 space-y-0.5">
          <div className="flex items-center gap-2">
            <span
              className="text-sm font-medium leading-tight truncate"
              title={song.title || song.name}
            >
              {song.title || song.name}
            </span>
          </div>
          <div className="text-xs text-muted-foreground truncate">
            {song.artist || '未知艺术家'}
            {song.album ? ` · ${song.album}` : ''}
            {song.duration ? ` · ${formatDuration(song.duration)}` : ''}
          </div>
        </div>

        <PlayButton
          track={{
            id: `${song.source}-${song.id}`,
            url: song.url || '',
            title: song.title || song.name,
            artist: song.artist || '',
            cover: song.cover,
            durationSec:
              typeof song.duration === 'number'
                ? song.duration
                : song.duration
                  ? Number(song.duration)
                  : undefined,
            source: { kind: 'plugin', source: song.source, songId: song.id },
          }}
        />

        {song.url ? (
          <a
            href={song.url}
            target="_blank"
            rel="noopener noreferrer"
            title={`下载 ${song.title || song.name}`}
            aria-label={`下载 ${song.title || song.name}`}
            className="inline-flex items-center justify-center w-7 h-7 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground transition-colors shrink-0"
          >
            <Download className="w-3.5 h-3.5" />
          </a>
        ) : (
          <div
            className="w-7 h-7 shrink-0 flex items-center justify-center text-muted-foreground/60"
            title="暂未提供下载链接"
            aria-label="暂未提供下载链接"
          >
            <Download className="w-3.5 h-3.5 opacity-40" />
          </div>
        )}
      </div>
    </div>
  );
}

function formatDuration(d: string | number | undefined): string {
  if (d == null) return '';
  const secs = typeof d === 'string' ? parseInt(d, 10) : d;
  if (Number.isNaN(secs)) return String(d);
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}
