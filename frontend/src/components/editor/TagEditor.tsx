import { useState, type CSSProperties } from 'react';
import { useAppStore } from '@/store/useAppStore';
import { updateId3, fetchId3ByTitle, uploadImage } from '@/api/client';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Label } from '@/components/ui/label';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Search, Save, Sparkles, Upload } from 'lucide-react';
import type { MusicSource, MusicTagInfo } from '@/types';
import {
  COVER_PLACEHOLDER_GRADIENTS,
  inspectCoverSrc,
  resolveCoverSrc,
  stableIdFromString,
} from '@/utils/cover';
import { toInitialChar, toTrimmedString } from '@/utils/string';

interface Props {
  onLoadFiles: (path?: string) => void;
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

/** Prominent cover shown in the song-detail header. Mirrors the row-level
 *  CoverThumb semantics byte-for-byte: same gradient palette, same
 *  array-indexed seed (`id % length`), same empty-payload + oversized
 *  short-circuit, same `imgFailed` fallback, same first-letter placeholder.
 *  The only intentional difference is the visual size (w-32 h-32 vs w-10 h-10). */
function DetailCover({ src, id, alt }: { src?: string; id: number; alt: string }) {
  const [imgFailed, setImgFailed] = useState(false);
  const { isEmptyPayload, isOversized, tooltip } = inspectCoverSrc(src);
  const showImg = !isEmptyPayload && !isOversized && !imgFailed;
  // Backend sometimes hands us `alt` as a number or another non-string —
  // `toInitialChar` coerces defensively so a stray type flip here becomes
  // a no-op instead of "X?.trim is not a function" unmounting the tree.
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

/** Header shown at the top of the song-detail panel. Big cover on the left;
 *  title/artist/album/year stacked on the right. Stable id derived from
 *  title so the same song always picks the same gradient slot in sync with
 *  the row-level CoverThumb. */
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
        <div
          className="text-lg font-semibold leading-tight truncate"
          title={headerTitle}
        >
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
        <div
          className="text-xs text-muted-foreground/80 truncate"
          title={year || '未知年份'}
        >
          <span className="text-foreground/70">年份</span>
          <span className="mx-1.5 opacity-50">·</span>
          {toTrimmedString(year) || <span className="opacity-60">未知年份</span>}
        </div>
      </div>
    </div>
  );
}

export function TagEditor({ onLoadFiles }: Props) {
  const {
    musicInfo, updateMusicInfo, fullPath, selectedFile, resource, setResource,
    showFields, fadeShowDetail, setFadeShowDetail, setSongList,
    songList, isLoading, setIsLoading,
    checkedIds,
  } = useAppStore();

  // (Removed: `<Dialog open={settingsOpen}>` + `<Dialog open={batchOpen}>` stubs
  // that lived inside the outer AppShell dialog. base-ui's DialogPrimitive.Root
  // mounts a provider chain per Root; nesting two extra Roots inside the
  // outermost one silently unmounts the React tree on detail-open, producing
  // a white screen with no dev-tools error.)

  const handleSearch = async () => {
    if (!musicInfo.title) return;
    setSongList([]);
    setFadeShowDetail(false);
    try {
      const res = await fetchId3ByTitle(musicInfo.title, resource, fullPath);
      if (res.result) {
        setSongList(res.data);
        setFadeShowDetail(true);
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
        onLoadFiles();
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
        // DetailCover re-resolves via `musicInfo.album_img`, so mutating it
        // through `updateMusicInfo` is enough to refresh the header preview —
        // no extra toggle needed.
        updateMusicInfo('album_img', res.data);
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
              onChange={e => updateMusicInfo('title', e.target.value)}
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
              onChange={e => updateMusicInfo('filename', e.target.value)}
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
              value={(musicInfo as any)[field] || ''}
              onChange={e => updateMusicInfo(field, e.target.value)}
              className="h-8 text-sm flex-1"
            />
          </div>
        );

      case 'genre':
        return (
          <div className="flex items-center gap-2">
            <Label className="w-20 shrink-0 text-xs text-muted-foreground">风格</Label>
            <Select value={musicInfo.genre || '流行'} onValueChange={v => updateMusicInfo('genre', v)}>
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
            <Select value={musicInfo.language || ''} onValueChange={v => updateMusicInfo('language', v)}>
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
            <Select value={musicInfo.album_type || ''} onValueChange={v => updateMusicInfo('album_type', v)}>
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
                onChange={e => updateMusicInfo('lyrics', e.target.value)}
                className="text-xs flex-1 min-h-[180px] font-mono"
                rows={12}
              />
            </div>
            <div className="flex items-center gap-2 ml-[88px]">
              <Switch
                checked={musicInfo.is_save_lyrics_file || false}
                onCheckedChange={v => updateMusicInfo('is_save_lyrics_file', v)}
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
              onChange={e => updateMusicInfo('comment', e.target.value)}
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
                onCheckedChange={v => updateMusicInfo('is_save_album_cover', v)}
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

  if (!selectedFile && checkedIds.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
        选择文件以编辑标签
      </div>
    );
  }

  return (
    <ScrollArea className="h-full">
      <div className="p-4 space-y-3">
        {/* Song-detail header. Shown only in single-file mode so the cover
            reflects the actual selected song rather than the batch aggregate. */}
        {selectedFile && checkedIds.length === 0 && (
          <SongHeader
            coverSrc={resolveCoverSrc(musicInfo as Partial<MusicTagInfo>)}
            title={musicInfo.title}
            filename={musicInfo.filename}
            artist={musicInfo.artist}
            album={musicInfo.album}
            year={musicInfo.year}
          />
        )}

        {/* Tag-source selector + scrape trigger. "标签源" reflects that this
            picker drives the scrape-by-title search, not playback. The right-
            side 开始刮削 button is the discoverable secondary CTA of this
            clicking it runs the same handleSearch as the per-title Search
            icon, but the placement makes the action discoverable up-front. */}
        <div className="flex items-center gap-2">
          <Label className="text-xs text-muted-foreground shrink-0">标签源</Label>
          <Select value={resource} onValueChange={v => setResource(v as MusicSource)}>
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

      {/* Sticky bottom action bar. The save button lives here (rather than
          next to the source selector at the top) so it stays reachable
          through any field scroll position without competing for visual
          space with the source picker. */}
      <div className="sticky bottom-0 bg-card/95 backdrop-blur-sm border-t border-border px-4 py-3 flex justify-end">
        <Button size="sm" className="h-8 text-xs" onClick={handleSave} disabled={isLoading}>
          <Save className="w-3.5 h-3.5 mr-1.5" />
          保存
        </Button>
      </div>
    </ScrollArea>
  );
}
