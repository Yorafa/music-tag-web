import { useAppStore } from '@/store/useAppStore';
import type { MusicTagInfo, SongInfo } from '@/types';
import { Button } from '@/components/ui/button';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Badge } from '@/components/ui/badge';
import { X, Music } from 'lucide-react';

interface Props {
  onApply: (field: keyof MusicTagInfo, value: string) => void;
}

/** Map from SongInfo keys to MusicTagInfo keys + Chinese display labels. */
const SCRAPE_FIELD_MAP: {
  songKey: keyof SongInfo;
  tagKey: keyof MusicTagInfo;
  label: string;
}[] = [
  { songKey: 'name',   tagKey: 'title',    label: '标题' },
  { songKey: 'artist', tagKey: 'artist',   label: '艺术家' },
  { songKey: 'album',  tagKey: 'album',    label: '专辑' },
  { songKey: 'year',   tagKey: 'year',     label: '年份' },
];

/** Single thumbnail for a scrape result. Falls back to a music icon when
 *  no cover URL is available (common for MusicBrainz / acoustid results). */
function ScrapeCover({ src, alt }: { src?: string; alt: string }) {
  return (
    <div className="w-10 h-10 rounded overflow-hidden bg-muted shrink-0 ring-1 ring-border/50">
      {src ? (
        <img
          src={src}
          alt={alt}
          className="w-full h-full object-cover"
          onError={(e) => { (e.target as HTMLImageElement).style.display = 'none'; }}
        />
      ) : (
        <div className="w-full h-full flex items-center justify-center text-muted-foreground">
          <Music className="w-4 h-4" />
        </div>
      )}
    </div>
  );
}

export function ScrapeResults({ onApply }: Props) {
  const songList = useAppStore((s) => s.songList);
  const setSongList = useAppStore((s) => s.setSongList);

  const handleClose = () => setSongList([]);

  if (songList.length === 0) {
    return (
      <div className="h-full flex flex-col items-center justify-center text-muted-foreground gap-2 px-4">
        <Music className="w-8 h-8 opacity-40" />
        <span className="text-sm">暂无刮削结果</span>
        <span className="text-xs opacity-60">请选择标签源后点击「开始刮削」</span>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-border shrink-0">
        <span className="text-xs font-medium">
          刮削结果
          <Badge variant="secondary" className="ml-1.5 text-[10px] px-1.5 py-0">
            {songList.length}
          </Badge>
        </span>
        <Button
          variant="ghost"
          size="icon"
          className="h-6 w-6"
          onClick={handleClose}
        >
          <X className="w-3.5 h-3.5" />
        </Button>
      </div>

      {/* Scrollable result list */}
      <ScrollArea className="flex-1">
        <div className="p-2 space-y-2">
          {songList.map((song) => (
            <div
              key={song.id}
              className="border border-border rounded-md p-2.5 space-y-2 bg-card/50 hover:bg-card transition-colors"
            >
              {/* Cover + basic info */}
              <div className="flex items-start gap-2.5">
                <ScrapeCover src={song.album_img} alt={song.name} />
                <div className="flex-1 min-w-0 space-y-0.5">
                  <div className="text-sm font-medium leading-tight truncate" title={song.name}>
                    {song.name}
                  </div>
                  <div className="text-xs text-muted-foreground truncate" title={song.artist}>
                    {song.artist || '未知艺术家'}
                  </div>
                  <div className="text-xs text-muted-foreground truncate" title={song.album}>
                    {song.album || '未知专辑'}
                    {song.year ? ` · ${song.year}` : ''}
                  </div>
                </div>
              </div>

              {/* Clickable field chips */}
              <div className="flex flex-wrap gap-1.5">
                {SCRAPE_FIELD_MAP.map(({ songKey, tagKey, label }) => {
                  const value = song[songKey];
                  if (!value) return null;
                  return (
                    <Button
                      key={tagKey}
                      variant="outline"
                      size="sm"
                      className="h-6 px-2 text-[11px] font-normal gap-1 hover:bg-primary/10 hover:text-primary hover:border-primary/40 transition-colors"
                      onClick={() => onApply(tagKey, String(value))}
                      title={`${label}: ${value}`}
                    >
                      <span className="text-muted-foreground">{label}</span>
                      <span className="max-w-[100px] truncate">{value}</span>
                    </Button>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </ScrollArea>
    </div>
  );
}
