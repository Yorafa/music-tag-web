import { useEffect, useMemo, useState } from 'react';
import { searchMusic, downloadToLibrary } from '@/api/client';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import { ScrollArea } from '@/components/ui/scroll-area';
import { PlayButton } from '@/components/player/PlayButton';
import { useSourceStore } from '@/store/useSourceStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { resolveDownloadUrl, audioDownloadBasename } from '@/lib/streamUrl';
import { formatDuration, toSeconds } from '@/utils/duration';
import { searchCoverForTrack } from '@/components/search/searchCover';
import {
  loadCloudSearchSnapshot,
  reconcileSnapshot,
  saveCloudSearchResults,
  saveCloudSearchSettings,
} from '@/lib/cloudSearchCache';
import { COVER_PLACEHOLDER_GRADIENTS } from '@/utils/cover';
import type { SearchResult } from '@/types';
import {
  Search,
  Loader2,
  Music,
  Download,
  FolderPlus,
  Sparkles,
  Layers,
  History,
} from 'lucide-react';
import { cn } from '@/lib/utils';

function gradientIdx(text: string): number {
  let h = 0;
  for (let i = 0; i < text.length; i++) {
    h = (h * 31 + text.charCodeAt(i)) | 0;
  }
  return Math.abs(h) % COVER_PLACEHOLDER_GRADIENTS.length;
}

export function CloudSearchView() {
  const sourceList = useSourceStore((s) => s.sources);
  const loadSources = useSourceStore((s) => s.loadSources);

  // Restored from the last visit, not reset. The sources, the single/multi
  // mode, the query AND the result list all come back: a five-source
  // fan-out is a real network cost, and re-issuing it to look at the same
  // list again is the waste. `restored` records that what is on screen is
  // the previous search rather than a fresh one, so the panel can say so —
  // 搜索音源 is what actually re-runs it.
  const [restored] = useState(() => loadCloudSearchSnapshot());
  const [query, setQuery] = useState(restored.query);
  const [selectedSources, setSelectedSources] = useState<string[]>(restored.selectedSources);
  const [multiSource, setMultiSource] = useState(restored.multiSource);
  const [singleSource, setSingleSource] = useState(restored.singleSource);

  const [results, setResults] = useState<SearchResult[]>(restored.results);
  const [loading, setLoading] = useState(false);
  const [hasSearched, setHasSearched] = useState(restored.results.length > 0);
  const [fromCache, setFromCache] = useState(restored.results.length > 0);

  useEffect(() => {
    void loadSources();
  }, [loadSources]);

  const searchableSources = useMemo(
    () => sourceList.filter((s) => s.searchable),
    [sourceList],
  );

  const offeredSources = useMemo(
    () => searchableSources.map((s) => s.name),
    [searchableSources],
  );

  // What the search actually uses, narrowed to sources this build has.
  //
  // The stored selection can name a plugin that was renamed or dropped, and
  // searching a name the gateway does not know returns nothing — the panel
  // then says "没有匹配的云端歌曲", which reads as "this song does not
  // exist" rather than "that source is gone". singleSource is the sharper
  // case: in single-source mode it IS the request, so a dead name there
  // means that mode silently searches nothing at all.
  //
  // Derived rather than corrected in an effect, which is the version that
  // would render one dead chip as selected for a frame before removing it.
  // The loader cannot do this narrowing either — on the first render
  // `sourceList` is still empty, and reconciling against nothing would
  // wipe the selection the snapshot just restored.
  const live = useMemo(
    () =>
      reconcileSnapshot(
        { query, multiSource, singleSource, selectedSources, results },
        offeredSources,
      ),
    [query, multiSource, singleSource, selectedSources, results, offeredSources],
  );

  // Settings and results are written separately. Toggling a source chip
  // must not re-serialise — or worse, clear — the result list the user
  // came back to look at.
  useEffect(() => {
    saveCloudSearchSettings({ query, multiSource, singleSource, selectedSources });
  }, [query, multiSource, singleSource, selectedSources]);

  // Results are persisted only when a search actually produced them, so a
  // failure does not overwrite the last good list with an empty one.
  useEffect(() => {
    if (fromCache) return;
    saveCloudSearchResults(results);
  }, [fromCache, results]);

  const toggleSource = (name: string) => {
    // Off `live`, not the raw state. After reconcile the raw list can still
    // hold a name this build dropped, and counting THOSE against "at least
    // one source must stay" lets the user click away their only real
    // source — the chip row then shows two unrelated sources selected,
    // with the click they made nowhere in it.
    const current = live.selectedSources;
    setSelectedSources(
      current.includes(name)
        ? current.length > 1
          ? current.filter((s) => s !== name)
          : current
        : [...current, name],
    );
  };

  const handleSearch = async () => {
    const q = query.trim();
    if (!q) return;

    setLoading(true);
    setHasSearched(true);
    setFromCache(false);
    try {
      const activeSources = multiSource ? live.selectedSources : [live.singleSource];
      const res = await searchMusic({
        query: q,
        sources: activeSources,
        pages: {},
        limit: 25,
      });
      if (res.result) {
        setResults(res.data.songs || []);
      } else {
        useNoticeStore.getState().push(`搜索失败: ${res.message || '未知错误'}`, 'warn');
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      useNoticeStore.getState().push(`网络搜索异常: ${msg}`, 'error');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex-1 flex flex-col overflow-hidden bg-surface-1 min-h-0">
      {/* Top Search Controls Bar */}
      <div className="p-4 glass-panel border-b border-border/80 space-y-3 shrink-0">
        <div className="flex flex-col sm:flex-row gap-2.5 max-w-4xl">
          {/* Search Input Bar */}
          <div className="relative flex-1">
            <Search className="w-4 h-4 absolute left-3 top-3 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
              placeholder="输入歌曲名、歌手、专辑进行全网检索..."
              className="h-10 pl-9 pr-3 text-sm rounded-xl bg-background/80"
              autoFocus
            />
          </div>

          <Button
            onClick={handleSearch}
            disabled={loading || !query.trim()}
            className="h-10 px-5 rounded-xl gap-2 shadow-sm font-medium shrink-0"
          >
            {loading ? (
              <Loader2 className="w-4 h-4 animate-spin" />
            ) : (
              <Search className="w-4 h-4" />
            )}
            <span>搜索音源</span>
          </Button>
        </div>

        {/* Source Picker Chips */}
        <div className="flex flex-wrap items-center gap-2 pt-1 text-xs">
          <div className="flex items-center gap-1 text-muted-foreground mr-1">
            <Layers className="w-3.5 h-3.5" />
            <span>搜索来源:</span>
          </div>

          {searchableSources.map((s) => {
            const isSelected = multiSource
              ? live.selectedSources.includes(s.name)
              : live.singleSource === s.name;
            return (
              <button
                key={s.name}
                type="button"
                onClick={() => {
                  if (multiSource) {
                    toggleSource(s.name);
                  } else {
                    setSingleSource(s.name);
                  }
                }}
                className={cn(
                  'px-2.5 py-1 rounded-lg text-xs font-medium transition-all border flex items-center gap-1.5',
                  isSelected
                    ? 'bg-primary text-primary-foreground border-primary shadow-xs'
                    : 'bg-muted/40 text-muted-foreground border-border hover:bg-muted/70 hover:text-foreground',
                )}
              >
                <span>{s.display_name || s.name}</span>
                {s.kind === 'download' && (
                  <span className="text-[9px] opacity-75 px-1 py-0.2 bg-black/20 rounded">
                    下载
                  </span>
                )}
              </button>
            );
          })}

          <div className="ml-auto text-[11px] text-muted-foreground flex items-center gap-2">
            <button
              type="button"
              onClick={() => setMultiSource(!multiSource)}
              className="underline hover:text-foreground transition-colors"
            >
              {multiSource ? '切换为单源模式' : '切换为多源并发模式'}
            </button>
          </div>
        </div>
      </div>

      {/* Main Results Container */}
      <main className="flex-1 min-h-0 overflow-hidden p-4">
        {loading ? (
          <div className="h-full flex flex-col items-center justify-center p-8 text-center space-y-2">
            <Loader2 className="w-8 h-8 animate-spin text-primary" />
            <p className="text-sm font-medium text-foreground">正在全网并发检索音源…</p>
            <p className="text-xs text-muted-foreground">正在聚合各平台音源与高保真元数据</p>
          </div>
        ) : !hasSearched ? (
          <div className="h-full flex flex-col items-center justify-center p-8 text-center space-y-3">
            <div className="w-14 h-14 rounded-2xl bg-primary/10 text-primary flex items-center justify-center">
              <Sparkles className="w-7 h-7" />
            </div>
            <p className="text-sm font-semibold text-foreground">跨平台多源音乐云端检索</p>
            <p className="text-xs text-muted-foreground max-w-md">
              支持自定义源的搜索与下载。可即时在线试听、保存至本地 NAS 音乐库或下载到浏览器。
            </p>
          </div>
        ) : results.length === 0 ? (
          <div className="h-full flex flex-col items-center justify-center p-8 text-center space-y-2">
            <Music className="w-10 h-10 text-muted-foreground/40" />
            <p className="text-sm text-muted-foreground">未找到匹配的云端歌曲</p>
            <p className="text-xs text-muted-foreground/60">尝试切换搜索源或更换搜索关键字</p>
          </div>
        ) : (
          <ScrollArea className="h-full pr-2">
            {/* Say what these rows are. A list that is already on screen
                when the page opens looks exactly like one that was just
                fetched, and the difference matters: 试听 and 入库 both act
                on a row, and a result from last week may name a track
                that has since been pulled. The button above is the way to
                actually re-run it. */}
            {fromCache && (
              <div
                className="max-w-6xl mx-auto mb-2.5 rounded-lg border border-border/80 bg-surface-2/60 px-3 py-2 text-[11px] text-muted-foreground flex items-center gap-2"
                data-testid="cloud-search-restored"
              >
                <History className="w-3.5 h-3.5 shrink-0" />
                <span className="min-w-0 flex-1">
                  上次搜「{query}」的结果
                  {results.length > 0 ? `（${results.length} 条）` : ''}，已保留。要看最新的点「搜索音源」。
                </span>
              </div>
            )}
            <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5 max-w-6xl mx-auto">
              {results.map((song, i) => {
                const fileName = audioDownloadBasename(song.artist, song.title || song.name, 'ogg');
                const downloadUrl = resolveDownloadUrl(
                  { kind: 'plugin', source: song.source, songId: song.id },
                  song.url || undefined,
                  sourceList,
                  fileName,
                );
                const gradIdx = gradientIdx(`${song.name}-${song.artist}`);
                // The plugins send album_img, not cover — reading song.cover
                // here is why every card showed a letter placeholder.
                const cover = searchCoverForTrack(song);
                // Empty for sources that report no length (MusicBrainz
                // search carries none), which is why it is interpolated
                // rather than rendered unconditionally.
                const duration = formatDuration(song.duration);

                return (
                  <Card
                    key={`${song.source}-${song.id}-${i}`}
                    className="bg-surface-2/70 hover:bg-surface-2 transition-all border-border/80 hover:border-primary/40 group overflow-hidden"
                  >
                    <CardContent className="p-3 flex items-center gap-3">
                      {/* Album Cover Thumbnail */}
                      <div className="relative w-12 h-12 rounded-lg overflow-hidden shrink-0 bg-muted flex items-center justify-center">
                        {cover ? (
                          <img
                            src={cover}
                            alt=""
                            className="w-full h-full object-cover"
                            loading="lazy"
                            // A cover host can 404 or hotlink-block after
                            // the row is drawn; fall back to the gradient
                            // rather than leaving a broken image icon.
                            onError={(e) => {
                              (e.currentTarget as HTMLImageElement).style.display = 'none';
                            }}
                          />
                        ) : (
                          <div
                            className="w-full h-full flex items-center justify-center text-white text-xs font-bold"
                            style={{ background: COVER_PLACEHOLDER_GRADIENTS[gradIdx] }}
                          >
                            {(song.title || song.name || '?').charAt(0)}
                          </div>
                        )}
                        <div className="absolute inset-0 bg-black/30 opacity-100 sm:opacity-0 sm:group-hover:opacity-100 transition-opacity flex items-center justify-center">
                          <PlayButton
                            track={{
                              id: `${song.source}-${song.id}`,
                              url: song.url || '',
                              title: song.title || song.name,
                              artist: song.artist || '',
                              cover,
                              durationSec: toSeconds(song.duration) ?? undefined,
                              source: { kind: 'plugin', source: song.source, songId: song.id },
                            }}
                          />
                        </div>
                      </div>

                      {/* Info body */}
                      <div className="flex-1 min-w-0 space-y-0.5">
                        <div className="text-xs font-semibold text-foreground truncate flex items-center gap-1.5">
                          <span className="truncate">{song.title || song.name}</span>
                          <Badge
                            variant="outline"
                            className="text-[9px] px-1 py-0 h-4 shrink-0 bg-muted/50 text-muted-foreground uppercase font-mono"
                          >
                            {song.source}
                          </Badge>
                        </div>
                        <div className="text-[11px] text-muted-foreground truncate">
                          {song.artist || '未知艺术家'}
                          {song.album ? ` · ${song.album}` : ''}
                          {duration ? ` · ${duration}` : ''}
                        </div>
                      </div>

                      {/* Action Buttons */}
                      <div className="flex items-center gap-1 shrink-0">
                        {/* Download to Library Action */}
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={async () => {
                            try {
                              const dir = (localStorage.getItem('settings.downloadPath') || '')
                                .replace(/^\/+|\/+$/g, '')
                                .replace(/\.\./g, '');
                              const dlPath = dir ? `${dir}/${fileName}` : fileName;
                              const res = await downloadToLibrary({
                                source: song.source,
                                video_id: song.id,
                                download_path: dlPath,
                                extra_audio_format: 'ogg',
                              });
                              if (res.result) {
                                useNoticeStore
                                  .getState()
                                  .push(`已提交入库下载: ${song.title || song.name}`, 'info');
                              } else {
                                useNoticeStore
                                  .getState()
                                  .push(`下载失败: ${res.message || '未知错误'}`, 'warn');
                              }
                            } catch (e) {
                              const msg = e instanceof Error ? e.message : String(e);
                              useNoticeStore
                                .getState()
                                .push(`加入下载失败: ${msg}`, 'error');
                            }
                          }}
                          className="h-8 px-2 text-xs text-muted-foreground hover:text-foreground gap-1"
                          title="下载并加入本地音乐库 (加入 NAS 曲库)"
                        >
                          <FolderPlus className="w-3.5 h-3.5" />
                          <span className="hidden lg:inline text-[11px]">入库</span>
                        </Button>

                        {/* Download to Browser Action.
                            No per-source exclusion: a YouTube row carries
                            no `url` at all (the plugin returns id + cover
                            only), so resolveDownloadUrl falls through to
                            the /api/stream/ proxy, which is the one path
                            that actually works for it — the gateway
                            enqueues a yt-dlp task, long-polls, and answers
                            with Content-Disposition: attachment. Hiding
                            the button for YouTube therefore left those
                            rows with no way to download at all. */}
                        {downloadUrl && (
                          <a
                            href={downloadUrl}
                            download={fileName}
                            className="inline-flex items-center justify-center h-8 px-2 rounded-md hover:bg-muted/80 text-muted-foreground hover:text-foreground text-xs gap-1 transition-colors"
                            title="直接下载音频到浏览器"
                          >
                            <Download className="w-3.5 h-3.5" />
                          </a>
                        )}
                      </div>
                    </CardContent>
                  </Card>
                );
              })}
            </div>
          </ScrollArea>
        )}
      </main>
    </div>
  );
}
