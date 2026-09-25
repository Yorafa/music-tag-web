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

  const [query, setQuery] = useState('');
  const [selectedSources, setSelectedSources] = useState<string[]>(['netease', 'qmusic']);
  const [multiSource, setMultiSource] = useState(true);
  const [singleSource, setSingleSource] = useState('netease');

  const [results, setResults] = useState<SearchResult[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasSearched, setHasSearched] = useState(false);

  useEffect(() => {
    void loadSources();
  }, [loadSources]);

  const searchableSources = useMemo(
    () => sourceList.filter((s) => s.searchable),
    [sourceList],
  );

  const toggleSource = (name: string) => {
    setSelectedSources((prev) =>
      prev.includes(name)
        ? prev.length > 1
          ? prev.filter((s) => s !== name)
          : prev
        : [...prev, name],
    );
  };

  const handleSearch = async () => {
    const q = query.trim();
    if (!q) return;

    setLoading(true);
    setHasSearched(true);
    try {
      const activeSources = multiSource ? selectedSources : [singleSource];
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
              ? selectedSources.includes(s.name)
              : singleSource === s.name;
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
              支持网易云、QQ 音乐、酷狗、酷我、咪咕、MusicBrainz 与 YouTube 音频。可即时在线试听、保存至本地 NAS 音乐库或下载到浏览器。
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
            <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5 max-w-6xl mx-auto">
              {results.map((song, i) => {
                const fileName = audioDownloadBasename(song.artist, song.title || song.name, 'ogg');
                const downloadUrl = resolveDownloadUrl(
                  { kind: 'plugin', source: song.source, songId: song.id },
                  song.url || undefined,
                  sourceList,
                  fileName,
                );
                const isDlSource = song.source === 'youtube';
                const gradIdx = gradientIdx(`${song.name}-${song.artist}`);
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
                        {song.cover ? (
                          <img
                            src={song.cover}
                            alt=""
                            className="w-full h-full object-cover"
                            loading="lazy"
                          />
                        ) : (
                          <div
                            className="w-full h-full flex items-center justify-center text-white text-xs font-bold"
                            style={{ background: COVER_PLACEHOLDER_GRADIENTS[gradIdx] }}
                          >
                            {(song.title || song.name || '?').charAt(0)}
                          </div>
                        )}
                        <div className="absolute inset-0 bg-black/30 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
                          <PlayButton
                            track={{
                              id: `${song.source}-${song.id}`,
                              url: song.url || '',
                              title: song.title || song.name,
                              artist: song.artist || '',
                              cover: song.cover,
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

                        {/* Download to Browser Action */}
                        {downloadUrl && !isDlSource && (
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
