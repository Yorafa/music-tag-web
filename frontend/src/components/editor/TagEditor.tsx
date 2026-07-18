// Song-detail editor. Migrated off useAppStore to useEditorStore — the
// slice of state this component reads (musicInfo, selectedFile,
// fullPath, resource, etc.) lives in useEditorStore today.
//
// Batch-mode is gone: the old "no selectedFile + ≥1 checkedIds in
// FileBrowser checkbox list" branch has been removed because
// FileBrowser's checkbox list doesn't exist in the post-refactor layout.
// The editor now always operates on a single file. The placeholder
// guard drops to a single condition (`!selectedFile`) — when no row is
// open, the editor shows "选择文件以编辑标签" and nothing else.
//
// Per-row id3 hydrate path: when a Worklist row is clicked, the
// WorklistRowView's openEditor() handler sets selectedFile + fullPath +
// musicInfo from the row's lazy cache, then sets editorOpen=true.
// TagEditor mounts on that signal.

import { useState, type CSSProperties } from 'react';
import { useEditorStore, editorActions } from '@/store/useEditorStore';
import { useWorklistStore } from '@/store/useWorklistStore';
import { updateId3, fetchId3ByTitle, uploadImage } from '@/api/client';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Label } from '@/components/ui/label';
import { Search, Save, Sparkles, Upload } from 'lucide-react';
import type { MusicSource, MusicTagInfo } from '@/types';
import {
  COVER_PLACEHOLDER_GRADIENTS,
  inspectCoverSrc,
  resolveCoverSrc,
  stableIdFromString,
} from '@/utils/cover';
import { toInitialChar, toTrimmedString } from '@/utils/string';

// Read a text-shaped field off MusicTagInfo without falling back to
// `any`. The discriminated-index dance keeps the @typescript-eslint/
// no-explicit-any rule happy while preserving the runtime semantics
// of `(m)[field] || ''`. Six case branches below dispatch through
// this helper so the field-index cast + null-coerce + input-coerce
// logic lives in one place. Inlining the cast at each call would
// force a copy of this dance into every branch.
function readStringField(m: Partial<MusicTagInfo>, field: string): string {
  const v = (m as Record<string, unknown>)[field];
  if (v == null) return '';
  return typeof v === 'string' ? v : String(v);
}

const SOURCES: { id: MusicSource; name: string }[] = [
  { id: 'netease', name: '网易云' },
  { id: 'qmusic', name: 'QQ音乐' },
  { id: 'kugou', name: '酷狗' },
  { id: 'kuwo', name: '酷我' },
  { id: 'migu', name: '咪咕' },
  { id: 'musicbrainz', name: 'MusicBrainz' },
  { id: 'smart_tag', name: '智能刮削' },
];

const GENRES = ['流行', '摇滚', '说唱', '民谣', '电子', '爵士', '纯音乐', '金属', '世界音乐', '新世纪', '古典', '独立', '氛围音乐'];
const LANGUAGES = ['中文', '英文', '日文', '韩文', '泰文', '未知'];
const ALBUM_TYPES = [
  { id: 'album;compilation', name: '合集' },
  { id: 'album;live', name: '现场' },
  { id: 'album;remix', name: '混音' },
  { id: 'album;soundtrack', name: '原声' },
  { id: 'album;demo', name: '演示' },
  { id: 'album;album', name: '普通' },
  { id: 'ep', name: 'EP' },
  { id: 'single', name: '单曲' },
];

const FIELD_LABELS: Record<string, string> = {
  title: '标题', filename: '文件名', artist: '艺术家', album: '专辑',
  albumartist: '专辑艺术家', genre: '风格', language: '语言', year: '年份',
  lyrics: '歌词', comment: '描述', album_img: '封面', discnumber: '光盘编号',
  tracknumber: '音轨号', duration: '时长', bit_rate: '比特率', size: '文件大小', album_type: '专辑类型',
};

/** Prominent cover shown in the song-detail header. Mirrors the
 *  row-level CoverThumb semantics byte-for-byte: same gradient
 *  palette, same array-indexed seed (`id % length`), same empty-
 *  payload + oversized short-circuit, same `imgFailed` fallback, same
 *  first-letter placeholder. The only intentional difference is the
 *  visual size (w-32 h-32 vs w-10 h-10). */
function DetailCover({ src, id, alt }: { src?: string; id: number; alt: string }) {
  const [imgFailed, setImgFailed] = useState(false);
  const { isEmptyPayload, isOversized, tooltip } = inspectCoverSrc(src);
  const showImg = !isEmptyPayload && !isOversized && !imgFailed;
  const initial = toInitialChar(alt);
  const bg: CSSProperties = {
    background: COVER_PLACEHOLDER_GRADIENTS[id % COVER_PLACEHOLDER_GRADIENTS.length],
  };

  return (
    <div className="w-32 h-32 rounded-md overflow-hidden bg-muted shrink-0 ring-1 ring-border/60 shadow-sm">
      {showImg ? (
        <img
          src={src}
          alt="封面"
          className="w-full h-full object-cover block"
          onError={() => setImgFailed(true)}
        />
      ) : (
        <div
          className="w-full h-full flex items-center justify-center text-white font-semibold text-5xl select-none"
          style={bg}
          title={tooltip}
        >
          {initial}
        </div>
      )}
    </div>
  );
}

/** Header shown at the top of the song-detail panel. Big cover on the
 *  left; title/artist/album/year stacked on the right. Stable id
 *  derived from title so the same song always picks the same gradient
 *  slot in sync with the row-level CoverThumb. */
function SongHeader({
  coverSrc,
  title,
  filename,
  artist,
  album,
  year,
}: {
  coverSrc: string | undefined;
  title?: string;
  filename?: string;
  artist?: string;
  album?: string;
  year?: string;
}) {
  const headerTitle = title || filename || '未命名歌曲';
  const seed = toTrimmedString(title || filename);
  const coverId = stableIdFromString(seed);
  return (
    <div className="flex items-start gap-4 pb-4 border-b border-border">
      <DetailCover src={coverSrc} id={coverId} alt={headerTitle} />
      <div className="flex-1 min-w-0 space-y-1.5 pt-0.5">
        <div className="text-lg font-semibold leading-tight truncate" title={headerTitle}>
          {headerTitle}
        </div>
        <div className="text-sm text-muted-foreground truncate" title={artist || ''}>
          {artist ? (
            <>
              <span className="text-foreground/80">艺术家</span>
              <span className="mx-1.5 opacity-50">·</span>
              {artist}
            </>
          ) : (
            <span className="opacity-60">未知艺术家</span>
          )}
        </div>
        <div className="text-sm text-muted-foreground truncate" title={album || ''}>
          {album ? (
            <>
              <span className="text-foreground/80">专辑</span>
              <span className="mx-1.5 opacity-50">·</span>
              {album}
            </>
          ) : (
            <span className="opacity-60">未知专辑</span>
          )}
        </div>
        <div className="text-xs text-muted-foreground/80 truncate" title={year || '未知年份'}>
          <span className="text-foreground/70">年份</span>
          <span className="mx-1.5 opacity-50">·</span>
          {toTrimmedString(year) || <span className="opacity-60">未知年份</span>}
        </div>
      </div>
    </div>
  );
}

export function TagEditor() {
  const musicInfo = useEditorStore((s) => s.musicInfo);
  const fullPath = useEditorStore((s) => s.fullPath);
  const selectedFile = useEditorStore((s) => s.selectedFile);
  const resource = useEditorStore((s) => s.resource);
  const showFields = useEditorStore((s) => s.showFields);

  // Local loading flag — was a global on useAppStore. The save + scrape
  // calls are short-lived so the editor-local useState is sufficient
  // and avoids the cross-component re-render storm a global flag
  // would create.
  const [isLoading, setIsLoading] = useState(false);

  // fullPath 现在是「相对 MUSIC_DIR」字面量（与 filePath 同根）。
  // 后端 fetch_id3_by_title/update_id3 接 full_path：files相对路径
  // 转 disk 路径走的是同 SafeJoin(MUSIC_DIR, ...)路径，所以此处不需要
  // 任何 absolute 重构。前端 /media/<rel>/<name> 与后端 /app/media/<rel>/<name>
  // 也指向同一文件——三处一致。
  const handleSearch = async () => {
    if (!musicInfo.title) return;
    editorActions.setSongList([]);
    editorActions.setFadeShowDetail(false);
    try {
      const res = await fetchId3ByTitle(musicInfo.title, resource, fullPath);
      if (res.result) {
        editorActions.setSongList(res.data);
        editorActions.setFadeShowDetail(true);
      }
    } catch {
      // ignore
    }
  };

  const handleSave = async () => {
    if (!selectedFile) return;
    setIsLoading(true);
    try {
      const params = [{
        file_full_path: `${fullPath}`,
        ...musicInfo,
      }];
      const res = await updateId3(params);
      if (res.result) {
        // Mark the Worklist row as scraped on success. Falls through
        // silently if fullPath isn't a row id (e.g. open editor in
        // play mode — no Worklist row to mark).
        const rowId = useEditorStore.getState().fullPath;
        if (rowId) {
          useWorklistStore.getState().setStatus(rowId, 'scraped');
        }
      }
    } finally {
      setIsLoading(false);
    }
  };

  const handleImageUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      const res = await uploadImage(file);
      if (res.result) {
        // DetailCover re-resolves via `musicInfo.album_img`, so
        // mutating it through `updateMusicInfo` is enough to refresh
        // the header preview — no extra toggle needed.
        editorActions.updateMusicInfo('album_img', res.data);
      }
    } catch {
      // ignore
    }
  };

  const renderField = (field: string) => {
    switch (field) {
      case 'title':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">{FIELD_LABELS[field]}</Label>
            <Input
              value={musicInfo.title || ''}
              onChange={e => editorActions.updateMusicInfo('title', e.target.value)}
              className="h-8 text-sm flex-1"
            />
            <Button size="icon" variant="ghost" className="h-8 w-8" onClick={handleSearch}>
              <Search className="w-4 h-4" />
            </Button>
          </div>
        );

      case 'filename':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">{FIELD_LABELS[field]}</Label>
            <Input
              value={musicInfo.filename || ''}
              onChange={e => editorActions.updateMusicInfo('filename', e.target.value)}
              className="h-8 text-sm flex-1"
            />
          </div>
        );

      case 'artist':
      case 'album':
      case 'albumartist':
      case 'year':
      case 'discnumber':
      case 'tracknumber':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">{FIELD_LABELS[field]}</Label>
            <Input
              value={readStringField(musicInfo, field)}
              onChange={e => editorActions.updateMusicInfo(field, e.target.value)}
              className="h-8 text-sm flex-1"
            />
          </div>
        );

      case 'genre':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">风格</Label>
            <Select value={musicInfo.genre || '流行'} onValueChange={v => editorActions.updateMusicInfo('genre', v)}>
              <SelectTrigger className="h-8 text-sm flex-1">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {GENRES.map(g => <SelectItem key={g} value={g}>{g}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
        );

      case 'language':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">语言</Label>
            <Select value={musicInfo.language || ''} onValueChange={v => editorActions.updateMusicInfo('language', v)}>
              <SelectTrigger className="h-8 text-sm flex-1">
                <SelectValue placeholder="选择语言" />
              </SelectTrigger>
              <SelectContent>
                {LANGUAGES.map(l => <SelectItem key={l} value={l}>{l}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
        );

      case 'album_type':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">专辑类型</Label>
            <Select value={musicInfo.album_type || ''} onValueChange={v => editorActions.updateMusicInfo('album_type', v)}>
              <SelectTrigger className="h-8 text-sm flex-1">
                <SelectValue placeholder="选择类型" />
              </SelectTrigger>
              <SelectContent>
                {ALBUM_TYPES.map(a => <SelectItem key={a.id} value={a.id}>{a.name}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
        );

      case 'lyrics':
        return (
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Label className="w-20 shrink-0 text-xs text-muted-foreground">歌词</Label>
              <Textarea
                value={musicInfo.lyrics || ''}
                onChange={e => editorActions.updateMusicInfo('lyrics', e.target.value)}
                className="text-xs flex-1 min-h-[180px] font-mono"
                rows={12}
              />
            </div>
            <div className="flex items-center gap-2 ml-[88px]">
              <Switch
                checked={musicInfo.is_save_lyrics_file || false}
                onCheckedChange={v => editorActions.updateMusicInfo('is_save_lyrics_file', v)}
              />
              <Label className="text-xs text-muted-foreground">保存歌词文件</Label>
            </div>
          </div>
        );

      case 'comment':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">描述</Label>
            <Textarea
              value={musicInfo.comment || ''}
              onChange={e => editorActions.updateMusicInfo('comment', e.target.value)}
              className="text-xs flex-1"
              rows={3}
            />
          </div>
        );

      case 'album_img':
        return (
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Label className="w-20 shrink-0 text-xs text-muted-foreground">封面源</Label>
              <div className="flex items-center gap-3">
                <label className="cursor-pointer">
                  <input type="file" accept="image/*" className="hidden" onChange={handleImageUpload} />
                  <div className="flex items-center gap-1 text-xs text-primary hover:underline">
                    <Upload className="w-3 h-3" />
                    上传新封面
                  </div>
                </label>
                {musicInfo.artwork_w && (
                  <span className="text-[10px] text-muted-foreground">
                    嵌入封面：{musicInfo.artwork_w}×{musicInfo.artwork_h} · {musicInfo.artwork_size}MB
                  </span>
                )}
              </div>
            </div>
            <div className="flex items-center gap-2 ml-[88px]">
              <Switch
                checked={musicInfo.is_save_album_cover || false}
                onCheckedChange={v => editorActions.updateMusicInfo('is_save_album_cover', v)}
              />
              <Label className="text-xs text-muted-foreground">将封面写入文件元数据</Label>
            </div>
          </div>
        );

      case 'duration':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">时长</Label>
            <span className="text-sm text-muted-foreground">{musicInfo.duration || 0} s</span>
          </div>
        );

      case 'bit_rate':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">比特率</Label>
            <span className="text-sm text-muted-foreground">{musicInfo.bit_rate || 0} kbps</span>
          </div>
        );

      case 'size':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">大小</Label>
            <span className="text-sm text-muted-foreground">{musicInfo.size || 0} MB</span>
          </div>
        );

      default:
        return null;
    }
  };

  if (!selectedFile) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
        选择文件以编辑标签
      </div>
    );
  }

  return (
    <ScrollArea className="h-full">
      <div className="py-4 space-y-3">
        {/* Song-detail header — shown unconditionally in single-file
            mode (the old `selectedFile && checkedIds.length === 0`
            branch collapsed to just `selectedFile` after the batch-
            mode concept was removed).
            `filename` falls back to `selectedFile` so the header
            never reads "未命名歌曲" during the optimistic-mount
            window before /api/music_id3/ resolves (or if the fetch
            returns a Failure envelope we can't recover from). The
            scene-mapping is intentional: Worklist/PlayView's row
            click handler sets `selectedFile = row.fileName`
            synchronously, so this stays a basename in lockstep with
            whatever the file's on-disk name actually is — exactly
            what `tag.Read`'s `Filename` fallback would have returned. */}
        {selectedFile && (
          <SongHeader
            coverSrc={resolveCoverSrc(musicInfo as Partial<MusicTagInfo>)}
            title={musicInfo.title}
            filename={musicInfo.filename || selectedFile}
            artist={musicInfo.artist}
            album={musicInfo.album}
            year={musicInfo.year}
          />
        )}

        {/* Tag-source selector + scrape trigger. The right-side
            「开始刮削」button is the discoverable secondary CTA; it
            runs the same handleSearch as the per-title Search icon. */}
        <div className="flex items-center gap-2">
          <Label className="text-xs text-muted-foreground shrink-0">标签源</Label>
          <Select value={resource} onValueChange={v => editorActions.setResource(v as MusicSource)}>
            <SelectTrigger className="h-7 text-xs w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SOURCES.map(s => <SelectItem key={s.id} value={s.id}>{s.name}</SelectItem>)}
            </SelectContent>
          </Select>
          <Button
            size="sm"
            variant="secondary"
            className="ml-auto h-7 text-xs"
            onClick={handleSearch}
            disabled={isLoading}
          >
            <Sparkles className="w-3 h-3 mr-1" />
            开始刮削
          </Button>
        </div>

        {/* Editable fields */}
        <div className="space-y-2">
          {showFields.map(field => (
            <div key={field}>
              {renderField(field)}
            </div>
          ))}
        </div>

      </div>

      {/* Sticky bottom action bar. The save button lives here (rather
          than next to the source selector at the top) so it stays
          reachable through any field scroll position without
          competing for visual space with the source picker. */}
      <div className="sticky bottom-0 bg-surface-3 backdrop-blur-sm border-t border-border px-4 py-3 flex justify-end">
        <Button size="sm" className="h-8 text-xs" onClick={handleSave} disabled={isLoading}>
          <Save className="w-3.5 h-3.5 mr-1.5" />
          保存
        </Button>
      </div>
    </ScrollArea>
  );
}
