import { useAppStore } from '@/store/useAppStore';
import { fetchLyric } from '@/api/client';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Badge } from '@/components/ui/badge';
import { Copy, Music } from 'lucide-react';
import type { SongInfo, LyricResult } from '@/types';

export function SearchResults() {
  const {
    songList, resource, updateMusicInfo, setReloadImg,
  } = useAppStore();

  const handleCopy = async (key: string, value: string | SongInfo) => {
    if (key === 'lyric') {
      const song = value as SongInfo;
      const src = resource !== 'smart_tag' ? resource : (song.resource || resource);
      try {
        const res = await fetchLyric(song.id, src);
        if (res.result) {
          const data = res.data;
          // Kuwo returns {lyric, cover} dict
          if (data && typeof data === 'object' && data.lyric !== undefined) {
            updateMusicInfo('lyrics', data.lyric);
            if (data.cover) {
              updateMusicInfo('album_img', data.cover);
              setReloadImg(false);
              setTimeout(() => setReloadImg(true), 50);
            }
          } else {
            updateMusicInfo('lyrics', data || '');
          }
        }
      } catch {
        // ignore
      }
    } else if (key === 'album_img') {
      updateMusicInfo('album_img', value as string);
      setReloadImg(false);
      setTimeout(() => setReloadImg(true), 50);
    } else {
      updateMusicInfo(key, value as string);
    }
  };

  const handleCopyAll = (song: SongInfo) => {
    handleCopy('title', song.name);
    handleCopy('year', song.year);
    handleCopy('lyric', song);
    handleCopy('album', song.album);
    handleCopy('artist', song.artist);
    handleCopy('album_img', song.album_img);
  };

  if (songList.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
        未搜索到结果
      </div>
    );
  }

  return (
    <ScrollArea className="h-full">
      {/* Header */}
      <div className="grid grid-cols-[auto_48px_1fr_1fr_1fr_64px_48px] gap-2 px-4 py-2 text-xs text-muted-foreground border-b border-border sticky top-0 bg-card/80 backdrop-blur-sm">
        <div className="w-6" />
        <div>封面</div>
        <div>标题</div>
        <div>艺术家</div>
        <div>专辑</div>
        <div>歌词</div>
        <div>年份</div>
      </div>

      {/* Song list */}
      {songList.map((song, i) => (
        <div
          key={song.id || i}
          className="grid grid-cols-[auto_48px_1fr_1fr_1fr_64px_48px] gap-2 px-4 py-2 items-center hover:bg-accent/40 transition-colors group text-xs border-b border-border/50"
        >
          {/* Apply all button */}
          <button
            onClick={() => handleCopyAll(song)}
            className="text-muted-foreground hover:text-primary transition-colors"
            title="应用全部"
          >
            <Copy className="w-3.5 h-3.5" />
          </button>

          {/* Cover */}
          <div
            className="w-10 h-10 rounded overflow-hidden bg-muted cursor-pointer shrink-0"
            onClick={() => handleCopy('album_img', song.album_img)}
          >
            {song.album_img ? (
              <img
                src={song.album_img}
                alt=""
                className="w-full h-full object-cover"
                onError={e => { (e.target as HTMLImageElement).style.display = 'none'; }}
              />
            ) : (
              <div className="w-full h-full flex items-center justify-center text-muted-foreground">
                <Music className="w-4 h-4" />
              </div>
            )}
          </div>

          {/* Title */}
          <div
            onClick={() => handleCopy('title', song.name)}
            className="truncate cursor-pointer hover:text-primary transition-colors font-medium"
            title={song.name}
          >
            {resource === 'smart_tag' && song.score !== undefined ? (
              <Badge variant="secondary" className="mr-1 text-[10px] px-1 py-0 h-4">
                {song.score}
              </Badge>
            ) : null}
            {song.name}
          </div>

          {/* Artist */}
          <div
            onClick={() => handleCopy('artist', song.artist)}
            className="truncate cursor-pointer hover:text-primary transition-colors"
            title={song.artist}
          >
            {song.artist || '-'}
          </div>

          {/* Album */}
          <div
            onClick={() => handleCopy('album', song.album)}
            className="truncate cursor-pointer hover:text-primary transition-colors"
            title={song.album}
          >
            {song.album || '-'}
          </div>

          {/* Lyrics */}
          <div
            onClick={() => handleCopy('lyric', song)}
            className="cursor-pointer text-primary hover:underline text-center text-[11px]"
          >
            加载歌词
          </div>

          {/* Year */}
          <div
            onClick={() => handleCopy('year', song.year)}
            className="text-center cursor-pointer hover:text-primary transition-colors"
          >
            {song.year || '-'}
          </div>
        </div>
      ))}
    </ScrollArea>
  );
}
