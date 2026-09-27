// The song-detail surface: 标签 / 候选 / 歌词 / 封面 / 指纹 for one
// local file, plus the 智能刮削 action. Rendered inside
// TrackDetailDialog, which is the only place it appears — it used to be
// the scraper view's right-hand column, but a column cannot exist on a
// phone, so the same content now opens as a dialog at every width and
// the phone gets the identical surface rather than a different one.
//
// Lives in components/detail/ rather than components/workstation/ because
// it is no longer owned by the scraper: 音乐库 opens the same dialog.
import { useEffect, useMemo, useState, useCallback, useRef } from 'react';
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
import { cn } from '@/lib/utils';
import { CandidateCard } from '@/components/detail/CandidateCard';
import { SourcePickerDialog } from '@/components/detail/SourcePickerDialog';
import { CANDIDATES_PER_PAGE, CANDIDATE_PAGE_SIZES, paginateCandidates } from '@/components/detail/candidates';
import {
  CANDIDATE_FETCH_LIMIT,
  SOURCES,
  loadRememberedSources,
  rememberSources,
} from '@/components/common/tagSources';
import { searchAcrossSources } from '@/api/scrapeSources';
import {
  APPLYABLE_FIELDS,
  fieldLabel,
  type ApplyableField,
} from '@/components/workstation/scrapedInfo';
import { useWorklistStore } from '@/store/useWorklistStore';
import { useLibraryStore } from '@/store/useLibraryStore';
import { usePlayerStore } from '@/store/usePlayerStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { duplicateWarningsFromUpdate } from '@/components/detail/renameResult';
import { dedupeFlag, isDedupeEnabled } from '@/utils/dedupe';
import {
  updateId3,
  fetchId3ByTitle,
  fetchLyric,
  uploadImage,
  getMusicId3,
} from '@/api/client';
import { resolveCoverSrc, COVER_PLACEHOLDER_GRADIENTS } from '@/utils/cover';
import { buildMediaUrl } from '@/lib/mediaUrl';
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

/** What a first-time scrape asks. The three big Chinese catalogues plus
 *  MusicBrainz: enough coverage that a miss is usually the track's fault,
 *  not the source set's, and the picker is one click away for anything
 *  else. AcoustID is deliberately absent — it reads the audio rather than
 *  the title, so it belongs to the 指纹 tab. */
const DEFAULT_SCRAPE_SOURCES: MusicSource[] = ['netease', 'qmusic', 'musicbrainz'];

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
  const [page, setPage] = useState(1);
  // How many candidates one page holds. Adjustable because "5" was a guess
  // baked into a constant: someone comparing 25 results wants them side by
  // side, someone reading 歌名/艺术家/年份/流派 wants one card at a time.
  const [pageSize, setPageSize] = useState(CANDIDATES_PER_PAGE);
  const [loadingCandidates, setLoadingCandidates] = useState(false);
  const [saving, setSaving] = useState(false);
  // Which sources the last search asked. Kept as state rather than a
  // constant so the picker can start from them: re-running a search with
  // the same sources is the common case, and re-picking them every time is
  // the friction that stops people retrying with a different source.
  const [scrapeSources, setScrapeSources] = useState<MusicSource[]>(() =>
    loadRememberedSources(DEFAULT_SCRAPE_SOURCES),
  );
  const [sourcePickerOpen, setSourcePickerOpen] = useState(false);

  // Remember the choice, not just the search. Picking a source set is
  // deliberate and slow, and the next track usually wants the same one —
  // re-ticking the same three boxes before every search is the friction
  // that makes people scrape with whatever the default was.
  useEffect(() => {
    rememberSources(scrapeSources);
  }, [scrapeSources]);
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

  // Search candidate matches across the sources the user picked. The old
  // version searched one hardcoded source and told the user nothing about
  // it, so "no candidates" was indistinguishable from "wrong source".
  const handleSearchCandidates = useCallback(
    async (customQuery?: string, sources?: MusicSource[], limit?: number) => {
      const query =
        customQuery ||
        searchQuery ||
        formData.title ||
        row.fileName.replace(/\.[^/.]+$/, '');
      if (!query) return;
      const chosen = sources && sources.length > 0 ? sources : scrapeSources;

      setLoadingCandidates(true);
      setSourcePickerOpen(false);
      try {
        const { candidates: list, emptySources, failedSources, error } =
          await searchAcrossSources(query, chosen, row.fullPath, limit);
        setCandidates(list);
        setPage(1);
        if (list.length > 0) {
          useNoticeStore.getState().push(`找到 ${list.length} 个匹配候选`, 'info');
        } else {
          useNoticeStore.getState().push('未找到相关音源，可尝试更换关键词或音源', 'warn');
        }
        // A source that failed is a different fact from a source that found
        // nothing, and the user cannot retry a source they were never told
        // had failed.
        if (failedSources.length > 0) {
          useNoticeStore
            .getState()
            .push(`${failedSources.length} 个音源检索失败：${error ?? '未知原因'}`, 'warn');
        } else if (list.length === 0 && emptySources.length > 0) {
          useNoticeStore
            .getState()
            .push(`${emptySources.length} 个音源均未返回结果`, 'warn');
        }
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        useNoticeStore.getState().push(`检索失败: ${msg}`, 'error');
      } finally {
        setLoadingCandidates(false);
      }
    },
    [row, searchQuery, formData.title, scrapeSources],
  );

  /** 指纹 tab: fingerprint the audio with AcoustID alone. Kept separate
   *  from handleSearchCandidates because it is a different question — it
   *  reads the file, so it is not one of the title sources the picker
   *  offers. */
  const handleFingerprintSearch = useCallback(async () => {
    setLoadingCandidates(true);
    setActiveTab('candidates');
    try {
      const res = await fetchId3ByTitle(row.fullPath, 'acoustid', row.fullPath, CANDIDATE_FETCH_LIMIT);
      const list = res?.data ?? [];
      setCandidates(list);
      setPage(1);
      useNoticeStore
        .getState()
        .push(
          list.length > 0 ? `声纹识别到 ${list.length} 个候选` : '声纹未匹配到任何候选',
          list.length > 0 ? 'info' : 'warn',
        );
    } catch (e) {
      useNoticeStore
        .getState()
        .push(`声纹识别失败: ${e instanceof Error ? e.message : String(e)}`, 'error');
    } finally {
      setLoadingCandidates(false);
    }
  }, [row.fullPath]);

  // Apply candidate metadata
  // Apply a candidate, or just some of it.
  //
  // `fields` omitted means the whole candidate, which is right most of the
  // time. The subset form is what makes a second candidate worth opening:
  // taking 专辑 from one source and 歌词 from another, or fixing only the
  // year, used to be impossible — the only control was "take everything",
  // and the only way to find out whether it was right was to save and look
  // at the file.
  //
  // A field the candidate does not carry is left as the form had it. The
  // alternative — clearing it — would let a source with no genre erase the
  // genre the file already had.
  const handleApplyCandidate = (c: SongInfo, fields?: ApplyableField[]) => {
    const wanted = fields ?? APPLYABLE_FIELDS;
    const values: Partial<MusicTagInfo> = {
      title: c.name,
      artist: c.artist,
      album: c.album,
      album_img: c.album_img,
      year: c.year,
      // Read the candidate's genre rather than leaving the field on
      // whatever it was seeded with — MusicBrainz reports one, and
      // applying its tags used to throw that away.
      genre: c.genre,
      lyrics: c.lyric || c.lyrics,
    };
    const patched: Partial<MusicTagInfo> = { ...formData };
    for (const field of wanted) {
      const value = values[field];
      if (value) (patched as Record<string, unknown>)[field] = value;
    }
    setFormData(patched);
    useNoticeStore
      .getState()
      .push(
        fields
          ? `已应用「${c.name}」的 ${fields.map(fieldLabel).join('、')}`
          : `已应用「${c.name}」候选标签`,
        'info',
      );
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
          // The same preference the batch scrape reads, so turning dedup off
          // in one place covers both. It lives inside the tag map because
          // that is the only place the server looks for it.
          ...dedupeFlag(isDedupeEnabled()),
        },
      ]);
      // Both stores key rows by fullPath, and both setters ignore ids
      // they don't own — so writing to both lets a save made from
      // 音乐库 also refresh the scraper table's status badge and
      // preview, and vice versa, without either view knowing which
      // section the dialog was opened from.
      for (const note of duplicateWarningsFromUpdate(res)) {
        useNoticeStore.getState().push(note, 'warn');
      }
      const rawSkipped = (res as { skipped?: unknown })?.skipped;
      if (Array.isArray(rawSkipped) && rawSkipped.length > 0) {
        // A refused duplicate is the only thing this endpoint puts in
        // `skipped`, so say why rather than reporting a save that wrote
        // nothing, and point at the way through.
        useNoticeStore
          .getState()
          .push(
            '保存被跳过：内容与库内文件完全一致。可在刮削设置中关闭「跳过重复文件」后重试',
            'warn',
          );
      }

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
      url: buildMediaUrl(row.fullPath),
      title: formData.title || row.fileName,
      artist: formData.artist || '本地音乐',
      cover: formData.album_img,
      lyrics: formData.lyrics || undefined,
      source: { kind: 'local', fileName, filePath },
    });
  };

  const coverSrc = resolveCoverSrc(formData);

  // Derived, so a re-search that returns fewer candidates cannot leave the
  // tab on a page number that no longer exists.
  const candidatePage = useMemo(
    () => paginateCandidates(candidates, page, pageSize),
    [candidates, page, pageSize],
  );

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
              // Opens the source picker rather than searching straight
              // away. Which sources get asked is the one decision that
              // decides whether a scrape finds anything, and it used to be
              // made invisibly, on the user's behalf, by a constant.
              setSourcePickerOpen(true);
            }}
            disabled={loadingCandidates}
            className="h-9 px-3 text-sm gap-1.5 border-primary/40 text-primary hover:bg-primary/10"
            title="选择音源后全网并发刮削候选"
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
          <div className="px-6 py-3 border-b border-border bg-surface-2/60 space-y-2 shrink-0">
            <div className="flex items-center gap-2">
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
                title={`用选中的 ${scrapeSources.length} 个音源检索`}
              >
                <Search className={`w-4 h-4 ${loadingCandidates ? 'animate-spin' : ''}`} />
              </Button>
            </div>
            {/* Which sources the search above will ask. The bare search
                button used to hide this entirely, which made an empty
                result impossible to act on: the fix (a different keyword,
                a different source) was invisible from here. */}
            <div className="flex items-center gap-1.5 flex-wrap">
              <span className="text-[10px] text-muted-foreground shrink-0">音源</span>
              {SOURCES.filter((s) => scrapeSources.includes(s.id)).map((s) => (
                <Badge
                  key={s.id}
                  variant="secondary"
                  className="h-4 px-1.5 text-[10px] font-normal"
                >
                  {s.name}
                </Badge>
              ))}
              <button
                type="button"
                onClick={() => setSourcePickerOpen(true)}
                className="text-[10px] text-primary hover:underline shrink-0"
              >
                更换
              </button>
              {/* Page size lives in the header, not the pager: the pager
                  only exists once there is more than one page, and a
                  control that appears and disappears with the thing it
                  controls is a control you cannot find. Back to page 1
                  because page 4 of a 5-per-page list is page 1 of the
                  new one, not page 4. */}
              <span className="flex items-center gap-1 ml-auto shrink-0">
                <span className="text-[10px] text-muted-foreground">每页</span>
                {CANDIDATE_PAGE_SIZES.map((n) => (
                  <button
                    key={n}
                    type="button"
                    aria-pressed={pageSize === n}
                    onClick={() => {
                      setPageSize(n);
                      setPage(1);
                    }}
                    className={cn(
                      'px-1.5 h-4 rounded text-[10px] font-mono transition-colors',
                      pageSize === n
                        ? 'bg-primary text-primary-foreground'
                        : 'text-muted-foreground hover:bg-accent hover:text-foreground',
                    )}
                  >
                    {n}
                  </button>
                ))}
              </span>
            </div>
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
              <>
              <div className="space-y-2">
                {candidatePage.items.map((c, i) => (
                  <CandidateCard
                    key={`${c.source ?? ''}-${c.id}-${i}`}
                    candidate={c}
                    onApply={handleApplyCandidate}
                  />
                ))}
              </div>
              {/* Paging, not a longer scroll. Fifteen candidates in one
                  scroll is a list nobody compares; five at a time with a
                  position indicator is one they can read against the
                  count they were told about. */}
              {candidatePage.pageCount > 1 && (
                <div className="flex items-center justify-between gap-2 pt-3 mt-1 border-t border-border/40">
                  <span className="text-[11px] text-muted-foreground font-mono">
                    第 {candidatePage.page} / {candidatePage.pageCount} 页 · 共{' '}
                    {candidatePage.total} 个候选
                  </span>
                  <div className="flex items-center gap-1">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setPage((p) => Math.max(1, p - 1))}
                      disabled={candidatePage.page <= 1}
                      className="h-7 px-2 text-xs"
                    >
                      上一页
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        setPage((p) =>
                          Math.min(candidatePage.pageCount, p + 1),
                        )
                      }
                      disabled={candidatePage.page >= candidatePage.pageCount}
                      className="h-7 px-2 text-xs"
                    >
                      下一页
                    </Button>
                  </div>
                </div>
              )}
              </>
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
              onClick={handleFingerprintSearch}
              disabled={loadingCandidates}
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

      {/* Mounted only while open: its source selection is draft state, and
          keeping it mounted would let a cancelled dialog leave half-changed
          sources behind for the next search. */}
      {sourcePickerOpen && (
        <SourcePickerDialog
          open={sourcePickerOpen}
          onOpenChange={setSourcePickerOpen}
          initialSources={scrapeSources}
          initialQuery={searchQuery || formData.title || ''}
          loading={loadingCandidates}
          onSearch={(sources, query) => {
            setScrapeSources(sources);
            setSearchQuery(query);
            void handleSearchCandidates(query, sources, CANDIDATE_FETCH_LIMIT);
          }}
        />
      )}
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
