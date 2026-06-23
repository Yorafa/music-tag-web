import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { useAppStore } from '@/store/useAppStore';
import { getMusicId3 } from '@/api/client';
import { ScrollArea } from '@/components/ui/scroll-area';
import type { FileNode, MusicTagInfo } from '@/types';
import { makeCompareFn } from '@/utils/sortBy';
import { isAudioFile } from '@/utils/audioTypes';
import {
  COVER_PLACEHOLDER_GRADIENTS,
  inspectCoverSrc,
  resolveCoverSrc,
} from '@/utils/cover';
import { toInitialChar, toTrimmedString } from '@/utils/string';

/** Cover thumbnail — falls back to a colored initials tile when no image is
 *  available OR the image fails to load. Accepts either an HTTP URL or a
 *  `data:image/...` base64 URL produced by the backend from embedded tags.
 *  Caps the inline payload at MAX_INLINE_COVER_CHARS (300 KB) so a runaway
 *  high-res scan can't lock the renderer on decode. */
function CoverThumb({
  src,
  id,
  title,
}: {
  src?: string;
  id: number;
  title: string;
}) {
  const [imgFailed, setImgFailed] = useState(false);
  const { isEmptyPayload, isOversized, tooltip } = inspectCoverSrc(src);
  const showImg = !isEmptyPayload && !isOversized && !imgFailed;
  // Defensive coerce — backend-row title comes from `info?.album ||
  // file.name` and `info.album` can be a non-string from upstream. The
  // helper makes a type flip here a no-op rather than a crash.
  const initial = toInitialChar(title);
  const bg: CSSProperties = {
    background: COVER_PLACEHOLDER_GRADIENTS[id % COVER_PLACEHOLDER_GRADIENTS.length],
  };

  return (
    <div className="w-10 h-10 rounded overflow-hidden bg-muted shrink-0">
      {showImg ? (
        <img
          src={src}
          alt="封面"
          className="w-full h-full object-cover"
          onError={() => setImgFailed(true)}
        />
      ) : (
        <div
          className="w-full h-full flex items-center justify-center text-white font-semibold text-sm"
          style={bg}
          title={tooltip}
        >
          {initial}
        </div>
      )}
    </div>
  );
}

interface MusicRowProps {
  file: FileNode;
  filePath: string;
}

// Tailwind arbitrary-value grid template shared by header + rows so column
// alignment stays in lockstep. Cover (48px), lrc + year (64px) are fixed;
// the five text columns use `minmax(0, Xfr)` so they shrink when the panel
// is narrow instead of overflowing horizontally.
const ROW_GRID_CLASS =
  'grid-cols-[48px_minmax(0,1.5fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,2fr)_64px_64px]';

/** Single row in the current-directory song list. Hydrates its id3 lazily
 *  from the shared cache; falls back to a fetch + aborts on unmount/path change. */
function MusicRow({ file, filePath }: MusicRowProps) {
  const id3Cache = useAppStore((s) => s.id3Cache);
  const setId3CacheEntry = useAppStore((s) => s.setId3CacheEntry);
  const setSelectedFile = useAppStore((s) => s.setSelectedFile);
  const setFullPath = useAppStore((s) => s.setFullPath);
  const setMusicInfo = useAppStore((s) => s.setMusicInfo);
  const setEditorOpen = useAppStore((s) => s.setEditorOpen);

  // Cache key must be scoped to (folder, filename) — file ids are assigned
  // sequentially per `file_list` response and get reused across directories,
  // so a `String(file.id)` key would let stale cover/lyrics/title metadata
  // from a previous folder leak into the current one.
  const cacheKey = `${filePath}|${file.name}`;
  const cached = id3Cache[cacheKey];
  const hasCachedData = cached && Object.keys(cached).length > 0;
  const triedRef = useRef<boolean>(Boolean(cached));
  const [info, setInfo] = useState<Partial<MusicTagInfo> | null>(
    hasCachedData ? cached : null,
  );

  useEffect(() => {
    if (triedRef.current) return;
    triedRef.current = true;
    const ac = new AbortController();
    let active = true;
    (async () => {
      try {
        const res = await getMusicId3(filePath, file.name);
        if (!active || ac.signal.aborted) return;
        const next: Partial<MusicTagInfo> = res?.result && res.data ? res.data : {};
        setId3CacheEntry(cacheKey, next);
        setInfo(next);
      } catch {
        if (active) {
          setId3CacheEntry(cacheKey, {});
          setInfo({});
        }
      }
    })();
    return () => {
      active = false;
      ac.abort();
    };
  }, [cacheKey, file.name, filePath, setId3CacheEntry]);

  const openEditor = async () => {
    setSelectedFile(file.name);
    const full = `${filePath.replace(/\/$/, '')}/${file.name}`;
    setFullPath(full);

    // Use cached data when available; otherwise await an inline fetch so the
    // editor doesn't open with blank fields if the row hadn't finished its
    // lazy mount-fetch before the click.
    if (info && Object.keys(info).length > 0) {
      setMusicInfo(info);
      setEditorOpen(true);
      return;
    }

    try {
      const res = await getMusicId3(filePath, file.name);
      const next: Partial<MusicTagInfo> = res?.result && res.data ? res.data : {};
      setId3CacheEntry(cacheKey, next);
      setMusicInfo(next);
    } catch {
      setMusicInfo({});
    }
    setEditorOpen(true);
  };

  const title = info?.title || '—';
  const artist = info?.artist || '—';
  const album = info?.album || '—';
  const year = info?.year || '—';
  // Backend stores the embedded cover as a base64 data URL in `artwork`. Some
  // upstream search paths still fill `album_img` with an HTTP URL; the shared
  // `resolveCoverSrc` picks the embedded source and rejects the empty-payload
  // sentinel `"data:image/...,"` from mutagen's no-art fallback.
  const coverSrc = resolveCoverSrc(info);
  // Lyrics presence flag — whether the id3 record has any embedded lyric text.
  // `toTrimmedString` returns `''` for nullish input but coerces falsy
  // values like `0` to a non-empty trimmed string, so we still keep the
  // truthy guard up front to reject numeric-zero / false sentinels.
  const hasLyrics = !!(info?.lyrics && toTrimmedString(info?.lyrics).length > 0);
  const hasLrc = info?.is_save_lyrics_file === true;

  return (
    <div
      onClick={openEditor}
      className={`gap-2 px-4 py-2 items-center hover:bg-accent/40 transition-colors text-xs border-b border-border/50 cursor-pointer grid ${ROW_GRID_CLASS}`}
    >
      <CoverThumb
        src={coverSrc}
        id={file.id}
        title={toTrimmedString(info?.album) || file.name}
      />
      <div className="truncate font-medium" title={file.name}>{file.name}</div>
      <div className="truncate" title={info?.title || file.name}>{title}</div>
      <div className="truncate" title={artist}>{artist}</div>
      <div className="truncate" title={album}>{album}</div>
      <div
        className="text-center"
        title={hasLyrics ? '已嵌入歌词字段' : '无嵌入歌词'}
      >
        {hasLyrics ? (
          <span className="text-emerald-400">✓</span>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </div>
      <div
        className="text-center"
        title={hasLrc ? '本地歌词文件已存在' : '无本地歌词文件'}
      >
        {hasLrc ? (
          <span className="text-emerald-400">✓</span>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </div>
      <div className="text-center" title={year}>{year}</div>
    </div>
  );
}

export function SearchResults() {
  const treeData = useAppStore((s) => s.treeData);
  const filePath = useAppStore((s) => s.filePath);
  const sortField = useAppStore((s) => s.sortField);
  const sortDir = useAppStore((s) => s.sortDir);

  const audioFiles = useMemo(() => {
    const root = treeData[0];
    const children = root?.children ?? [];
    return children.filter((c) => c.icon !== 'icon-folder' && isAudioFile(c.name));
  }, [treeData]);

  const sortedFiles = useMemo(
    () => [...audioFiles].sort(makeCompareFn<FileNode>(sortField, sortDir)),
    [audioFiles, sortField, sortDir],
  );

  if (sortedFiles.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground text-sm p-4 text-center">
        当前文件夹内未识别到音频文件
      </div>
    );
  }

  return (
    <ScrollArea className="h-full">
      {/* Header — grid template MUST match the row template below for column alignment. */}
      <div
        className={`gap-2 px-4 py-2 text-xs text-muted-foreground border-b border-border sticky top-0 bg-card/80 backdrop-blur-sm grid ${ROW_GRID_CLASS}`}
      >
        <div className="flex items-center">封面</div>
        <div className="flex items-center">文件名</div>
        <div className="flex items-center">歌曲标题</div>
        <div className="flex items-center">艺术家</div>
        <div className="flex items-center">专辑</div>
        <div className="flex items-center justify-center">歌词</div>
        <div className="flex items-center justify-center">LRC</div>
        <div className="flex items-center justify-center">年份</div>
      </div>
      {sortedFiles.map((file) => (
        <MusicRow key={file.id} file={file} filePath={filePath} />
      ))}
    </ScrollArea>
  );
}
