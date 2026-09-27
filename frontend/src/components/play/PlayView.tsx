// Play-mode main view — Navidrome-style single main area (decision #3: no
// sidebar) with a floating PlayTopBar above a track table; PlayerBar stays
// mounted at AppShell root (mode-independent) and owns the audio element.
//
// Layout:
//   <PlayView>                       vertical stack, fills flex-1
//     <PlayTopBar/>                  bg-surface-2; add + search + count + clear
//     <main>                         bg-surface-1; flex-1; min-h-0; overflow-auto
//       rows > 0        → <LibraryTable/>  (map useLibraryStore.getFiltered())
//       rows == 0       → <EmptyState/>    (centered CTA → onAddMusic)
//     <DirPickerDrawer/>             from Plan A; open state owned here.
//
// Click contract on a row:
//   - row body click  → openDetail(row): the one song-detail dialog,
//                       shared with 智能刮削, at every viewport width
//   - Play icon click → usePlayerStore.playTrack(local PlayerTrack) and
//                       swallow the click so the row body handler doesn't
//                       double-fire.
//

import { useState, useCallback } from 'react';
import { Play, Music, Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ScrollArea } from '@/components/ui/scroll-area';
import { PlayTopBar } from '@/components/play/PlayTopBar';
import { DirPickerDrawer } from '@/components/scraper/DirPickerDrawer';
import { useLibraryStore, type LibraryRow } from '@/store/useLibraryStore';
import { useDetailStore } from '@/store/useDetailStore';
import { usePlayerStore, type PlayerTrack } from '@/store/usePlayerStore';
import { buildMediaUrl } from '@/lib/mediaUrl';
import { COVER_PLACEHOLDER_GRADIENTS, resolveCoverSrc } from '@/utils/cover';
import { cn } from '@/lib/utils';

// Gradient placeholder index — deterministic per-row stable hash based on
// fullPath, mirroring Worklist.tsx RowCover / SearchResults CoverThumb.
function gradientIdx(fullPath: string): number {
  let h = 0;
  for (let i = 0; i < fullPath.length; i++) {
    h = (h * 31 + fullPath.charCodeAt(i)) | 0;
  }
  return Math.abs(h) % COVER_PLACEHOLDER_GRADIENTS.length;
}

// Per-row cover thumbnail. resolveCoverSrc honors embedded artwork +
// album_img + rejects oversized / empty sentinel (skill pitfall #8). When
// no cover available, fall through to gradient + first-letter of fileName.
function RowCover({ row }: { row: LibraryRow }) {
  const src = resolveCoverSrc(row.musicInfo ?? undefined);
  const [imgFailed, setImgFailed] = useState(false);

  if (src && !imgFailed) {
    return (
      <img
        src={src}
        alt=""
        loading="lazy"
        onError={() => setImgFailed(true)}
        className="w-10 h-10 rounded shrink-0 object-cover bg-surface-1"
      />
    );
  }
  const idx = gradientIdx(row.fullPath);
  const initial = row.fileName.replace(/^.*\//, '').charAt(0).toUpperCase() || '?';
  return (
    <div
      className="w-10 h-10 rounded shrink-0 flex items-center justify-center text-white text-sm font-semibold"
      style={{ background: COVER_PLACEHOLDER_GRADIENTS[idx] }}
      aria-hidden
    >
      {initial}
    </div>
  );
}

function buildLocalTrack(row: LibraryRow): PlayerTrack {
  return {
    id: `local-${row.fullPath}`,
    url: buildMediaUrl(row.fullPath),
    title: row.musicInfo?.title || row.fileName,
    artist: row.musicInfo?.artist || '未知艺术家',
    cover: resolveCoverSrc(row.musicInfo ?? undefined),
    source: { kind: 'local', fileName: row.fileName, filePath: row.fullPath },
  };
}

function LibraryTable() {
  const filtered = useLibraryStore((s) => s.getFiltered());
  const playTrack = usePlayerStore((s) => s.playTrack);
  const currentTrackId = usePlayerStore((s) => s.currentTrack?.id);
  const isPlaying = usePlayerStore((s) => s.isPlaying);

  const onPlay = useCallback(
    (row: LibraryRow, e: React.MouseEvent) => {
      e.stopPropagation();
      playTrack(buildLocalTrack(row));
    },
    [playTrack],
  );

  if (filtered.length === 0) {
    return (
      <div className="h-full flex items-center justify-center p-8 text-center">
        <div className="space-y-1">
          <Music className="w-10 h-10 mx-auto text-muted-foreground/40" />
          <p className="text-sm text-muted-foreground">没有匹配的本地音乐</p>
          <p className="text-xs text-muted-foreground/60">
            清除上面的搜索条件或添加更多目录。
          </p>
        </div>
      </div>
    );
  }

  return (
    <ScrollArea className="h-full">
      <div
        role="list"
        aria-label="本地音乐列表"
        className="divide-y divide-border"
      >
        {filtered.map((row) => {
          const isCurrent = currentTrackId === `local-${row.fullPath}`;
          return (
            <button
              key={row.id}
              type="button"
              onClick={() =>
                useDetailStore.getState().openDetail({
                  fullPath: row.fullPath,
                  fileName: row.fileName,
                  musicInfo: row.musicInfo ?? null,
                })
              }
              className={cn(
                'group w-full flex items-center gap-3 px-3 py-2 text-left transition-colors',
                'hover:bg-surface-2 focus:bg-surface-2 focus:outline-none',
                isCurrent && 'bg-surface-2/60',
              )}
            >
              <RowCover row={row} />
              <div className="flex-1 min-w-0">
                <div
                  className={cn(
                    'text-sm truncate',
                    isCurrent ? (isPlaying ? 'text-foreground' : 'text-muted-foreground') : 'text-foreground',
                  )}
                >
                  {row.musicInfo?.title || row.fileName}
                </div>
                <div className="text-xs text-muted-foreground truncate">
                  {row.musicInfo?.artist || '未知艺术家'}
                  {row.musicInfo?.album ? ` · ${row.musicInfo.album}` : ''}
                </div>
              </div>
              {/* Play icon — appears on row hover OR if currently active. */}
              <span
                role="button"
                tabIndex={-1}
                onClick={(e) => onPlay(row, e)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') onPlay(row, e as unknown as React.MouseEvent);
                }}
                className={cn(
                  'inline-flex items-center justify-center w-8 h-8 rounded-full transition-colors cursor-pointer',
                  // Revealed on hover where hover exists; always shown on a phone,
                  // where a hover-only control is an invisible one.
                  'sm:opacity-0 sm:group-hover:opacity-100 sm:focus-visible:opacity-100',
                  isCurrent && 'opacity-100',
                  isCurrent
                    ? isPlaying
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-surface-3 text-foreground'
                    : 'hover:bg-surface-3 text-foreground',
                )}
                aria-label={isCurrent && isPlaying ? '暂停' : '播放'}
                title={isCurrent && isPlaying ? '暂停' : '播放'}
              >
                <Play className="w-3.5 h-3.5" />
              </span>
            </button>
          );
        })}
      </div>
    </ScrollArea>
  );
}

function EmptyState({ onAddMusic }: { onAddMusic: () => void }) {
  return (
    <div className="h-full flex flex-col items-center justify-center p-8 text-center space-y-3">
      <Music className="w-12 h-12 text-muted-foreground/40 animate-pulse" />
      <p className="text-sm text-muted-foreground">还没有音乐</p>
      <p className="text-xs text-muted-foreground/60 max-w-xs">
        点击「添加音乐」选择本地目录，扁平展开其中的音频文件加入试听列表。
      </p>
      <Button
        type="button"
        size="sm"
        onClick={onAddMusic}
        className="gap-1.5"
      >
        <Plus className="w-3.5 h-3.5" />
        添加音乐
      </Button>
    </div>
  );
}

export function PlayView() {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const rowcount = useLibraryStore((s) => s.rows.length);

  const openAddMusic = useCallback(() => setDrawerOpen(true), []);

  return (
    <div className="flex-1 flex flex-col overflow-hidden bg-surface-1 min-h-0">
      <PlayTopBar onAddMusic={openAddMusic} />
      <main className="flex-1 min-h-0 overflow-hidden bg-surface-1">
        {rowcount > 0 ? <LibraryTable /> : <EmptyState onAddMusic={openAddMusic} />}
      </main>
      {/*
          Plan A's DirPickerDrawer owns its own multi-select tree browser
          and dispatches `useLibraryStore.getState().enqueueDirs(selected)`
          directly when the user confirms — so PlayView only has to manage
          the open state. Same prop surface it always exposed (open /
          onOpenChange / destination) so the wiring is a one-for-one swap
          from the early placeholder to the production component.
      */}
      <DirPickerDrawer
        open={drawerOpen}
        onOpenChange={setDrawerOpen}
        destination="library"
      />
    </div>
  );
}
