import { useState, useRef, useCallback, useEffect } from 'react';
import { searchMusic, downloadToLibrary } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { SourcePickerModal } from '@/components/search/SourcePickerModal';
import { PlayButton } from '@/components/player/PlayButton';
import { Search, Settings2, Loader2, Music, Download, ChevronDown, FolderPlus } from 'lucide-react';
import type { SearchResult, SearchPagination } from '@/types';
import { useSourceStore } from '@/store/useSourceStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { resolveDownloadUrl } from '@/lib/streamUrl';

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
  // Per-row "加入库 in-flight" set. The backend short-circuits on a
  // per-source cache hit so the actual cp is fast, but we still want to
  // disable the button + show a spinner while waiting on the response
  // envelope so a rapid double-click doesn't fire two POSTs. Keyed by
  // `<source>:<songId>` to align with the row's React `key`.
  const [adding, setAdding] = useState<Set<string>>(new Set());
  const pushToast = useNoticeStore((s) => s.push);
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

  // handleAddToLibrary POSTs /api/download/ to land the row's preview file
  // into the on-disk library at MUSIC_DIR/<sanitized artist - title>.<ext>.
  // The server short-circuits when the per-source cache dir (from a
  // previous /api/stream preview-listen long-poll) already holds the
  // track, so re-clicking after listening is a cp, not a re-fetch. The
  // download_path naming convention here is intentionally naive — the
  // user can still rename in the editor post-download. We DON'T prompt
  // for the path on purpose: the song title is the best signal we have
  // pre-conversion, and a prompt modal would make the row action slow
  // enough to defeat the "re-click to land" UX.
  const handleAddToLibrary = useCallback(
    async (song: SearchResult) => {
      const key = `${song.source}:${song.id}`;
      if (adding.has(key)) return; // dedupe rapid double-clicks
      setAdding((prev) => new Set(prev).add(key));
      try {
        const baseName = `${song.artist ?? '未知艺术家'} - ${song.title || song.name}`;
        // strip characters that yt-dlp / filesystems consider unsafe.
        // intentionally conservative — paranoia > broken cp.
        const safe = baseName.replace(/[\\/:*?"<>|]/g, '_').replace(/\s+/g, ' ').trim();
        const resp = await downloadToLibrary({
          source: song.source,
          video_id: song.id,
          download_path: `${safe}.mp3`,
        });
        if (resp.result) {
          if (resp.skipped) {
            pushToast(`已从缓存写入：${resp.dest ?? safe}`, 'info');
          } else {
            pushToast(`已加入库：${resp.message || safe}`, 'info');
          }
        } else {
          pushToast(`加入库失败：${resp.message || '未知原因'}`, 'warn');
        }
      } catch (err) {
        const axiosErr = err as { response?: { data?: { message?: string } } };
        pushToast(`加入库失败：${axiosErr?.response?.data?.message ?? '网络错误'}`, 'warn');
      } finally {
        setAdding((prev) => {
          const next = new Set(prev);
          next.delete(key);
          return next;
        });
      }
    },
    [adding, pushToast],
  );

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

                    {/* Download row actions.
                        Two affordances now that /api/stream is generic:
                         - "下载到浏览器": the native <a download> against
                           /api/stream?src=…&id=…&as_attachment=1. The
                           backend forces `Content-Disposition: attachment`
                           so the browser writes the file to the user's
                           Downloads folder, NOT the library. For Download
                           Source rows this triggers the same enqueue +
                           long-poll as preview playback. For metadata-only
                           sources (musicbrainz / acoustid) the helper
                           returns null and the row shows a quieted
                           affordance instead of a broken link.
                         - "加入库": POST /api/download/ with a
                           download_path under MUSIC_DIR so the file lands
                           in the on-disk library. Backend short-circuits
                           when the per-source cache already holds the
                           track (so listening first makes this a cp). */}

                    {/* 下载到浏览器 — pure <a>, server pushes
                        Content-Disposition: attachment so the browser
                        doesn't try to play inline. */}
                    {(() => {
                      const dl = resolveDownloadUrl(
                        { kind: 'plugin', source: song.source, songId: song.id },
                        song.url || undefined,
                        sourceList,
                      );
                      if (dl) {
                        return (
                          <a
                            href={dl}
                            download={`${(song.title || song.name).replace(/[\\/:*?"<>|]/g, '_')}.mp3`}
                            title={`下载到浏览器：${song.title || song.name}`}
                            aria-label={`下载到浏览器：${song.title || song.name}`}
                            className="inline-flex items-center justify-center w-7 h-7 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground transition-colors shrink-0"
                          >
                            <Download className="w-3.5 h-3.5" />
                          </a>
                        );
                      }
                      return (
                        <div
                          className="w-7 h-7 shrink-0 flex items-center justify-center text-muted-foreground/60"
                          title="该来源仅提供元数据，不支持下载"
                          aria-label="暂未提供下载链接"
                        >
                          <Download className="w-3.5 h-3.5 opacity-40" />
                        </div>
                      );
                    })()}

                    {/* 加入库 — POSTs /api/download/ with download_path
                        so the gateway land to MUSIC_DIR. Only sensible
                        for DownloadSources (today: youtube). For others
                        (TagSource-only) the backend rejects the POST, so
                        we render the same placeholder guard shape — the
                        user gets a visible squelch instead of a click
                        that fails silently. */}
                    {sourceList.find((s) => s.name === song.source)?.kind === 'download' ? (
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          void handleAddToLibrary(song);
                        }}
                        disabled={adding.has(`${song.source}:${song.id}`)}
                        aria-busy={adding.has(`${song.source}:${song.id}`)}
                        title={`加入库（保存到服务器 MUSIC_DIR）：${song.title || song.name}`}
                        aria-label={`加入库：${song.title || song.name}`}
                        className={
                          'inline-flex items-center justify-center w-7 h-7 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground transition-colors shrink-0 disabled:opacity-40 disabled:cursor-not-allowed'
                        }
                      >
                        {adding.has(`${song.source}:${song.id}`) ? (
                          <Loader2 className="w-3.5 h-3.5 animate-spin" />
                        ) : (
                          <FolderPlus className="w-3.5 h-3.5" />
                        )}
                      </button>
                    ) : (
                      <div
                        className="w-7 h-7 shrink-0 flex items-center justify-center text-muted-foreground/40"
                        title="该来源仅提供试听，加入库需走下载源"
                        aria-label="该来源不支持加入库"
                      >
                        <FolderPlus className="w-3.5 h-3.5 opacity-40" />
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
