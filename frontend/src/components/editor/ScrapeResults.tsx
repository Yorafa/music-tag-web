// Scrape-result sidebar inside the song-detail Dialog. Migrated off
// useAppStore to useEditorStore — the slice it owns (songList +
// fadeShowDetail) lives entirely in useEditorStore today.
//
// Batch-mode is gone: the old `if (!selectedFile && checkedIds.length
// > 0)` lockdown has been removed because FileBrowser's checkbox list
// doesn't exist in the post-refactor layout, and the editor is now
// single-file by construction. Every chip / 应用所有 button on a
// scrape-result row directly applies to the open editor's musicInfo,
// then ALSO mirrors into the matching Worklist row's lazy musicInfo
// cache so the thumbnail + first-line preview pick up the change
// without a full /api/music_id3/ refetch.
//
// Lyric field handling: the SCRAPE_FIELD_MAP entry reads BOTH `lyric`
// (singular — Kuwo's [cover] appendix path) and `lyrics` (plural —
// bulk-fetch path). One chip covers both shapes; no second row.

import { useEditorStore, editorActions } from '@/store/useEditorStore';
import { useWorklistStore } from '@/store/useWorklistStore';
import type { MusicTagInfo, SongInfo } from '@/types';
import { Button } from '@/components/ui/button';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Badge } from '@/components/ui/badge';
import { X, Music, Sparkles } from 'lucide-react';

interface Props {
  onApply: (field: keyof MusicTagInfo, value: string) => void;
}

const SCRAPE_FIELD_MAP: {
  songKey: string;
  tagKey: keyof MusicTagInfo;
  label: string;
  altKey?: string;
}[] = [
  { songKey: 'name',   tagKey: 'title',    label: '标题' },
  { songKey: 'artist', tagKey: 'artist',   label: '艺术家' },
  { songKey: 'album',  tagKey: 'album',    label: '专辑' },
  { songKey: 'year',   tagKey: 'year',     label: '年份' },
  { songKey: 'lyric',  tagKey: 'lyrics',   label: '歌词', altKey: 'lyrics' },
];

/** Single thumbnail for a scrape result. Falls back to a music icon
 *  when no cover URL is available (common for MusicBrainz / acoustid
 *  results). */
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

/** Read a field off a SongInfo per the SCRAPE_FIELD_MAP entry. Caller
 *  sometimes delivers `lyric` (singular) and sometimes `lyrics`; the
 *  `altKey` lets one map entry cover both shapes so we don't need to
 *  enumerate a second row.
 *
 *  Two-step `unknown → Record<string, unknown>` cast: required because
 *  SongInfo's numeric fields (artwork_w/artwork_h/artwork_size) are
 *  typed as `number` and don't satisfy the `Record<string, unknown>`
 *  overlap rule. Going through `unknown` first is TS's blessed idiom
 *  for untyped-indexing with explicit acknowledgement. */
function readScrapeValue(song: SongInfo, songKey: string, altKey?: string): string {
  const obj = song as unknown as Record<string, unknown>;
  const primary = obj[songKey];
  if (primary != null && String(primary).length > 0) return String(primary);
  if (altKey) {
    const alt = obj[altKey];
    if (alt != null && String(alt).length > 0) return String(alt);
  }
  return '';
}

export function ScrapeResults({ onApply }: Props) {
  const songList = useEditorStore((s) => s.songList);
  const handleClose = () => editorActions.setSongList([]);

  // Apply writes to BOTH the editor (so the visible form updates)
  // AND the Worklist row's lazy musicInfo cache (so the row's
  // thumbnail + first-line preview pick up the change instantly). The
  // row id == editor's fullPath by convention set in WorklistRowView's
  // openEditor handler.

  // Inline apply that mirrors into the Worklist cache. The component
  // is fed onApply via props (so the parent can decide whether to
  // merge into a shared store or a per-row store); we extend it here
  // so the row's lazy cache stays in lockstep with the editor.
  const applyAndCache = (field: keyof MusicTagInfo, value: string): void => {
    onApply(field, value);
    const rowId = useEditorStore.getState().fullPath;
    if (rowId) {
      useWorklistStore.getState().setMusicInfo(rowId, { [field]: value });
    }
  };

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

              {/* Clickable field chips. Apply always works (no batch-
                  mode lockdown in the single-file editor). */}
              <div className="flex flex-wrap gap-1.5">
                {SCRAPE_FIELD_MAP.map(({ songKey, tagKey, label, altKey }) => {
                  const value = readScrapeValue(song, songKey, altKey);
                  if (!value) return null;
                  return (
                    <Button
                      key={tagKey}
                      variant="outline"
                      size="sm"
                      className="h-6 px-2 text-[11px] font-normal gap-1 hover:bg-primary/10 hover:text-primary hover:border-primary/40 transition-colors"
                      onClick={() => applyAndCache(tagKey, value)}
                      title={
                        tagKey === 'lyrics' && value.length > 80
                          ? `${label} · ${value.length} chars`
                          : `${label}: ${value}`
                      }
                    >
                      <span className="text-muted-foreground">{label}</span>
                      <span className="max-w-[100px] truncate">{value}</span>
                    </Button>
                  );
                })}
              </div>

              {/* One-click apply-all — pops every non-empty fieldwork
                  onto the open editor in a single batch. Same altKey
                  fallback as the per-field chip. */}
              <div className="flex justify-end pt-1 border-t border-border/40">
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-6 px-2 text-[11px] gap-1 text-primary hover:text-primary hover:bg-primary/10"
                  onClick={() => {
                    SCRAPE_FIELD_MAP.forEach(({ songKey, tagKey, altKey }) => {
                      const v = readScrapeValue(song, songKey, altKey);
                      if (v.length > 0) applyAndCache(tagKey, v);
                    });
                  }}
                  title="把这一行所有可填充字段一次性写到正在编辑的歌曲上"
                >
                  <Sparkles className="w-3 h-3" />
                  应用所有
                </Button>
              </div>
            </div>
          ))}
        </div>
      </ScrollArea>
    </div>
  );
}
