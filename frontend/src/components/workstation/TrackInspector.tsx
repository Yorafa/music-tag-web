import { useEffect, useState, useCallback, useRef } from 'react';
import {
  Save,
  Sparkles,
  Music,
  Play,
  Pause,
  Upload,
  Search,
  Image as ImageIcon,
  Fingerprint,
  RefreshCw,
  Trash2,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { useWorklistStore } from '@/store/useWorklistStore';
import { usePlayerStore } from '@/store/usePlayerStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import {
  updateId3,
  fetchId3ByTitle,
  fetchLyric,
  uploadImage,
  getMusicId3,
} from '@/api/client';
import { resolveCoverSrc, COVER_PLACEHOLDER_GRADIENTS } from '@/utils/cover';
import type { MusicSource, MusicTagInfo, SongInfo, WorklistRow } from '@/types';

interface Props {
  row: WorklistRow | null;
}

function getInitialFormData(row: WorklistRow): Partial<MusicTagInfo> {
  const info = row.musicInfo ?? {};
  return {
    title: info.title || row.fileName.replace(/\.[^/.]+$/, '').trim(),
    artist: info.artist || '',
    album: info.album || '',
    albumartist: info.albumartist || info.artist || '',
    genre: info.genre || '流行',
    year: info.year || '',
    tracknumber: info.tracknumber || '',
    discnumber: info.discnumber || '',
    lyrics: info.lyrics || '',
    album_img: info.album_img || '',
    comment: info.comment || '',
  };
}

function TrackInspectorInner({ row }: { row: WorklistRow }) {
  const setMusicInfo = useWorklistStore((s) => s.setMusicInfo);
  const setStatus = useWorklistStore((s) => s.setStatus);
  const playTrack = usePlayerStore((s) => s.playTrack);
  const isPlaying = usePlayerStore((s) => s.isPlaying);
  const currentTrack = usePlayerStore((s) => s.currentTrack);

  const [activeTab, setActiveTab] = useState<'tags' | 'candidates' | 'lyrics' | 'cover' | 'audio'>('tags');
  const [formData, setFormData] = useState<Partial<MusicTagInfo>>(() => getInitialFormData(row));
  const [candidates, setCandidates] = useState<SongInfo[]>([]);
  const [loadingCandidates, setLoadingCandidates] = useState(false);
  const [saving, setSaving] = useState(false);
  const [selectedSource, setSelectedSource] = useState<MusicSource>('smart_tag');
  const [searchQuery, setSearchQuery] = useState(() => formData.title || row.fileName.replace(/\.[^/.]+$/, '').trim());
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Background fetch full ID3 if cover or lyrics missing
  useEffect(() => {
    const info = row.musicInfo ?? {};
    if (!info.album_img || !info.lyrics) {
      const parts = row.fullPath.split('/');
      const fileName = parts.pop() || row.fileName;
      const filePath = parts.join('/');
      getMusicId3(filePath, fileName).then((res) => {
        if (res?.data) {
          setFormData((prev) => ({
            ...prev,
            ...res.data,
            title: prev.title || res.data.title,
            artist: prev.artist || res.data.artist,
          }));
        }
      }).catch(() => {});
    }
  }, [row]);

  // Search candidate matches from cloud sources
  const handleSearchCandidates = useCallback(async (customQuery?: string) => {
    const query = customQuery || searchQuery || formData.title || row.fileName.replace(/\.[^/.]+$/, '');
    if (!query) return;

    setLoadingCandidates(true);
    try {
      const res = await fetchId3ByTitle(query, selectedSource, row.fullPath);
      const list = res?.data ?? [];
      setCandidates(list);
      if (list.length > 0) {
        useNoticeStore.getState().push(`找到 ${list.length} 个匹配候选`, 'info');
      } else {
        useNoticeStore.getState().push('未找到相关音源，可尝试更换关键词或音源', 'warn');
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      useNoticeStore.getState().push(`检索失败: ${msg}`, 'error');
    } finally {
      setLoadingCandidates(false);
    }
  }, [row, searchQuery, formData.title, selectedSource]);

  // Apply candidate metadata
  const handleApplyCandidate = (c: SongInfo) => {
    const updated: Partial<MusicTagInfo> = {
      ...formData,
      title: c.name || formData.title,
      artist: c.artist || formData.artist,
      album: c.album || formData.album,
      album_img: c.album_img || formData.album_img,
      year: c.year || formData.year,
      lyrics: c.lyric || c.lyrics || formData.lyrics,
    };
    setFormData(updated);
    useNoticeStore.getState().push(`已应用「${c.name}」候选标签`, 'info');
    setActiveTab('tags');
  };

  // Save all modified tags
  const handleSaveTags = async () => {
    setSaving(true);
    try {
      await updateId3([
        {
          file_full_path: row.fullPath,
          file_name: row.fileName,
          ...formData,
        },
      ]);
      setMusicInfo(row.fullPath, formData);
      setStatus(row.fullPath, 'scraped');
      useNoticeStore.getState().push(`已成功保存「${formData.title || row.fileName}」标签`, 'info');
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      useNoticeStore.getState().push(`保存失败: ${msg}`, 'error');
    } finally {
      setSaving(false);
    }
  };

  // Handle Cover image upload
  const handleCoverUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      const res = await uploadImage(file);
      if (res?.data) {
        setFormData((prev) => ({ ...prev, album_img: `data:image/jpeg;base64,${res.data}` }));
        useNoticeStore.getState().push('封面上传成功', 'info');
      }
    } catch {
      useNoticeStore.getState().push('封面上传失败', 'error');
    }
  };

  // Fetch online lyric
  const handleFetchLyric = async () => {
    if (!formData.title) return;
    try {
      const res = await fetchLyric(formData.title, 'netease');
      if (res?.data) {
        setFormData((prev) => ({ ...prev, lyrics: res.data }));
        useNoticeStore.getState().push('歌词检索成功并已填充', 'info');
      }
    } catch {
      useNoticeStore.getState().push('歌词检索失败', 'error');
    }
  };

  // Local audio preview playback
  const isThisPlaying = isPlaying && currentTrack?.id === row.fullPath;
  const handleTogglePlay = () => {
    const parts = row.fullPath.split('/');
    const fileName = parts.pop() || row.fileName;
    const filePath = parts.join('/');
    playTrack({
      id: row.fullPath,
      url: `/api/stream/local/?path=${encodeURIComponent(row.fullPath)}`,
      title: formData.title || row.fileName,
      artist: formData.artist || '本地音乐',
      cover: formData.album_img,
      source: { kind: 'local', fileName, filePath },
    });
  };

  const coverSrc = resolveCoverSrc(formData);

  return (
    <aside className="w-80 lg:w-96 flex flex-col h-full bg-surface-1 border-l border-border overflow-hidden select-none">
      {/* Hero Header: Big Artwork & Quick Actions */}
      <div className="p-4 border-b border-border bg-surface-2/40 space-y-3 shrink-0">
        <div className="flex items-start gap-3">
          {/* Cover Art Box with Play Trigger */}
          <div className="relative w-16 h-16 rounded-lg overflow-hidden shrink-0 bg-muted group ring-1 ring-border/80 shadow-md">
            {coverSrc ? (
              <img
                src={coverSrc}
                alt=""
                className="w-full h-full object-cover"
              />
            ) : (
              <div
                className="w-full h-full flex items-center justify-center text-white text-base font-bold"
                style={{ background: COVER_PLACEHOLDER_GRADIENTS[0] }}
              >
                {(formData.title || row.fileName).charAt(0)}
              </div>
            )}
            <button
              type="button"
              onClick={handleTogglePlay}
              className="absolute inset-0 bg-black/40 opacity-0 group-hover:opacity-100 flex items-center justify-center transition-opacity text-white"
              title="即时试听播放"
            >
              {isThisPlaying ? <Pause className="w-6 h-6" /> : <Play className="w-6 h-6 fill-white" />}
            </button>
          </div>

          {/* Quick Info & Save */}
          <div className="flex-1 min-w-0 space-y-1">
            <h3 className="text-sm font-bold text-foreground truncate leading-tight" title={formData.title}>
              {formData.title || row.fileName}
            </h3>
            <p className="text-xs text-muted-foreground truncate" title={formData.artist}>
              {formData.artist || '未知艺术家'} {formData.album ? `· ${formData.album}` : ''}
            </p>
            <div className="flex items-center gap-1.5 pt-0.5">
              <Badge variant="outline" className="text-[10px] px-1 py-0 h-4 font-mono uppercase bg-muted/60">
                {row.fileName.split('.').pop() || 'AUDIO'}
              </Badge>
              {formData.year && (
                <Badge variant="secondary" className="text-[10px] px-1 py-0 h-4 font-mono">
                  {formData.year}
                </Badge>
              )}
            </div>
          </div>
        </div>

        {/* Action Buttons Row */}
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            onClick={handleSaveTags}
            disabled={saving}
            className="flex-1 h-8 text-xs font-semibold bg-primary text-primary-foreground gap-1.5 shadow-xs"
          >
            <Save className={`w-3.5 h-3.5 ${saving ? 'animate-spin' : ''}`} />
            <span>{saving ? '正在保存…' : '保存标签'}</span>
          </Button>

          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setActiveTab('candidates');
              handleSearchCandidates();
            }}
            disabled={loadingCandidates}
            className="h-8 px-2.5 text-xs gap-1 border-primary/40 text-primary hover:bg-primary/10"
            title="全网并发刮削多源候选"
          >
            <Sparkles className={`w-3.5 h-3.5 ${loadingCandidates ? 'animate-spin' : ''}`} />
            <span>智能刮削</span>
          </Button>
        </div>
      </div>

      {/* Tabs Switcher */}
      <Tabs
        value={activeTab}
        onValueChange={(v) => setActiveTab(v as typeof activeTab)}
        className="flex-1 flex flex-col min-h-0"
      >
        <TabsList className="grid grid-cols-5 h-9 p-1 bg-surface-2 rounded-none border-b border-border shrink-0">
          <TabsTrigger value="tags" className="text-[11px] px-1 py-1">
            标签
          </TabsTrigger>
          <TabsTrigger value="candidates" className="text-[11px] px-1 py-1 relative">
            候选
            {candidates.length > 0 && (
              <span className="w-1.5 h-1.5 rounded-full bg-primary absolute top-1 right-1" />
            )}
          </TabsTrigger>
          <TabsTrigger value="lyrics" className="text-[11px] px-1 py-1">
            歌词
          </TabsTrigger>
          <TabsTrigger value="cover" className="text-[11px] px-1 py-1">
            封面
          </TabsTrigger>
          <TabsTrigger value="audio" className="text-[11px] px-1 py-1">
            指纹
          </TabsTrigger>
        </TabsList>

        {/* Tab 1: Tags Form */}
        <TabsContent value="tags" className="flex-1 overflow-hidden m-0 p-0">
          <ScrollArea className="h-full p-4">
            <div className="space-y-3 pb-8">
              <div>
                <Label className="text-xs mb-1 block">歌曲标题 (Title)</Label>
                <Input
                  value={formData.title || ''}
                  onChange={(e) => setFormData({ ...formData, title: e.target.value })}
                  className="h-8 text-xs font-medium"
                />
              </div>

              <div className="grid grid-cols-2 gap-2">
                <div>
                  <Label className="text-xs mb-1 block">艺术家 (Artist)</Label>
                  <Input
                    value={formData.artist || ''}
                    onChange={(e) => setFormData({ ...formData, artist: e.target.value })}
                    className="h-8 text-xs"
                  />
                </div>
                <div>
                  <Label className="text-xs mb-1 block">专辑 (Album)</Label>
                  <Input
                    value={formData.album || ''}
                    onChange={(e) => setFormData({ ...formData, album: e.target.value })}
                    className="h-8 text-xs"
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 gap-2">
                <div>
                  <Label className="text-xs mb-1 block">专辑艺术家 (Album Artist)</Label>
                  <Input
                    value={formData.albumartist || ''}
                    onChange={(e) => setFormData({ ...formData, albumartist: e.target.value })}
                    className="h-8 text-xs"
                  />
                </div>
                <div>
                  <Label className="text-xs mb-1 block">流派 (Genre)</Label>
                  <Input
                    value={formData.genre || ''}
                    onChange={(e) => setFormData({ ...formData, genre: e.target.value })}
                    className="h-8 text-xs"
                  />
                </div>
              </div>

              <div className="grid grid-cols-3 gap-2">
                <div>
                  <Label className="text-xs mb-1 block">年份 (Year)</Label>
                  <Input
                    value={formData.year || ''}
                    onChange={(e) => setFormData({ ...formData, year: e.target.value })}
                    className="h-8 text-xs font-mono"
                  />
                </div>
                <div>
                  <Label className="text-xs mb-1 block">音轨号 (Track#)</Label>
                  <Input
                    value={formData.tracknumber || ''}
                    onChange={(e) => setFormData({ ...formData, tracknumber: e.target.value })}
                    className="h-8 text-xs font-mono"
                  />
                </div>
                <div>
                  <Label className="text-xs mb-1 block">光盘 (Disc#)</Label>
                  <Input
                    value={formData.discnumber || ''}
                    onChange={(e) => setFormData({ ...formData, discnumber: e.target.value })}
                    className="h-8 text-xs font-mono"
                  />
                </div>
              </div>

              <div>
                <Label className="text-xs mb-1 block">备注与描述 (Comment)</Label>
                <Textarea
                  value={formData.comment || ''}
                  onChange={(e) => setFormData({ ...formData, comment: e.target.value })}
                  className="text-xs resize-none h-16"
                  placeholder="ID3 备注信息..."
                />
              </div>
            </div>
          </ScrollArea>
        </TabsContent>

        {/* Tab 2: Scrape Candidates */}
        <TabsContent value="candidates" className="flex-1 flex flex-col min-h-0 m-0 p-0">
          <div className="p-2 border-b border-border/80 bg-surface-2 flex items-center gap-1.5 shrink-0">
            <Input
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleSearchCandidates()}
              placeholder="输入歌名/关键字检索..."
              className="h-7 text-xs flex-1 bg-surface-1"
            />
            <Button
              size="sm"
              onClick={() => handleSearchCandidates()}
              disabled={loadingCandidates}
              className="h-7 px-2.5 text-xs"
            >
              <Search className={`w-3 h-3 ${loadingCandidates ? 'animate-spin' : ''}`} />
            </Button>
          </div>

          <ScrollArea className="flex-1 p-2">
            {loadingCandidates ? (
              <div className="py-12 text-center text-xs text-muted-foreground space-y-2">
                <RefreshCw className="w-6 h-6 animate-spin text-primary mx-auto" />
                <p>正在全网多源检索候选…</p>
              </div>
            ) : candidates.length === 0 ? (
              <div className="py-12 text-center text-xs text-muted-foreground space-y-2">
                <Sparkles className="w-6 h-6 text-muted-foreground/40 mx-auto" />
                <p>点击上方搜索或「智能刮削」检索候选</p>
              </div>
            ) : (
              <div className="space-y-2">
                {candidates.map((c, i) => {
                  const score = typeof c.score === 'number' ? Math.min(100, Math.round(c.score * 20)) : 85;
                  return (
                    <div
                      key={`${c.id}-${i}`}
                      className="p-2.5 rounded-lg border border-border/80 bg-surface-2 hover:border-primary/50 transition-all space-y-2"
                    >
                      <div className="flex items-start gap-2.5">
                        <div className="w-10 h-10 rounded overflow-hidden bg-muted shrink-0 ring-1 ring-border/50">
                          {c.album_img ? (
                            <img src={c.album_img} alt="" className="w-full h-full object-cover" />
                          ) : (
                            <div className="w-full h-full flex items-center justify-center bg-primary/10 text-primary text-xs font-bold">
                              {(c.name || '?').charAt(0)}
                            </div>
                          )}
                        </div>

                        <div className="flex-1 min-w-0 space-y-0.5">
                          <div className="flex items-center justify-between gap-1">
                            <span className="text-xs font-semibold text-foreground truncate">
                              {c.name}
                            </span>
                            <Badge variant="outline" className="text-[9px] px-1 h-3.5 uppercase font-mono">
                              {c.source || 'cloud'}
                            </Badge>
                          </div>
                          <p className="text-[11px] text-muted-foreground truncate">
                            {c.artist || '未知'} · {c.album || '单曲'}
                          </p>
                        </div>
                      </div>

                      <div className="flex items-center justify-between pt-1 border-t border-border/40">
                        <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
                          <span>匹配度:</span>
                          <span className="font-mono font-bold text-emerald-500">{score}%</span>
                        </div>
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => handleApplyCandidate(c)}
                          className="h-6 px-2 text-[11px] text-primary hover:bg-primary hover:text-primary-foreground font-semibold"
                        >
                          应用此标签
                        </Button>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </ScrollArea>
        </TabsContent>

        {/* Tab 3: Lyrics Editor & Sync */}
        <TabsContent value="lyrics" className="flex-1 flex flex-col min-h-0 m-0 p-3 space-y-2">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold text-foreground">LRC 歌词文本</span>
            <Button
              variant="outline"
              size="sm"
              onClick={handleFetchLyric}
              className="h-6 text-[11px] px-2 gap-1 text-primary"
            >
              <Search className="w-3 h-3" />
              <span>在线获取歌词</span>
            </Button>
          </div>
          <Textarea
            value={formData.lyrics || ''}
            onChange={(e) => setFormData({ ...formData, lyrics: e.target.value })}
            placeholder="[00:00.00] 暂无歌词或直接粘贴 LRC 歌词文本..."
            className="flex-1 font-mono text-xs leading-relaxed resize-none bg-surface-2 p-2.5"
          />
        </TabsContent>

        {/* Tab 4: Cover Artwork */}
        <TabsContent value="cover" className="flex-1 p-4 space-y-4 m-0">
          <div className="flex flex-col items-center space-y-3">
            <div className="w-44 h-44 rounded-xl overflow-hidden bg-muted ring-1 ring-border shadow-md flex items-center justify-center">
              {coverSrc ? (
                <img src={coverSrc} alt="" className="w-full h-full object-cover" />
              ) : (
                <div className="text-muted-foreground/60 flex flex-col items-center gap-1">
                  <ImageIcon className="w-8 h-8" />
                  <span className="text-xs">暂无内嵌封面</span>
                </div>
              )}
            </div>

            <input
              type="file"
              ref={fileInputRef}
              onChange={handleCoverUpload}
              accept="image/*"
              className="hidden"
            />

            <div className="flex items-center gap-2 w-full max-w-xs">
              <Button
                variant="outline"
                size="sm"
                onClick={() => fileInputRef.current?.click()}
                className="flex-1 h-8 text-xs gap-1.5"
              >
                <Upload className="w-3.5 h-3.5" />
                <span>上传本地图片</span>
              </Button>
              {formData.album_img && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setFormData({ ...formData, album_img: '' })}
                  className="h-8 text-xs text-destructive hover:text-destructive"
                >
                  <Trash2 className="w-3.5 h-3.5" />
                </Button>
              )}
            </div>
          </div>
        </TabsContent>

        {/* Tab 5: Audio info & AcoustID */}
        <TabsContent value="audio" className="flex-1 p-4 space-y-3 text-xs m-0">
          <div className="p-3 rounded-lg border border-border/80 bg-surface-2 space-y-2">
            <h4 className="font-semibold text-foreground flex items-center gap-1.5">
              <Fingerprint className="w-4 h-4 text-primary" />
              AcoustID 声学指纹
            </h4>
            <p className="text-muted-foreground text-[11px] leading-relaxed">
              基于 Chromaprint (fpcalc) 的物理波形分析，不受文件名与已有标签影响，可精准完成听歌识曲与文件查重。
            </p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setSelectedSource('acoustid');
                setActiveTab('candidates');
                handleSearchCandidates(row.fullPath);
              }}
              className="w-full h-7 text-xs text-primary border-primary/40"
            >
              计算声纹并在线识别
            </Button>
          </div>

          <div className="p-3 rounded-lg border border-border/80 bg-surface-2 space-y-1.5 font-mono text-[11px]">
            <div className="flex justify-between text-muted-foreground">
              <span>文件路径:</span>
              <span className="text-foreground truncate max-w-[180px]">{row.fullPath}</span>
            </div>
            <div className="flex justify-between text-muted-foreground">
              <span>音频格式:</span>
              <span className="text-foreground uppercase">{row.fileName.split('.').pop() || 'AUDIO'}</span>
            </div>
          </div>
        </TabsContent>
      </Tabs>
    </aside>
  );
}

export function TrackInspector({ row }: Props) {
  if (!row) {
    return (
      <aside className="w-80 lg:w-96 flex flex-col h-full bg-surface-1 border-l border-border p-6 items-center justify-center text-center space-y-3 text-muted-foreground select-none">
        <div className="w-14 h-14 rounded-2xl bg-muted/60 flex items-center justify-center text-muted-foreground/60">
          <Music className="w-7 h-7" />
        </div>
        <div className="space-y-1">
          <p className="text-sm font-semibold text-foreground">未选择曲目</p>
          <p className="text-xs text-muted-foreground/80 max-w-[220px]">
            在中间曲目列表中点击任意歌曲，即可在此即时查看与深度编辑标签、歌词、封面与多源刮削
          </p>
        </div>
      </aside>
    );
  }

  return <TrackInspectorInner key={row.fullPath} row={row} />;
}
