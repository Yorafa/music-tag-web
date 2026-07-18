import { useState, useRef, useCallback, useEffect } from 'react';
import { searchMusic } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { SourcePickerModal } from '@/components/search/SourcePickerModal';
import { PlayButton } from '@/components/player/PlayButton';
import { Search, Settings2, Loader2, Music, Download, ChevronDown } from 'lucide-react';
import type { SearchResult, SearchPagination } from '@/types';
import { useSourceStore } from '@/store/useSourceStore';

// Static color map preserves visual consistency across restarts for the
// search-eligible sources we ship today (YouTube is intentionally
// omitted \u2014 it registers as a DownloadSource on the backend, not a
// TagSource, so it cannot appear in /api/search_music/ results).
// Unknown sources (Stage B/C plugin slots) fall through to a deterministic
// hash-derived color so the row chip still has a stable identity.
const SOURCE_COLORS: Record<string, string> = {
  netease: 'bg-red-500/15 text-red-600 dark:text-red-400',
  qmusic: 'bg-green-500/15 text-green-600 dark:text-green-400',
  kugou: 'bg-blue-500/15 text-blue-600 dark:text-blue-400',
  kuwo: 'bg-orange-500/15 text-orange-600 dark:text-orange-400',
  migu: 'bg-pink-500/15 text-pink-600 dark:text-pink-400',
  musicbrainz: 'bg-purple-500/15 text-purple-600 dark:text-purple-400',
  acoustid: 'bg-cyan-500/15 text-cyan-600 dark:text-cyan-400',
};

const FALLBACK_PALETTE = [
  'bg-amber-500/15 text-amber-600 dark:text-amber-400',
  'bg-indigo-500/15 text-indigo-600 dark:text-indigo-400',
  'bg-teal-500/15 text-teal-600 dark:text-teal-400',
  'bg-lime-500/15 text-lime-600 dark:text-lime-400',
];

function colorForSource(name: string): string {
  if (SOURCE_COLORS[name]) return SOURCE_COLORS[name];
  // djb2-style rolling hash so the same name always picks the same slot
  // across users and renders. | 0 forces 32-bit signed; Math.abs guards
  // the modulo against negatives.
  let h = 0;
  for (let i = 0; i < name.length; i++) {
    h = ((h << 5) - h + name.charCodeAt(i)) | 0;
  }
  return FALLBACK_PALETTE[Math.abs(h) % FALLBACK_PALETTE.length];
}

function formatDuration(d: string | number | undefined): string {
  if (!d) return '';
  const secs = typeof d === 'string' ? parseInt(d, 10) : d;
  if (isNaN(secs)) return String(d);
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}

export function SearchPanel() {
  // Stage A: source list + enabled set live in a single Zustand store
  // hydrated from GET /api/sources/. Drop the legacy per-component
  // localStorage dance so the picker can never go out of sync with the
  // backend's plugin registry.
  const sourceList = useSourceStore((s) => s.sources);
  const enabled = useSourceStore((s) => s.enabled);
  const sourcesLoaded = useSourceStore((s) => s.loaded);
  const loadSources = useSourceStore((s) => s.loadSources);
  const setEnabled = useSourceStore((s) => s.setEnabled);

  // Hydrate once on mount. The store no-ops on re-entry after the first
  // successful fetch, so this fires at most once per app load.
  useEffect(() => {
    void loadSources();
  }, [loadSources]);

  const [query, setQuery] = useState('');
  const [results, setResults] = useState<SearchResult[]>([]);
  const [pagination, setPagination] = useState<SearchPagination>({ pages: {}, has_more: {} });
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [hasSearched, setHasSearched] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  const enabledArr = Array.from(enabled);

  const anyHasMore = Object.values(pagination.has_more).some(Boolean);

  // Build a {name -> display_name} lookup so result rows show the user's
  // native label, not the raw plugin key. Recomputes when sourceList
  // changes (rare — only on bootstrap).
  const displayNames: Record<string, string> = Object.fromEntries(
    sourceList.map((s) => [s.name, s.display_name]),
  );

  const handleSearch = useCallback(async () => {
    const trimmed = query.trim();
    if (!trimmed || enabled.size === 0) return;

    setLoading(true);
    setHasSearched(true);
    setResults([]);
    setPagination({ pages: {}, has_more: {} });

    try {
      const res = await searchMusic({
        query: trimmed,
        sources: enabledArr,
        pages: {},
        limit: 10,
      });
      if (res.result) {
        setResults(res.data.songs || []);
        setPagination({
          pages: res.data.pages || {},
          has_more: res.data.has_more || {},
        });
      }
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [query, enabled.size, enabledArr]);

  const handleLoadMore = useCallback(async () => {
    const trimmed = query.trim();
    if (!trimmed || loadingMore || !anyHasMore) return;

    setLoadingMore(true);
    try {
      const res = await searchMusic({
        query: trimmed,
        sources: enabledArr,
        pages: pagination.pages,
        limit: 10,
      });
      if (res.result) {
        setResults((prev) => [...prev, ...(res.data.songs || [])]);
        setPagination({
          pages: res.data.pages || {},
          has_more: res.data.has_more || {},
        });
      }
    } catch {
      // ignore
    } finally {
      setLoadingMore(false);
    }
  }, [query, pagination, loadingMore, anyHasMore, enabledArr]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      handleSearch();
    }
  };

  const handleScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el || loadingMore || !anyHasMore || loading) return;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 200) {
      handleLoadMore();
    }
  }, [loadingMore, anyHasMore, loading, handleLoadMore]);

  return (
    <div className="h-full flex flex-col">
      {/* Search bar */}
      <div className="flex items-center gap-2 px-4 py-3 border-b border-border shrink-0">
        <Button
          variant="outline"
          size="sm"
          className="h-8 px-2 gap-1 shrink-0"
          onClick={() => setPickerOpen(true)}
          disabled={!sourcesLoaded}
          title={sourcesLoaded ? '选择搜索源' : '加载源中...'}
        >
          <Settings2 className="w-3.5 h-3.5" />
          <span className="text-xs">
            {enabled.size === 0 ? '选择源' : `${enabled.size} 个源`}
          </span>
        </Button>

        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="搜索歌曲..."
          className="h-8 text-sm flex-1"
        />

        <Button
          size="sm"
          className="h-8 px-3"
          onClick={handleSearch}
          disabled={loading || !query.trim() || enabled.size === 0}
        >
          {loading ? (
            <Loader2 className="w-3.5 h-3.5 animate-spin" />
          ) : (
            <Search className="w-3.5 h-3.5" />
          )}
        </Button>
      </div>

      {/* Results */}
      <div className="flex-1 min-h-0 overflow-auto" ref={scrollRef} onScroll={handleScroll}>
        {!hasSearched ? (
          <div className="h-full flex items-center justify-center text-muted-foreground">
            <div className="text-center space-y-2">
              <Search className="w-10 h-10 mx-auto opacity-30" />
              <p className="text-sm">选择搜索源，输入关键词开始搜索</p>
            </div>
          </div>
        ) : results.length === 0 && !loading ? (
          <div className="h-full flex items-center justify-center text-muted-foreground">
            <p className="text-sm">未找到匹配结果</p>
          </div>
        ) : (
          <div className="p-3 space-y-2">
            {results.map((song, idx) => {
              return (
                <div
                  key={`${song.source}-${song.id}-${idx}`}
                  className="border border-border rounded-md p-2.5 bg-card/50 hover:bg-card transition-colors"
                >
                  <div className="flex items-start gap-2.5">
                    {/* Cover */}
                    <div className="w-10 h-10 rounded overflow-hidden bg-muted shrink-0 ring-1 ring-border/50">
                      {song.cover ? (
                        <img
                          src={song.cover}
                          alt={song.name}
                          className="w-full h-full object-cover"
                          onError={(e) => { (e.target as HTMLImageElement).style.display = 'none'; }}
                        />
                      ) : (
                        <div className="w-full h-full flex items-center justify-center text-muted-foreground">
                          <Music className="w-4 h-4" />
                        </div>
                      )}
                    </div>

                    {/* Info */}
                    <div className="flex-1 min-w-0 space-y-0.5">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium leading-tight truncate" title={song.title || song.name}>
                          {song.title || song.name}
                        </span>
                        <Badge
                          variant="secondary"
                          className={`text-[10px] px-1.5 py-0 shrink-0 ${colorForSource(song.source)}`}
                          title={song.source}
                        >
                          {displayNames[song.source] || song.source}
                        </Badge>
                      </div>
                      <div className="text-xs text-muted-foreground truncate">
                        {song.artist || '未知艺术家'}
                        {song.album ? ` · ${song.album}` : ''}
                        {song.duration ? ` · ${formatDuration(song.duration)}` : ''}
                      </div>
                    </div>

                    {/* Play preview — driven by usePlayerStore so a play here
                        routes to the bottom PlayerBar. Rendered unconditionally;
                        PlayButton internally decides playability (track.url set
                        → direct; empty + source.supports_audio_url=true → /api/stream
                        fallback; metadata-only source → disabled with toast on click).
                        Keeping the row layout stable means we don't need a spacer. */}
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

                    {/* Download — wire to song.url when the plugin populated one;
                        render an info-only "browser will follow cross-origin
                        link" affordance when the URL is empty (the gateway
                        /api/stream proxy is a follow-up — when it lands, the
                        empty-url branch will become a true native download
                        against the same /api/stream endpoint the PlayButton
                        fallback hits). Same-size spacer-preserved for now. */}
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
                        title="即将支持：网关 /api/stream 代理下载"
                        aria-label="暂未提供下载链接"
                      >
                        <Download className="w-3.5 h-3.5 opacity-40" />
                      </div>
                    )}
                  </div>
                </div>
              );
            })}

            {/* Load more */}
            {anyHasMore && (
              <div className="flex justify-center py-2">
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-xs text-muted-foreground"
                  onClick={handleLoadMore}
                  disabled={loadingMore}
                >
                  {loadingMore ? (
                    <Loader2 className="w-3.5 h-3.5 animate-spin mr-1" />
                  ) : (
                    <ChevronDown className="w-3.5 h-3.5 mr-1" />
                  )}
                  加载更多
                </Button>
              </div>
            )}

            {/* All loaded indicator */}
            {!anyHasMore && results.length > 0 && (
              <p className="text-center text-xs text-muted-foreground py-2">
                已加载全部结果
              </p>
            )}

            {/* Loading indicator */}
            {loading && (
              <div className="flex items-center justify-center py-4 text-muted-foreground">
                <Loader2 className="w-4 h-4 animate-spin mr-2" />
                <span className="text-sm">搜索中...</span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Source picker modal */}
      <SourcePickerModal
        open={pickerOpen}
        onOpenChange={setPickerOpen}
        sources={sourceList}
        selected={enabledArr}
        onConfirm={setEnabled}
      />
    </div>
  );
}
