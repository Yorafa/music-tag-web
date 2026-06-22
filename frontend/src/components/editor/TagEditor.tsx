import { useState } from 'react';
import { useAppStore } from '@/store/useAppStore';
import { updateId3, batchUpdateId3, fetchId3ByTitle, uploadImage } from '@/api/client';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Label } from '@/components/ui/label';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Search, Save, RefreshCw, Sparkles, Upload } from 'lucide-react';
import type { MusicSource } from '@/types';

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

export function TagEditor({ onLoadFiles }: Props) {
  const {
    musicInfo, updateMusicInfo, fullPath, selectedFile, resource, setResource,
    showFields, setShowFields, fadeShowDetail, setFadeShowDetail, setSongList,
    songList, isLoading, setIsLoading, reloadImg, setReloadImg,
    checkedIds, musicInfoManual, selectAutoMode, sourceList, setSourceList,
    tidyFormData, setTidyFormData, setSelectAutoMode,
  } = useAppStore();

  const [settingsOpen, setSettingsOpen] = useState(false);
  const [batchOpen, setBatchOpen] = useState(false);
  const [tidyOpen, setTidyOpen] = useState(false);

  const handleSearch = async () => {
    if (!musicInfo.title) return;
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
        updateMusicInfo('album_img', res.data);
        setReloadImg(false);
        setTimeout(() => setReloadImg(true), 50);
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
              <Label className="w-20 shrink-0 text-xs text-muted-foreground">封面</Label>
              <div className="flex items-center gap-3">
                {musicInfo.album_img && reloadImg && (
                  <img
                    src={musicInfo.album_img}
                    alt="cover"
                    className="w-16 h-16 rounded-md object-cover border border-border"
                    onError={e => { (e.target as HTMLImageElement).style.display = 'none'; }}
                  />
                )}
                <label className="cursor-pointer">
                  <input type="file" accept="image/*" className="hidden" onChange={handleImageUpload} />
                  <div className="flex items-center gap-1 text-xs text-primary hover:underline">
                    <Upload className="w-3 h-3" />
                    上传
                  </div>
                </label>
                {musicInfo.artwork_w && (
                  <span className="text-[10px] text-muted-foreground">
                    {musicInfo.artwork_w}×{musicInfo.artwork_h} {musicInfo.artwork_size}MB
                  </span>
                )}
              </div>
            </div>
            <div className="flex items-center gap-2 ml-[88px]">
              <Switch
                checked={musicInfo.is_save_album_cover || false}
                onCheckedChange={v => updateMusicInfo('is_save_album_cover', v)}
              />
              <Label className="text-xs text-muted-foreground">保存封面图片</Label>
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
        {/* Batch mode */}
        {checkedIds.length > 0 && (
          <div className="space-y-2 pb-3 border-b border-border">
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => setBatchOpen(true)}>
                <RefreshCw className="w-3 h-3 mr-1" />
                手动批量
              </Button>
              <Button size="sm" variant="outline" onClick={() => {/* batch auto */}}>
                <Sparkles className="w-3 h-3 mr-1" />
                自动刮削
              </Button>
              <Button size="sm" variant="outline" onClick={() => setTidyOpen(true)}>
                整理文件夹
              </Button>
            </div>
          </div>
        )}

        {/* Source selector */}
        <div className="flex items-center gap-2">
          <Label className="text-xs text-muted-foreground shrink-0">音源</Label>
          <Select value={resource} onValueChange={v => setResource(v as MusicSource)}>
            <SelectTrigger className="h-7 text-xs w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SOURCES.map(s => <SelectItem key={s.id} value={s.id}>{s.name}</SelectItem>)}
            </SelectContent>
          </Select>
          <Button size="sm" className="ml-auto h-7 text-xs" onClick={handleSave} disabled={isLoading}>
            <Save className="w-3 h-3 mr-1" />
            保存
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

      {/* Settings Dialog */}
      <Dialog open={settingsOpen} onOpenChange={setSettingsOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader><DialogTitle>编辑设置</DialogTitle></DialogHeader>
          <div className="space-y-3 py-4">
            <div>
              <Label className="text-xs mb-1 block">音源</Label>
              <Select value={resource} onValueChange={v => setResource(v as MusicSource)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>{SOURCES.map(s => <SelectItem key={s.id} value={s.id}>{s.name}</SelectItem>)}</SelectContent>
              </Select>
            </div>
            <div>
              <Label className="text-xs mb-1 block">显示字段</Label>
              <div className="flex flex-wrap gap-1">
                {Object.keys(FIELD_LABELS).filter(k => !['duration', 'bit_rate', 'size'].includes(k)).map(k => (
                  <Badge
                    key={k}
                    variant={showFields.includes(k) ? 'default' : 'outline'}
                    className="cursor-pointer text-[10px]"
                    onClick={() => {
                      if (showFields.includes(k)) {
                        setShowFields(showFields.filter(f => f !== k));
                      } else {
                        setShowFields([...showFields, k]);
                      }
                    }}
                  >
                    {FIELD_LABELS[k]}
                  </Badge>
                ))}
              </div>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {/* Batch dialog placeholder */}
      <Dialog open={batchOpen} onOpenChange={setBatchOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>批量修改</DialogTitle></DialogHeader>
          <div className="text-sm text-muted-foreground py-4">
            批量修改功能开发中...
          </div>
        </DialogContent>
      </Dialog>
    </ScrollArea>
  );
}
