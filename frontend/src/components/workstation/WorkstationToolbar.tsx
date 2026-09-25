import { useState } from 'react';
import {
  Sparkles,
  RefreshCw,
  FolderTree,
  FileText,
  Trash2,
  FolderPlus,
  CheckSquare,
  Square,
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
import { useWorklistStore, type WorklistGrouping } from '@/store/useWorklistStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import {
  fullScanFolder,
  tidyFolder,
  fetchId3ByTitle,
  updateId3,
} from '@/api/client';
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
  const grouping = useWorklistStore((s) => s.grouping);
  const setGrouping = useWorklistStore((s) => s.setGrouping);

  const [parseOpen, setParseOpen] = useState(false);
  const [tidyOpen, setTidyOpen] = useState(false);
  const [scrapePopoverOpen, setScrapePopoverOpen] = useState(false);
  const [isScraping, setIsScraping] = useState(false);
  const [scrapeProgress, setScrapeProgress] = useState<{ current: number; total: number } | null>(null);

  // Scrape settings
  const [selectedSources, setSelectedSources] = useState<MusicSource[]>([
    'netease',
    'qmusic',
    'kugou',
  ]);
  const [matchMode, setMatchMode] = useState<'smart' | 'simple'>('smart');
  const [autoApplyFirstMatch, setAutoApplyFirstMatch] = useState(true);

  // Tidy form
  const [tidyForm, setTidyForm] = useState({
    root_path: '',
    first_dir: 'artist',
    second_dir: 'album',
  });
  const [tidyRunning, setTidyRunning] = useState(false);

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

  // Run Batch Auto Scrape directly on selected rows
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

    let successCount = 0;
    let failCount = 0;

    for (let i = 0; i < targetRows.length; i++) {
      const row = targetRows[i];
      setScrapeProgress({ current: i + 1, total: targetRows.length });

      try {
        const queryTitle =
          row.musicInfo?.title ||
          row.fileName.replace(/\.[^/.]+$/, '').trim();

        const primarySource = selectedSources[0] || 'smart_tag';
        const res = await fetchId3ByTitle(
          queryTitle,
          primarySource,
          row.fullPath,
        );

        const candidates = res?.data ?? [];
        if (candidates.length > 0 && autoApplyFirstMatch) {
          const topCandidate = candidates[0];
          const newInfo = {
            title: topCandidate.name || queryTitle,
            artist: topCandidate.artist || '',
            album: topCandidate.album || '',
            album_img: topCandidate.album_img || '',
            genre: '流行',
            year: topCandidate.year || '',
            lyrics: topCandidate.lyric || topCandidate.lyrics || '',
          };

          // Update tags on server
          await updateId3([
            {
              file_full_path: row.fullPath,
              file_name: row.fileName,
              ...newInfo,
            },
          ]);

          setMusicInfo(row.fullPath, newInfo);
          setStatus(row.fullPath, 'scraped');
          successCount++;
        } else {
          setStatus(row.fullPath, 'failed');
          failCount++;
        }
      } catch {
        setStatus(row.fullPath, 'failed');
        failCount++;
      }
    }

    setIsScraping(false);
    setScrapeProgress(null);
    useNoticeStore
      .getState()
      .push(`批量刮削完成：成功 ${successCount} 首，未匹配 ${failCount} 首`, 'info');
  };

  // Submit Tidy Folder
  const handleSubmitTidy = async () => {
    if (!tidyForm.root_path) {
      useNoticeStore.getState().push('请填写整理后的根目录', 'warn');
      return;
    }
    setTidyRunning(true);
    try {
      const sampleId = selectedIds[0] || rows[0]?.fullPath || '';
      const res = await tidyFolder({
        root_path: tidyForm.root_path,
        first_dir: tidyForm.first_dir,
        second_dir: tidyForm.second_dir,
        file_full_path: sampleId,
        select_data: [{ id: 0, name: sampleId, icon: 'icon-folder' }],
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

        <Separator orientation="vertical" className="mx-0.5 h-4" />

        {/* Scan local buttons */}
        <Button
          variant="ghost"
          size="sm"
          onClick={async () => {
            try {
              const res = await fullScanFolder();
              useNoticeStore.getState().push(res?.result ? '已提交全盘扫描任务' : '全盘扫描失败', res?.result ? 'info' : 'error');
            } catch {
              useNoticeStore.getState().push('全盘扫描失败', 'error');
            }
          }}
          className="h-8 gap-1 text-xs text-muted-foreground hover:text-foreground"
          title="对全盘音乐库进行扫描"
        >
          <RefreshCw className="w-3.5 h-3.5" />
          <span className="hidden lg:inline">全盘扫描</span>
        </Button>
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
