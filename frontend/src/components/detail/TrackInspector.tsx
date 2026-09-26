// The song-detail surface: 标签 / 候选 / 歌词 / 封面 / 指纹 for one
// local file, plus the 智能刮削 action. Rendered inside
// TrackDetailDialog, which is the only place it appears — it used to be
// the scraper view's right-hand column, but a column cannot exist on a
// phone, so the same content now opens as a dialog at every width and
// the phone gets the identical surface rather than a different one.
//
// Lives in components/detail/ rather than components/workstation/ because
// it is no longer owned by the scraper: 音乐库 opens the same dialog.
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
import { TAB_BAR_CLASS } from '@/components/detail/tabBar';
import { useWorklistStore } from '@/store/useWorklistStore';
import { useLibraryStore } from '@/store/useLibraryStore';
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
import {
  renamedPathFromUpdate,
  sidecarWarningsFromUpdate,
  baseNameOf,
} from '@/components/detail/renameResult';
import { useDetailStore } from '@/store/useDetailStore';
import type { MusicSource, MusicTagInfo, SongInfo } from '@/types';
import type { DetailTarget } from '@/store/useDetailStore';

interface Props {
  /** The file being inspected, or null when the dialog has no target —
   *  only reachable if the row was removed from its store while the
   *  dialog was open, so the empty state is a guard, not a screen. */
  row: DetailTarget | null;
}

function getInitialFormData(row: DetailTarget): Partial<MusicTagInfo> {
  const info = row.musicInfo ?? {};
  return {
    // Seeded from the actual file, not from any cached tag: `filename` is
    // a rename instruction, and sending an empty or stale value is a
    // request to move the file. An untouched field resolves to the same
    // path and the handler skips the rename.
    filename: row.fileName,
    title: info.title || row.fileName.replace(/\.[^/.]+$/, '').trim(),
    artist: info.artist || '',
    album: info.album || '',
    albumartist: info.albumartist || info.artist || '',      // Seeded empty, never with an invented default. This form is spread
      // into the update payload, so a placeholder here is a placeholder
      // written to the file: opening any track that had no genre and
      // pressing save stamped 流行 on it. An empty field clears the tag,
      // which is the opposite of what "I didn't touch this" should mean —
      // but a track with no genre has nothing to lose, and unlike the
      // auto-scrape path there is a real user looking at the field.
      genre: info.genre || '',
    year: info.year || '',
    tracknumber: info.tracknumber || '',
    discnumber: info.discnumber || '',
    lyrics: info.lyrics || '',
    album_img: info.album_img || '',
    comment: info.comment || '',
  };
}

function TrackInspectorInner({ row }: { row: DetailTarget }) {
  const setMusicInfo = useWorklistStore((s) => s.setMusicInfo);
  const setStatus = useWorklistStore((s) => s.setStatus);
  const setLibraryMusicInfo = useLibraryStore((s) => s.setMusicInfo);
  const renameWorklistRow = useWorklistStore((s) => s.renameRow);
  const renameLibraryRow = useLibraryStore((s) => s.renameRow);
  const renameDetailTarget = useDetailStore((s) => s.renameTarget);
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
      album_img: c.album_img || formData.album_img,        year: c.year || formData.year,
        // Read the candidate's genre rather than leaving the field on
        // whatever it was seeded with — MusicBrainz reports one, and
        // applying its tags used to throw that away.
        genre: c.genre || formData.genre,
        lyrics: c.lyric || c.lyrics || formData.lyrics,
    };
    setFormData(updated);
    useNoticeStore.getState().push(`已应用「${c.name}」候选标签`, 'info');
    setActiveTab('tags');
  };

  // Save all modified tags
  const handleSaveTags = async () => {
    setSaving(true);
    // Captured before the request: once the handler renames the file this
    // path stops resolving, so it is needed to match the response and to
    // tell the stores which row moved.
    const savedFrom = row.fullPath;
    try {
      const res = await updateId3([
        {
          file_full_path: savedFrom,
          file_name: row.fileName,
          ...formData,
        },
      ]);
      // Both stores key rows by fullPath, and both setters ignore ids
      // they don't own — so writing to both lets a save made from
      // 音乐库 also refresh the scraper table's status badge and
      // preview, and vice versa, without either view knowing which
      // section the dialog was opened from.
      setMusicInfo(savedFrom, formData);
      setStatus(savedFrom, 'scraped');
      setLibraryMusicInfo(savedFrom, formData);

      // Did the save also move the file? The handler only renames when
      // info["filename"] resolved to a different name, and says so in the
      // response. A row's id is its path, so if we skip this the table
      // keeps pointing at a file that is no longer there.
      const newPath = renamedPathFromUpdate(res, savedFrom);
      if (newPath) {
        const newFileName = baseNameOf(newPath);
        renameWorklistRow(savedFrom, newPath, newFileName);
        renameLibraryRow(savedFrom, newPath, newFileName);
        renameDetailTarget(savedFrom, newPath, newFileName);
        setFormData((prev) => ({ ...prev, filename: newFileName }));
        useNoticeStore
          .getState()
          .push(`已保存并重命名为「${newFileName}」`, 'info');
      } else {
        useNoticeStore.getState().push(`已成功保存「${formData.title || row.fileName}」标签`, 'info');
      }
      // The save itself landed; these are the sidecars that did not
      // follow the file. Warn rather than fail — the tags and the new
      // name are both saved, and the alternative is a silent orphan.
      for (const warning of sidecarWarningsFromUpdate(res)) {
        useNoticeStore.getState().push(warning, 'warn');
      }
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
    // No bg-* here on purpose. The design system layers surfaces by
    // altitude (index.css: surface-1 = main work area, surface-3 = dialog
    // / popover), and DialogContent already paints bg-popover. Setting
    // surface-1 on the contents made the detail area the *main work area*
    // again — in dark mode surface-1 is oklch(0.165) against a popover of
    // oklch(0.205), so the panel rendered darker than the frame around it
    // and the layering inverted.
    //
    // Also no w-80 lg:w-96 (the old column width, which fought the dialog's
    // own width) and no select-none (it belonged to the fixed column, and it
    // would have made the 歌词 textarea the one field the user could not
    // select text in).
    <aside className="flex flex-col h-full w-full overflow-hidden">
      {/* Hero Header: Big Artwork & Quick Actions */}
      {/* /60 rather than the /40 it had: every other panel in the app
          (toolbar, search cards, audit log) tints surface-2 at /60-/70,
          and /40 left this header visibly lighter than its neighbours. */}
      <div className="p-6 border-b border-border bg-surface-2/60 space-y-4 shrink-0">
        <div className="flex items-start gap-3">
          {/* Cover Art Box with Play Trigger */}
          <div className="relative w-20 h-20 rounded-xl overflow-hidden shrink-0 bg-muted group ring-1 ring-border/80 shadow-md">
            {coverSrc ? (
              <img
                src={coverSrc}
                alt=""
                className="w-full h-full object-cover"
              />
            ) : (
              <div
                className="w-full h-full flex items-center justify-center text-white text-lg font-bold"
                style={{ background: COVER_PLACEHOLDER_GRADIENTS[0] }}
              >
                {(formData.title || row.fileName).charAt(0)}
              </div>
            )}
            <button
              type="button"
              onClick={handleTogglePlay}
              // Always visible below sm: a phone has no hover, so the
              // 试听 control was simply absent there. Now that this is
              // the phone's detail surface too, that gap is visible.
              className="absolute inset-0 bg-black/40 opacity-100 sm:opacity-0 sm:group-hover:opacity-100 flex items-center justify-center transition-opacity text-white"
              title="即时试听播放"
            >
              {isThisPlaying ? <Pause className="w-7 h-7" /> : <Play className="w-7 h-7 fill-white" />}
            </button>
          </div>

          {/* Quick Info & Save */}
          <div className="flex-1 min-w-0 space-y-1">
            <h3 className="text-base font-bold text-foreground truncate leading-tight" title={formData.title}>
              {formData.title || row.fileName}
            </h3>
            <p className="text-sm text-muted-foreground truncate" title={formData.artist}>
              {formData.artist || '未知艺术家'} {formData.album ? `· ${formData.album}` : ''}
            </p>
            <div className="flex items-center gap-1.5 pt-0.5">
              <Badge variant="outline" className="text-[11px] px-1.5 h-5 font-mono uppercase bg-muted/60">
                {row.fileName.split('.').pop() || 'AUDIO'}
              </Badge>
              {formData.year && (
                <Badge variant="secondary" className="text-[11px] px-1.5 h-5 font-mono">
                  {formData.year}
                </Badge>
              )}
            </div>
          </div>
        </div>

        {/* Action Buttons Row */}
        <div className="flex items-center gap-2">
          <Button
            onClick={handleSaveTags}
            disabled={saving}
            className="flex-1 h-9 text-sm font-semibold bg-primary text-primary-foreground gap-1.5 shadow-xs"
            >
            <Save className={`w-4 h-4 ${saving ? 'animate-spin' : ''}`} />
            <span>{saving ? '正在保存…' : '保存标签'}</span>
          </Button>

          <Button
            variant="outline"
            onClick={() => {
              setActiveTab('candidates');
              handleSearchCandidates();
            }}
            disabled={loadingCandidates}
            className="h-9 px-3 text-sm gap-1.5 border-primary/40 text-primary hover:bg-primary/10"
            title="全网并发刮削多源候选"
            >
            <Sparkles className={`w-4 h-4 ${loadingCandidates ? 'animate-spin' : ''}`} />
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
        {/* The app's tab idiom is SettingsView's: a `bg-muted/60 p-1
            rounded-xl` pill row whose active tab lifts to `bg-background`
            + `shadow-sm` via the primitive's own data-active rules. This
            used to override the primitive instead — grid + h-9 + p-1 +
            rounded-none + a border-b — which desynced two things it draws
            internally: the trigger is `h-[calc(100%-1px)]` with an `after:`
            indicator at `bottom-[-5px]`, both tuned for the primitive's own
            metrics, so a custom box made the active state a filled
            `bg-background` rectangle floating in a `bg-surface-2` strip.
            `grid grid-cols-5` is kept only to keep 5 labels evenly divided
            in a 56rem dialog. */}
        <TabsList className={TAB_BAR_CLASS}>
          <TabsTrigger value="tags" className="rounded-lg text-sm">
            标签
          </TabsTrigger>
          <TabsTrigger value="candidates" className="rounded-lg text-sm relative">
            候选
            {candidates.length > 0 && (
              <span
                className="absolute top-1.5 right-2.5 w-2 h-2 rounded-full bg-primary"
                aria-label="有新候选"
              />
            )}
          </TabsTrigger>
          <TabsTrigger value="lyrics" className="rounded-lg text-sm">
            歌词
          </TabsTrigger>
          <TabsTrigger value="cover" className="rounded-lg text-sm">
            封面
          </TabsTrigger>
          <TabsTrigger value="audio" className="rounded-lg text-sm">
            指纹
          </TabsTrigger>
        </TabsList>

        {/* Tab 1: Tags Form */}
        <TabsContent value="tags" className="flex-1 overflow-hidden m-0 p-0">
          <ScrollArea className="h-full p-6">
            <div className="space-y-4 pb-10">
              <div>
                <Label className="text-sm mb-1.5 block">歌曲标题 (Title)</Label>
                <Input
                  value={formData.title || ''}
                  onChange={(e) => setFormData({ ...formData, title: e.target.value })}
                  className="h-9 text-sm font-medium"
                />
              </div>

              <div>
                <Label className="text-sm mb-1.5 block">文件名 (Filename)</Label>
                <Input
                  value={formData.filename || ''}
                  onChange={(e) =>
                    setFormData({ ...formData, filename: e.target.value })
                  }
                  className="h-9 text-sm font-mono"
                  placeholder="支持 ${artist} / ${title} 模板；扩展名自动补全"
                />
                <p className="mt-1.5 text-[11px] leading-relaxed text-muted-foreground">
                  保存时会同时重命名磁盘上的文件；与当前文件名相同则不做任何改动。模板需写成
                  {' '}
                  <code className="font-mono">{'${title}'}</code>
                  {' '}
                  这样带花括号的形式，写成 $title 会被当作字面量。
                </p>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <Label className="text-sm mb-1.5 block">艺术家 (Artist)</Label>
                  <Input
                    value={formData.artist || ''}
                    onChange={(e) => setFormData({ ...formData, artist: e.target.value })}
                    className="h-9 text-sm"
                  />
                </div>
                <div>
                  <Label className="text-sm mb-1.5 block">专辑 (Album)</Label>
                  <Input
                    value={formData.album || ''}
                    onChange={(e) => setFormData({ ...formData, album: e.target.value })}
                    className="h-9 text-sm"
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <Label className="text-sm mb-1.5 block">专辑艺术家 (Album Artist)</Label>
                  <Input
                    value={formData.albumartist || ''}
                    onChange={(e) => setFormData({ ...formData, albumartist: e.target.value })}
                    className="h-9 text-sm"
                  />
                </div>
                <div>
                  <Label className="text-sm mb-1.5 block">流派 (Genre)</Label>
                  <Input
                    value={formData.genre || ''}
                    onChange={(e) => setFormData({ ...formData, genre: e.target.value })}
                    className="h-9 text-sm"
                  />
                </div>
              </div>

              <div className="grid grid-cols-3 gap-3">
                <div>
                  <Label className="text-sm mb-1.5 block">年份 (Year)</Label>
                  <Input
                    value={formData.year || ''}
                    onChange={(e) => setFormData({ ...formData, year: e.target.value })}
                    className="h-9 text-sm font-mono"
                  />
                </div>
                <div>
                  <Label className="text-sm mb-1.5 block">音轨号 (Track#)</Label>
                  <Input
                    value={formData.tracknumber || ''}
                    onChange={(e) => setFormData({ ...formData, tracknumber: e.target.value })}
                    className="h-9 text-sm font-mono"
                  />
                </div>
                <div>
                  <Label className="text-sm mb-1.5 block">光盘 (Disc#)</Label>
                  <Input
                    value={formData.discnumber || ''}
                    onChange={(e) => setFormData({ ...formData, discnumber: e.target.value })}
                    className="h-9 text-sm font-mono"
                  />
                </div>
              </div>

              <div>
                <Label className="text-sm mb-1.5 block">备注与描述 (Comment)</Label>
                <Textarea
                  value={formData.comment || ''}
                  onChange={(e) => setFormData({ ...formData, comment: e.target.value })}
                  className="text-sm resize-none h-24"
                  placeholder="ID3 备注信息..."
                />
              </div>
            </div>
          </ScrollArea>
        </TabsContent>

        {/* Tab 2: Scrape Candidates */}
        <TabsContent value="candidates" className="flex-1 flex flex-col min-h-0 m-0 p-0">
          <div className="px-6 py-4 border-b border-border bg-surface-2/60 flex items-center gap-2 shrink-0">
            <Input
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleSearchCandidates()}
              placeholder="输入歌名/关键字检索..."
              className="h-9 text-sm flex-1 bg-background"
            />
            <Button
              onClick={() => handleSearchCandidates()}
              disabled={loadingCandidates}
              className="h-9 px-3"
              >
              <Search className={`w-4 h-4 ${loadingCandidates ? 'animate-spin' : ''}`} />
            </Button>
          </div>

          <ScrollArea className="flex-1 p-4">
            {loadingCandidates ? (
              <div className="py-16 text-center text-sm text-muted-foreground space-y-3">
                <RefreshCw className="w-7 h-7 animate-spin text-primary mx-auto" />
                <p>正在全网多源检索候选…</p>
              </div>
            ) : candidates.length === 0 ? (
              <div className="py-16 text-center text-sm text-muted-foreground space-y-3">
                <Sparkles className="w-7 h-7 text-muted-foreground/40 mx-auto" />
                <p>点击上方搜索或「智能刮削」检索候选</p>
              </div>
            ) : (
              <div className="space-y-2">
                {candidates.map((c, i) => {
                  const score = typeof c.score === 'number' ? Math.min(100, Math.round(c.score * 20)) : 85;
                  return (
                    <div
                      key={`${c.id}-${i}`}
                      className="p-4 rounded-xl border border-border/70 bg-surface-2/60 hover:bg-surface-2 hover:border-primary/50 transition-all space-y-3"
                    >
                      <div className="flex items-start gap-2.5">
                        <div className="w-12 h-12 rounded-lg overflow-hidden bg-muted shrink-0 ring-1 ring-border/50">
                          {c.album_img ? (
                            <img src={c.album_img} alt="" className="w-full h-full object-cover" />
                          ) : (
                            <div className="w-full h-full flex items-center justify-center bg-primary/10 text-primary text-sm font-bold">
                              {(c.name || '?').charAt(0)}
                            </div>
                          )}
                        </div>

                        <div className="flex-1 min-w-0 space-y-0.5">
                          <div className="flex items-center justify-between gap-1">
                            <span className="text-sm font-semibold text-foreground truncate">
                              {c.name}
                            </span>
                            <Badge variant="outline" className="text-[10px] px-1.5 h-4 uppercase font-mono">
                              {c.source || 'cloud'}
                            </Badge>
                          </div>
                          <p className="text-xs text-muted-foreground truncate">
                            {c.artist || '未知'} · {c.album || '单曲'}
                          </p>
                        </div>
                      </div>

                      <div className="flex items-center justify-between pt-2 border-t border-border/40">
                        <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                          <span>匹配度:</span>
                          {/* A match score is a measurement, not a success state. `text-emerald-500` was a raw palette value with no dark-mode counterpart, so it rendered as the same fixed green in both themes while everything around it followed the design tokens. */}
                          <span className="font-mono font-bold text-foreground">{score}%</span>
                        </div>
                        <Button
                          variant="secondary"
                          onClick={() => handleApplyCandidate(c)}
                          className="h-8 px-3 text-xs text-primary hover:bg-primary hover:text-primary-foreground font-semibold"
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
        <TabsContent value="lyrics" className="flex-1 flex flex-col min-h-0 m-0 p-6 space-y-3">
          <div className="flex items-center justify-between">
            <span className="text-sm font-semibold text-foreground">LRC 歌词文本</span>
            <Button
              variant="outline"
              onClick={handleFetchLyric}
              className="h-8 px-3 text-xs gap-1.5 text-primary"
              >
              <Search className="w-4 h-4" />
              <span>在线获取歌词</span>
            </Button>
          </div>
          <Textarea
            value={formData.lyrics || ''}
            onChange={(e) => setFormData({ ...formData, lyrics: e.target.value })}
            placeholder="[00:00.00] 暂无歌词或直接粘贴 LRC 歌词文本..."
            className="flex-1 font-mono text-sm leading-relaxed resize-none bg-surface-2/60 p-4"
          />
        </TabsContent>

        {/* Tab 4: Cover Artwork */}
        <TabsContent value="cover" className="flex-1 p-6 space-y-5 m-0">
          <div className="flex flex-col items-center space-y-4">
            <div className="w-56 h-56 rounded-xl overflow-hidden bg-muted ring-1 ring-border shadow-md flex items-center justify-center">
              {coverSrc ? (
                <img src={coverSrc} alt="" className="w-full h-full object-cover" />
              ) : (
                <div className="text-muted-foreground/60 flex flex-col items-center gap-2">
                  <ImageIcon className="w-10 h-10" />
                  <span className="text-sm">暂无内嵌封面</span>
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
                onClick={() => fileInputRef.current?.click()}
                className="flex-1 h-9 text-sm gap-1.5"
                >
                <Upload className="w-4 h-4" />
                <span>上传本地图片</span>
              </Button>
              {formData.album_img && (
                <Button
                  variant="ghost"
                  onClick={() => setFormData({ ...formData, album_img: '' })}
                  className="h-9 text-sm text-destructive hover:text-destructive"
                  >
                  <Trash2 className="w-4 h-4" />
                </Button>
              )}
            </div>
          </div>
        </TabsContent>

        {/* Tab 5: Audio info & AcoustID */}
        <TabsContent value="audio" className="flex-1 p-6 space-y-4 text-sm m-0">
          <div className="p-4 rounded-xl border border-border/70 bg-surface-2/60 space-y-3">
            <h4 className="font-semibold text-foreground flex items-center gap-1.5">
              <Fingerprint className="w-5 h-5 text-primary" />
              AcoustID 声学指纹
            </h4>
            <p className="text-muted-foreground text-xs leading-relaxed">
              基于 Chromaprint (fpcalc) 的物理波形分析，不受文件名与已有标签影响，可精准完成听歌识曲与文件查重。
            </p>
            <Button
              variant="outline"
              onClick={() => {
                setSelectedSource('acoustid');
                setActiveTab('candidates');
                handleSearchCandidates(row.fullPath);
              }}
              className="w-full h-9 text-sm text-primary border-primary/40"
              >
              计算声纹并在线识别
            </Button>
          </div>

          <div className="p-4 rounded-xl border border-border/70 bg-surface-2/60 space-y-2 font-mono text-xs">
            <div className="flex justify-between text-muted-foreground">
              <span>文件路径:</span>
              <span className="text-foreground truncate max-w-[60%]">{row.fullPath}</span>
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
      <aside className="flex flex-col h-full w-full p-8 items-center justify-center text-center space-y-4 text-muted-foreground">
        <div className="w-16 h-16 rounded-2xl bg-muted/60 flex items-center justify-center text-muted-foreground/60">
          <Music className="w-8 h-8" />
        </div>
        <div className="space-y-1">
          <p className="text-base font-semibold text-foreground">未选择曲目</p>
          <p className="text-sm text-muted-foreground/80 max-w-sm">
            在中间曲目列表中点击任意歌曲，即可在此即时查看与深度编辑标签、歌词、封面与多源刮削
          </p>
        </div>
      </aside>
    );
  }

  return <TrackInspectorInner key={row.fullPath} row={row} />;
}
