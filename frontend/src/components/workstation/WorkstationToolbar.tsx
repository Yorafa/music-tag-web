import { useState } from 'react';
import {
  Sparkles,
  FolderTree,
  FolderX,
  FileText,
  Trash2,
  FolderPlus,
  CheckSquare,
  Square,
  Copy,
  ScanSearch,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Separator } from '@/components/ui/separator';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import { Label } from '@/components/ui/label';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { ParseFilenamesModal } from '@/components/scraper/ParseFilenamesModal';
import { skippedNotesFromUpdate } from '@/utils/skippedNotes';
import { useWorklistStore, type WorklistGrouping } from '@/store/useWorklistStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import {
  tidyFolder,
  fetchId3ByTitle,
  batchUpdateId3,
  pruneEmptyFolders,
  checkDuplicate,
  deleteFiles,
} from '@/api/client';
import {
  scrapedMusicInfo,
  groupSelectionsByDir,
} from '@/components/workstation/scrapedInfo';
import {
  renamedPathFromUpdate,
  sidecarWarningsFromUpdate,
  duplicateWarningsFromUpdate,
  baseNameOf,
} from '@/components/detail/renameResult';
import { dedupeFlag, isDedupeEnabled, setDedupeEnabled } from '@/utils/dedupe';
import { deleteTargetsFor } from '@/components/workstation/duplicateBadge';
import { cn } from '@/lib/utils';
import type { MusicSource } from '@/types';

interface Props {
  onOpenDirPicker: () => void;
}

const SOURCES: { id: MusicSource; name: string }[] = [
  { id: 'netease', name: '网易云音乐' },
  { id: 'qmusic', name: 'QQ 音乐' },
  { id: 'kugou', name: '酷狗音乐' },
  { id: 'kuwo', name: '酷我音乐' },
  { id: 'migu', name: '咪咕音乐' },
  { id: 'musicbrainz', name: 'MusicBrainz' },
  { id: 'acoustid', name: 'AcoustID 声纹' },
];

export function WorkstationToolbar({ onOpenDirPicker }: Props) {
  const rows = useWorklistStore((s) => s.rows);
  const selectedIds = useWorklistStore((s) => s.selectedIds);
  const selectAll = useWorklistStore((s) => s.selectAll);
  const clearSelected = useWorklistStore((s) => s.clearSelected);
  const remove = useWorklistStore((s) => s.remove);
  const clear = useWorklistStore((s) => s.clear);
  const setStatus = useWorklistStore((s) => s.setStatus);
  const setMusicInfo = useWorklistStore((s) => s.setMusicInfo);
  const renameRow = useWorklistStore((s) => s.renameRow);
  const grouping = useWorklistStore((s) => s.grouping);
  const setGrouping = useWorklistStore((s) => s.setGrouping);
  const setDuplicates = useWorklistStore((s) => s.setDuplicates);
  const clearDuplicates = useWorklistStore((s) => s.clearDuplicates);

  const [parseOpen, setParseOpen] = useState(false);
  const [tidyOpen, setTidyOpen] = useState(false);
  const [scrapePopoverOpen, setScrapePopoverOpen] = useState(false);
  const [isScraping, setIsScraping] = useState(false);
  const [scrapeProgress, setScrapeProgress] = useState<{ current: number; total: number } | null>(null);

  // Duplicate check / delete
  const [isCheckingDup, setIsCheckingDup] = useState(false);
  const [dupResultOpen, setDupResultOpen] = useState(false);
  const [dupDeleteOpen, setDupDeleteOpen] = useState(false);
  const [isDeletingDup, setIsDeletingDup] = useState(false);

  // Scrape settings
  const [selectedSources, setSelectedSources] = useState<MusicSource[]>([
    'netease',
    'qmusic',
    'kugou',
  ]);
  const [matchMode, setMatchMode] = useState<'smart' | 'simple'>('smart');
  const [autoApplyFirstMatch, setAutoApplyFirstMatch] = useState(true);
  // Duplicate detection is a server-side decision (see internal/dedup);
  // this only records whether the user wants it at all. It lives here
  // because a batch scrape is where duplicates actually pile up.
  const [dedupe, setDedupe] = useState(() => isDedupeEnabled());

  // Tidy form
  const [tidyForm, setTidyForm] = useState({
    root_path: '',
    first_dir: 'artist',
    second_dir: 'album',
  });
  const [tidyRunning, setTidyRunning] = useState(false);
  const [pruning, setPruning] = useState(false);

  const hasSelection = selectedIds.length > 0;
  const isAllSelected = rows.length > 0 && selectedIds.length === rows.length;

  // Toggle all selection
  const handleToggleSelectAll = () => {
    if (isAllSelected) {
      clearSelected();
    } else {
      selectAll();
    }
  };

  // Run Batch Auto Scrape over the selected rows.
  //
  // Two phases, because the two halves have different shapes. The SEARCH
  // is one request per track and always will be — every row has its own
  // query. The WRITE used to be one request per track too, which is what
  // made a 50-row scrape feel broken: 100 serial round-trips, no way to
  // cancel, and one audit row per track so 操作审计 showed fifty anonymous
  // single edits instead of one scrape. The writes now go out as one
  // batch_update_id3 per directory, which is one request for the common
  // case of a single-folder selection.
  const handleRunBatchScrape = async () => {
    const targetRows = hasSelection
      ? rows.filter((r) => selectedIds.includes(r.fullPath))
      : rows.filter((r) => r.status === 'pending');

    if (targetRows.length === 0) {
      useNoticeStore.getState().push('请选择需要刮削的音乐行', 'warn');
      return;
    }

    setScrapePopoverOpen(false);
    setIsScraping(true);
    setScrapeProgress({ current: 0, total: targetRows.length });

    // Phase 1 — search each track, keep the tags we'd write.
    const matched = new Map<string, Record<string, unknown>>();
    let failCount = 0;

    for (let i = 0; i < targetRows.length; i++) {
      const row = targetRows[i];
      setScrapeProgress({ current: i + 1, total: targetRows.length });

      const queryTitle =
        row.musicInfo?.title || row.fileName.replace(/\.[^/.]+$/, '').trim();

      try {
        const primarySource = selectedSources[0] || 'smart_tag';
        const res = await fetchId3ByTitle(
          queryTitle,
          primarySource,
          row.fullPath,
        );

        const candidates = res?.data ?? [];
        if (candidates.length === 0 || !autoApplyFirstMatch) {
          setStatus(row.fullPath, 'failed');
          failCount++;
          continue;
        }
        matched.set(
          row.fullPath,
          scrapedMusicInfo(candidates[0], queryTitle),
        );
      } catch {
        setStatus(row.fullPath, 'failed');
        failCount++;
      }
    }

    // Phase 2 — write. The endpoint takes one base directory and joins
    // each select_data name onto it, so a selection spanning folders
    // becomes one request per folder rather than one request per track.
    const groups = groupSelectionsByDir(
      [...matched.keys()].map((fullPath) => ({ fullPath })),
      (r) => matched.get(r.fullPath) ?? {},
    );

    let successCount = 0;
    const sidecarNotes: string[] = [];
    const dupNotes: string[] = [];

    for (const [dir, selectData] of groups) {
      const rowsInGroup = selectData.map((s) => ({
        fullPath: `${dir === '' ? '' : `${dir}/`}${s.name}`,
      }));

      try {
        const res = await batchUpdateId3({
          file_full_path: dir,
          // Required by the wire contract, but every row below carries
          // its own music_info, so this map is a placeholder that never
          // reaches a file. See handler.BatchUpdateID3's perEntry.
          //
          // The dedupe opt-out is the exception to "never reaches a file":
          // it is a control flag, and the server copies it from here into
          // each overriding row.
          music_info: dedupeFlag(dedupe),
          select_data: selectData,
          // Records this as 自动刮削 rather than 批量标签, so a scrape is
          // filterable as itself in 操作审计.
          action: 'auto_scrape',
        });

        sidecarNotes.push(...sidecarWarningsFromUpdate(res));
        dupNotes.push(...duplicateWarningsFromUpdate(res));

        // A row the server refused to write comes back in `skipped`, not
        // `done`. Counting it as a success would report "成功 N 首" for tags
        // that never landed.
        //
        // `skipped` is not one kind of refusal: a duplicate was compared and
        // rejected, a not_audio row was never comparable at all (the name
        // pointed at a cover, or at a file that does not exist). Each entry
        // carries its own reason, so read that instead of asserting
        // "duplicate" for all of them.
        const refused = skippedNotesFromUpdate(res);

        for (const { fullPath } of rowsInGroup) {
          const skipNote = refused.get(fullPath);
          if (skipNote !== undefined) {
            setStatus(fullPath, 'failed');
            failCount++;
            dupNotes.push(`已跳过（${skipNote}）: ${fullPath}`);
            continue;
          }
          const info = matched.get(fullPath);
          if (info) setMusicInfo(fullPath, info);
          setStatus(fullPath, 'scraped');
          successCount++;

          // A write can also rename the file (the filename template), and
          // a row's identity IS its path — so the store has to be told or
          // the row is left pointing at something that no longer exists.
          const newPath = renamedPathFromUpdate(res, fullPath);
          if (newPath) renameRow(fullPath, newPath, baseNameOf(newPath));
        }
      } catch {
        for (const { fullPath } of rowsInGroup) {
          setStatus(fullPath, 'failed');
          failCount++;
        }
      }
    }

    setIsScraping(false);
    setScrapeProgress(null);
    useNoticeStore
      .getState()
      .push(`批量刮削完成：成功 ${successCount} 首，未匹配 ${failCount} 首`, 'info');
    for (const note of sidecarNotes) {
      useNoticeStore.getState().push(note, 'warn');
    }
  };

  // Submit Tidy Folder
  const handleSubmitTidy = async () => {
    if (!tidyForm.root_path) {
      useNoticeStore.getState().push('请填写整理后的根目录', 'warn');
      return;
    }
    setTidyRunning(true);
    try {
      // music_paths is what the worker actually iterates. The old payload
      // sent file_full_path + select_data instead — fields the endpoint
      // never reads — so MusicPaths arrived empty and ProcessTask bailed
      // with "invalid tidy payload" on every single submission. The UI
      // reported success because enqueueing *did* succeed; the task died
      // afterwards, five retries, silently. See handler.TidyFolder.
      const targetRows = hasSelection
        ? rows.filter((r) => selectedIds.includes(r.fullPath))
        : rows;
      if (targetRows.length === 0) {
        useNoticeStore.getState().push('没有可整理的音乐行', 'warn');
        setTidyRunning(false);
        return;
      }
      const res = await tidyFolder({
        root_path: tidyForm.root_path,
        first_dir: tidyForm.first_dir,
        second_dir: tidyForm.second_dir,
        music_paths: targetRows.map((r) => r.fullPath),
      });
      if (res?.result) {
        useNoticeStore.getState().push('已提交目录整理异步任务', 'info');
        setTidyOpen(false);
      } else {
        useNoticeStore.getState().push('目录整理提交失败', 'warn');
      }
    } catch {
      useNoticeStore.getState().push('目录整理提交失败', 'error');
    } finally {
      setTidyRunning(false);
    }
    };

  // Delete the directories 整理目录 leaves behind, and drop index rows for
  // files that are gone. Deliberately a separate button from tidy rather than
  // a step inside it: tidy moves files and the user may want to check the
  // result before anything is deleted, and a tidy that silently removed
  // directories would be doing two destructive things behind one
  // confirmation.
  const handlePruneEmpty = async () => {
    setPruning(true);
    try {
      const res = await pruneEmptyFolders();
      if (res?.result) {
        useNoticeStore.getState().push('已提交清理任务，结果见操作审计', 'info');
      } else {
        useNoticeStore.getState().push('清理任务提交失败', 'warn');
      }
    } catch {
      useNoticeStore.getState().push('清理任务提交失败', 'error');
    } finally {
      setPruning(false);
    }
  };

  // Run the read-only duplicate scan over the selected rows.
  //
  // Selection-scoped rather than whole-list on purpose. Each row is a full
  // four-stage check and the fingerprint stage spawns fpcalc over a
  // duration-filtered candidate set, so scanning a 2000-row list is a
  // multi-minute request. Hand-selecting the folder you just suspect is
  // both faster and more likely to be what the user meant.
  const handleCheckDuplicate = async () => {
    if (!hasSelection) {
      useNoticeStore.getState().push('请先选择要查重的音乐行', 'warn');
      return;
    }
    const targetRows = rows.filter((r) => selectedIds.includes(r.id));
    if (targetRows.length === 0) return;

    setIsCheckingDup(true);
    try {
      const report = await checkDuplicate(targetRows.map((r) => r.id));
      setDuplicates(
        report.results.map((row) => ({
          fileFullPath: row.file_full_path,
          verdict: row.verdict,
          matchField: row.match_field,
          duplicatePath: row.duplicate_path,
          reason: row.reason,
          run: row.run,
        })),
      );
      setDupResultOpen(true);
      const { duplicate, likely_duplicate: likely, error } = report.summary;
      if (duplicate > 0) {
        useNoticeStore
          .getState()
          .push(`查重完成：发现 ${duplicate} 个重复文件${likely > 0 ? `，${likely} 个疑似` : ''}`, 'warn');
      } else {
        useNoticeStore
          .getState()
          .push(`查重完成：未发现重复${likely > 0 ? `（${likely} 个疑似同名）` : ''}`, 'info');
      }
      if (error > 0) {
        useNoticeStore.getState().push(`${error} 个文件无法查重（已跳过）`, 'warn');
      }
    } catch (e) {
      useNoticeStore.getState().push(`查重失败：${(e as Error).message}`, 'error');
    } finally {
      setIsCheckingDup(false);
    }
  };

  // Rows the delete action would actually remove: the selection, narrowed
  // to rows the server called a content-level duplicate, minus any
  // mutually-referencing pair. See duplicateBadge.ts::deleteTargetsFor.
  const dupTargets = deleteTargetsFor(
    hasSelection ? rows.filter((r) => selectedIds.includes(r.id)) : rows,
  );

  const handleDeleteDuplicates = async () => {
    if (dupTargets.length === 0) return;
    setIsDeletingDup(true);
    try {
      const report = await deleteFiles(dupTargets.map((r) => r.id));
      setDupDeleteOpen(false);
      // The file is gone, so the row must go too — and the badge with it.
      // remove() drops the row entirely; clearDuplicates() then sweeps any
      // verdict left on rows that survived.
      remove(report.results.filter((x) => x.status === 'deleted').map((x) => x.file_full_path));
      clearDuplicates();
      useNoticeStore
        .getState()
        .push(
          `已删除 ${report.deleted} 个重复文件${report.failed > 0 ? `，${report.failed} 个未处理` : ''}（已移入回收目录，可恢复）`,
          report.failed > 0 ? 'warn' : 'info',
        );
    } catch (e) {
      useNoticeStore.getState().push(`删除失败：${(e as Error).message}`, 'error');
    } finally {
      setIsDeletingDup(false);
    }
  };

  return (
    <div className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 border-b border-border bg-surface-2/60 shrink-0">
      {/* Left Operations: Add, Batch Scrape, Parse, Tidy */}
      <div className="flex items-center gap-1.5 flex-wrap">
        {/* Add Music button */}
        <Button
          variant="outline"
          size="sm"
          onClick={onOpenDirPicker}
          className="h-8 gap-1 text-xs"
          title="从本地/NAS 目录选择添加音乐"
        >
          <FolderPlus className="w-3.5 h-3.5 text-primary" />
          <span>添加目录</span>
        </Button>

        <Separator orientation="vertical" className="mx-0.5 h-4" />

        {/* 🚀 Batch Auto Scrape Trigger with Config Popover */}
        <Popover open={scrapePopoverOpen} onOpenChange={setScrapePopoverOpen}>
          <PopoverTrigger
            render={
              <Button
                variant="default"
                size="sm"
                disabled={isScraping || rows.length === 0}
                className="h-8 gap-1.5 text-xs bg-primary text-primary-foreground font-semibold shadow-xs"
              >
                <Sparkles className={`w-3.5 h-3.5 ${isScraping ? 'animate-spin' : ''}`} />
                <span>
                  {isScraping
                    ? `正在刮削 (${scrapeProgress?.current}/${scrapeProgress?.total})`
                    : hasSelection
                    ? `自动刮削 (${selectedIds.length})`
                    : '一键自动刮削'}
                </span>
              </Button>
            }
          />
          <PopoverContent align="start" className="w-80 p-4 space-y-3.5 shadow-xl border-border">
            <div className="space-y-1">
              <h4 className="text-sm font-semibold flex items-center gap-1.5 text-foreground">
                <Sparkles className="w-4 h-4 text-primary" />
                批量智能刮削配置
              </h4>
              <p className="text-xs text-muted-foreground">
                对选中的 {hasSelection ? selectedIds.length : rows.length} 首音乐自动匹配最佳标签与高清封面
              </p>
            </div>

            {/* Source Priority Selection */}
            <div className="space-y-1.5">
              <Label className="text-xs font-semibold">首选音源与优先级</Label>
              <div className="grid grid-cols-2 gap-1.5">
                {SOURCES.map((s) => {
                  const active = selectedSources.includes(s.id);
                  return (
                    <button
                      key={s.id}
                      type="button"
                      onClick={() => {
                        if (active) {
                          if (selectedSources.length > 1) {
                            setSelectedSources(selectedSources.filter((x) => x !== s.id));
                          }
                        } else {
                          setSelectedSources([...selectedSources, s.id]);
                        }
                      }}
                      className={cn(
                        'flex items-center justify-between px-2 py-1.5 rounded text-xs border transition-colors',
                        active
                          ? 'bg-primary/10 border-primary text-primary font-medium'
                          : 'bg-surface-2 border-border text-muted-foreground hover:text-foreground',
                      )}
                    >
                      <span className="truncate">{s.name}</span>
                      {active && <span className="text-[10px]">✓</span>}
                    </button>
                  );
                })}
              </div>
            </div>

            {/* Options */}
            <div className="space-y-2 pt-1 border-t border-border/60">
              <div className="flex items-center justify-between">
                <Label className="text-xs">匹配策略</Label>
                <div className="flex items-center gap-1 text-xs">
                  <Button
                    variant={matchMode === 'smart' ? 'secondary' : 'ghost'}
                    size="sm"
                    className="h-6 text-[11px] px-2"
                    onClick={() => setMatchMode('smart')}
                  >
                    智能打分
                  </Button>
                  <Button
                    variant={matchMode === 'simple' ? 'secondary' : 'ghost'}
                    size="sm"
                    className="h-6 text-[11px] px-2"
                    onClick={() => setMatchMode('simple')}
                  >
                    精确匹配
                  </Button>
                </div>
              </div>

              <div className="flex items-center justify-between">
                <Label className="text-xs cursor-pointer" htmlFor="auto-apply">
                  自动采纳高置信度结果
                </Label>
                <Switch
                  id="auto-apply"
                  checked={autoApplyFirstMatch}
                  onCheckedChange={setAutoApplyFirstMatch}
                />
              </div>

              <div className="flex items-center justify-between gap-2">
                <Label htmlFor="dedupe" className="text-xs">
                  跳过重复文件
                </Label>
                <Switch
                  id="dedupe"
                  checked={dedupe}
                  onCheckedChange={(v) => {
                    setDedupe(v);
                    setDedupeEnabled(v);
                  }}
                />
              </div>
            </div>

            <Button
              className="w-full h-8 text-xs font-semibold"
              onClick={handleRunBatchScrape}
            >
              开始批量刮削
            </Button>
          </PopoverContent>
        </Popover>

        {/* Filename Parser */}
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            if (!hasSelection) {
              useNoticeStore.getState().push('请先选择至少一首音乐', 'info');
              return;
            }
            setParseOpen(true);
          }}
          className="h-8 gap-1 text-xs"
          title="从文件名规则提取艺术家与标题"
        >
          <FileText className="w-3.5 h-3.5 text-muted-foreground" />
          <span>解析文件名</span>
        </Button>

        {/* Tidy Folder */}
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            if (!hasSelection && rows.length === 0) {
              useNoticeStore.getState().push('曲库无待整理文件', 'info');
              return;
            }
            setTidyOpen(true);
          }}
          className="h-8 gap-1 text-xs"
          title="按「歌手/专辑/曲目」结构归档整理物理目录"
        >
          <FolderTree className="w-3.5 h-3.5 text-muted-foreground" />
          <span>整理目录</span>
        </Button>

        {/* Prune Empty Folders */}
        <Button
          variant="outline"
          size="sm"
          onClick={handlePruneEmpty}
          disabled={pruning}
          className="h-8 gap-1 text-xs"
          title="删除库内空目录（只删真正空的目录；残留封面或 .lrc 的目录会保留），并清理文件已不在但索引行还在的记录"
        >
          <FolderX className="w-3.5 h-3.5 text-muted-foreground" />
          <span>{pruning ? '提交中…' : '清理残留'}</span>
        </Button>

        <Separator orientation="vertical" className="mx-0.5 h-4" />

        {/* Duplicate check — read-only, over the selection. */}
        <Button
          variant="outline"
          size="sm"
          onClick={handleCheckDuplicate}
          disabled={isCheckingDup || !hasSelection}
          className="h-8 gap-1 text-xs"
          title="对选中的行做只读查重（文件名 / 哈希 / 声纹 / 元数据），不修改任何文件"
        >
          <ScanSearch className={`w-3.5 h-3.5 text-muted-foreground ${isCheckingDup ? 'animate-pulse' : ''}`} />
          <span>{isCheckingDup ? '查重中…' : '查重'}</span>
        </Button>

        {/* Delete the duplicates found. Only enabled once rows carry a
            content-level duplicate verdict — see deleteTargetsFor for why
            疑似 (name clash) is not enough to act on. */}
        {dupTargets.length > 0 && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => setDupDeleteOpen(true)}
            className="h-8 gap-1 text-xs text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive"
            title={`将 ${dupTargets.length} 个重复文件移入回收目录（可恢复）`}
          >
            <Copy className="w-3.5 h-3.5" />
            <span>删除重复 ({dupTargets.length})</span>
          </Button>
        )}

        {/* Scan local buttons */}
      </div>

      {/* Right Operations: Selection, Grouping, Clear */}
      <div className="flex items-center gap-2">
        {/* Grouping toggles */}
        <div className="flex items-center gap-1 border border-border/80 rounded-lg p-0.5 bg-surface-1">
          <span className="text-[10px] text-muted-foreground px-1.5 font-medium">分组:</span>
          {(['none', 'album', 'artist'] as WorklistGrouping[]).map((g) => {
            const labels: Record<WorklistGrouping, string> = { none: '无', album: '专辑', artist: '歌手' };
            const active = grouping === g;
            return (
              <button
                key={g}
                type="button"
                onClick={() => setGrouping(g)}
                className={cn(
                  'px-2 py-0.5 text-xs rounded transition-colors',
                  active ? 'bg-primary text-primary-foreground font-semibold shadow-xs' : 'text-muted-foreground hover:text-foreground',
                )}
              >
                {labels[g]}
              </button>
            );
          })}
        </div>

        {/* Selection count & Toggle */}
        <Button
          variant="ghost"
          size="sm"
          onClick={handleToggleSelectAll}
          className="h-8 gap-1 text-xs text-muted-foreground hover:text-foreground"
        >
          {isAllSelected ? (
            <CheckSquare className="w-3.5 h-3.5 text-primary" />
          ) : (
            <Square className="w-3.5 h-3.5" />
          )}
          <span>{isAllSelected ? '取消全选' : '全选'}</span>
          {hasSelection && (
            <Badge variant="secondary" className="h-4 px-1 text-[10px] ml-0.5 font-mono">
              {selectedIds.length}
            </Badge>
          )}
        </Button>

        {/* Clear/Remove */}
        {hasSelection ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => remove(selectedIds)}
            className="h-8 text-xs text-destructive hover:text-destructive hover:bg-destructive/10"
            title="从列表中移除选中项"
          >
            <Trash2 className="w-3.5 h-3.5" />
            <span className="hidden sm:inline ml-1">移除</span>
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            onClick={clear}
            disabled={rows.length === 0}
            className="h-8 text-xs text-muted-foreground hover:text-foreground"
            title="清空曲目列表"
          >
            <Trash2 className="w-3.5 h-3.5" />
            <span className="hidden sm:inline ml-1">清空</span>
          </Button>
        )}
      </div>

      {/* Parse Filenames Modal */}
      <ParseFilenamesModal
        open={parseOpen}
        onOpenChange={setParseOpen}
        selectedPaths={selectedIds.length > 0 ? selectedIds : rows.map((r) => r.fullPath)}
      />

      {/* Duplicate result panel — the same verdicts now shown as row badges,
          gathered into one list so a 40-row check is reviewable without
          hunting down individual rows. */}
      <Dialog open={dupResultOpen} onOpenChange={setDupResultOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle className="text-base flex items-center gap-2">
              <ScanSearch className="w-4 h-4 text-primary" />
              查重结果
            </DialogTitle>
          </DialogHeader>
          <div className="max-h-[50vh] overflow-y-auto space-y-1.5 py-2">
            {rows
              .filter((r) => r.duplicate)
              .map((r) => (
                <div
                  key={r.id}
                  className="flex items-start gap-2 text-xs px-2 py-1.5 rounded border border-border/60 bg-surface-1"
                >
                  <Badge
                    variant="outline"
                    className={
                      r.duplicate?.verdict === 'duplicate'
                        ? 'shrink-0 text-[10px] h-4 px-1.5 text-destructive border-destructive/40 bg-destructive/10'
                        : r.duplicate?.verdict === 'likely_duplicate'
                          ? 'shrink-0 text-[10px] h-4 px-1.5 text-amber-500 border-amber-500/40 bg-amber-500/10'
                          : 'shrink-0 text-[10px] h-4 px-1.5'
                    }
                  >
                    {r.duplicate?.verdict === 'duplicate'
                      ? '重复'
                      : r.duplicate?.verdict === 'likely_duplicate'
                        ? '疑似'
                        : '唯一'}
                  </Badge>
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-medium">{r.fileName}</div>
                    {r.duplicate?.duplicatePath && (
                      <div className="truncate text-muted-foreground">
                        与 {r.duplicate.duplicatePath} 相同
                      </div>
                    )}
                    {r.duplicate?.reason && (
                      <div className="text-muted-foreground">{r.duplicate.reason}</div>
                    )}
                  </div>
                </div>
              ))}
          </div>
          <DialogFooter showCloseButton>
            {dupTargets.length > 0 && (
              <Button
                variant="destructive"
                onClick={() => {
                  setDupResultOpen(false);
                  setDupDeleteOpen(true);
                }}
                className="text-xs h-8"
              >
                <Copy className="w-3.5 h-3.5 mr-1" />
                删除 {dupTargets.length} 个重复文件
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation. Names every file, says plainly that the
          originals are kept, and states that the delete is recoverable —
          because the honest description of a quarantine is not "删除". */}
      <Dialog open={dupDeleteOpen} onOpenChange={setDupDeleteOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle className="text-base flex items-center gap-2 text-destructive">
              <Trash2 className="w-4 h-4" />
              确认删除 {dupTargets.length} 个重复文件？
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-2.5 py-1 text-xs">
            <p className="text-muted-foreground">
              以下文件与库内其他文件内容完全相同，将被移出音乐库。每行括号内是保留下来的那一份。
            </p>
            <div className="max-h-52 overflow-y-auto rounded border border-border/60 bg-surface-1 divide-y divide-border/40">
              {dupTargets.map((r) => (
                <div key={r.id} className="px-2 py-1.5 flex flex-col">
                  <span className="truncate font-medium">{r.fileName}</span>
                  <span className="truncate text-muted-foreground">
                    保留：{r.duplicate?.duplicatePath ?? '—'}
                  </span>
                </div>
              ))}
            </div>
            <p className="text-muted-foreground">
              文件不会被真正抹除，而是移入数据目录下的
              <code className="mx-1 px-1 rounded bg-muted/50 font-mono">.trash/</code>
              ，需要时可以从服务器恢复。
            </p>
          </div>
          <DialogFooter showCloseButton>
            <Button
              variant="destructive"
              onClick={handleDeleteDuplicates}
              disabled={isDeletingDup}
              className="text-xs h-8"
            >
              {isDeletingDup ? '删除中…' : `确认删除 ${dupTargets.length} 个`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Tidy Folder Modal */}
      <Dialog open={tidyOpen} onOpenChange={setTidyOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle className="text-base flex items-center gap-2">
              <FolderTree className="w-4 h-4 text-primary" />
              整理音乐文件夹
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div>
              <Label className="text-xs mb-1 block">整理后的目标根目录</Label>
              <Input
                value={tidyForm.root_path}
                onChange={(e) => setTidyForm({ ...tidyForm, root_path: e.target.value })}
                placeholder="/app/media/"
                className="h-8 text-xs font-mono"
              />
            </div>
            <div>
              <Label className="text-xs mb-1 block">一级目录规则（如 artist / genre）</Label>
              <Input
                value={tidyForm.first_dir}
                onChange={(e) => setTidyForm({ ...tidyForm, first_dir: e.target.value })}
                className="h-8 text-xs"
              />
            </div>
            <div>
              <Label className="text-xs mb-1 block">二级目录规则（可选，如 album）</Label>
              <Input
                value={tidyForm.second_dir}
                onChange={(e) => setTidyForm({ ...tidyForm, second_dir: e.target.value })}
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter showCloseButton>
            <Button
              onClick={handleSubmitTidy}
              disabled={tidyRunning}
              className="text-xs h-8"
            >
              {tidyRunning ? '正在提交…' : '确认整理'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
