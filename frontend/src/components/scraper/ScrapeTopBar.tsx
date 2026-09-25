// Top action bar for scrape mode. Houses the three "加工类" tools —
// 全盘扫描 / 增量扫描 / 整理文件夹 — that act on the user's selected
// Worklist rows. The tidy folder Dialog logic is lifted wholesale from
// the old Toolbar.tsx, but its state lives here in component scope
// rather than in the doomed Toolbar component.
//
// Tool semantics in this layout:
//   - 全盘扫描 / 增量扫描: kick off /api/full_scan_folder/ or
//     /api/task1/ (currently fire-and-forget — backend runs an asynq
//     scanner that updates the Worklist rows' status asynchronously).
//     We optimistically flip selected pending rows to a transient
//     "scraped" state when the call returns 200; the backend's actual
//     pass/fail feeds back through the asynq event bus (lands in a
//     future PR — until then the optimistic flip is the only signal we
//     get).
//   - 整理文件夹: open an inline Dialog (extracted from old Toolbar) so
//     the user can choose root / first_dir / second_dir before posting
//     /api/tidy_folder/. The call is best-effort — backend folds the
//     files into the new layout asynchronously.
//
// Disabled affordance: when no rows are selected, the buttons are
// visually muted and a tooltip "请先选择至少一行" reads on hover. We
// keep clickability but route non-selected clicks through useNoticeStore
// so the user gets feedback rather than a silent no-op.

import { useState } from 'react';
import {
  Search,
  RefreshCw,
  FolderTree,
  FileText,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';
import { ParseFilenamesModal } from '@/components/scraper/ParseFilenamesModal';
import { useWorklistStore } from '@/store/useWorklistStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import {
  scanFolder,
  fullScanFolder,
  tidyFolder,
} from '@/api/client';
import { cn } from '@/lib/utils';

interface ScrapeTopBarProps {
  className?: string;
}

interface TidyForm {
  root_path: string;
  first_dir: string;
  second_dir: string;
}


export function ScrapeTopBar({ className }: ScrapeTopBarProps) {
  const selectedIds = useWorklistStore((s) => s.selectedIds);

  const [running, setRunning] = useState<'scan-full' | 'scan' | 'tidy' | null>(
    null,
  );
  const [tidyOpen, setTidyOpen] = useState(false);
  const [parseOpen, setParseOpen] = useState(false);
  const [tidyForm, setTidyForm] = useState<TidyForm>({
    root_path: '',
    first_dir: 'artist',
    second_dir: '',
  });

  const hasSelection = selectedIds.length > 0;

  const disabledHint = !hasSelection ? '请先选择至少一行' : undefined;

  const runScanFull = async () => {
    if (!hasSelection) {
      useNoticeStore.getState().push('请先选择至少一行', 'info');
      return;
    }
    setRunning('scan-full');
    try {
      const res = await fullScanFolder();
      useNoticeStore.getState().push(
        res?.result ? '已提交全盘扫描任务' : '全盘扫描提交失败',
        res?.result ? 'info' : 'error',
      );
      // Best-effort optimistic flip (see header). Backend will
      // overwrite via asynq once that integration lands.
      const setStatus = useWorklistStore.getState().setStatus;
      for (const id of selectedIds) setStatus(id, 'scraped');
    } catch {
      useNoticeStore.getState().push('全盘扫描提交失败', 'error');
    } finally {
      setRunning(null);
    }
  };

  const runScan = async () => {
    if (!hasSelection) {
      useNoticeStore.getState().push('请先选择至少一行', 'info');
      return;
    }
    setRunning('scan');
    try {
      const res = await scanFolder();
      useNoticeStore.getState().push(
        res?.result ? '已提交增量扫描任务' : '增量扫描提交失败',
        res?.result ? 'info' : 'error',
      );
      const setStatus = useWorklistStore.getState().setStatus;
      for (const id of selectedIds) setStatus(id, 'scraped');
    } catch {
      useNoticeStore.getState().push('增量扫描提交失败', 'error');
    } finally {
      setRunning(null);
    }
  };

  const openTidy = () => {
    if (!hasSelection) {
      useNoticeStore.getState().push('请先选择至少一行', 'info');
      return;
    }
    setTidyOpen(true);
  };

  const openParse = () => {
    if (!hasSelection) {
      useNoticeStore.getState().push('请先选择至少一行', 'info');
      return;
    }
    setParseOpen(true);
  };

  const submitTidy = async () => {
    if (!tidyForm.root_path) {
      useNoticeStore.getState().push('请填写整理后的根目录', 'warn');
      return;
    }
    setRunning('tidy');
    // Mirror the file_full_path selection from the first selected row.
    // (Old Toolbar used folderName — odd but harmless; we keep
    // id-as-path which is more stable as a folder identifier.)
    const sampleId = selectedIds[0];
    try {
      const res = await tidyFolder({
        root_path: tidyForm.root_path,
        first_dir: tidyForm.first_dir,
        second_dir: tidyForm.second_dir,
        file_full_path: sampleId,
        select_data: [
          { id: 0, name: sampleId, icon: 'icon-folder' },
        ],
      });
      useNoticeStore.getState().push(
        res?.result ? '已提交整理任务' : '整理任务提交失败',
        res?.result ? 'info' : 'error',
      );
      setTidyOpen(false);
    } catch {
      useNoticeStore.getState().push('整理任务提交失败', 'error');
    } finally {
      setRunning(null);
    }
  };

  return (
    <>
      <div
        className={cn(
          'flex items-center gap-2 px-3 py-2 border-b border-border bg-surface-2 shrink-0',
          className,
        )}
      >
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant="outline"
                size="sm"
                onClick={runScanFull}
                disabled={running !== null}
                aria-label="全盘扫描"
                className={disabledHint ? 'opacity-60' : ''}
              >
                <Search className="w-3.5 h-3.5 mr-1.5" />
                全盘扫描
              </Button>
            }
          />
          <TooltipContent>{disabledHint ?? '对选中的 Worklist 行发起全盘扫描'}</TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant="outline"
                size="sm"
                onClick={runScan}
                disabled={running !== null}
                aria-label="增量扫描"
                className={disabledHint ? 'opacity-60' : ''}
              >
                <RefreshCw className="w-3.5 h-3.5 mr-1.5" />
                增量扫描
              </Button>
            }
          />
          <TooltipContent>{disabledHint ?? '对选中的 Worklist 行发起增量扫描'}</TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant="outline"
                size="sm"
                onClick={openTidy}
                disabled={running !== null}
                aria-label="整理文件夹"
                className={disabledHint ? 'opacity-60' : ''}
              >
                <FolderTree className="w-3.5 h-3.5 mr-1.5" />
                整理文件夹
              </Button>
            }
          />
          <TooltipContent>{disabledHint ?? '按选中的目录布局重整文件'}</TooltipContent>
        </Tooltip>

        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant="outline"
                size="sm"
                onClick={openParse}
                disabled={running !== null}
                aria-label="解析文件名"
                className={disabledHint ? 'opacity-60' : ''}
                data-testid="parse-filenames-trigger"
              >
                <FileText className="w-3.5 h-3.5 mr-1.5" />
                解析文件名
              </Button>
            }
          />
          <TooltipContent>{disabledHint ?? '预览 + 应用文件名解析到选中行'}</TooltipContent>
        </Tooltip>

        {/* Spacer + selected-count badge so the bar reads "you have N
            rows selected" at a glance. Mirrors the original Toolbar's
            affordance level so muscle-memory transfers across the
            rewrite. */}
        <div className="ml-auto text-xs text-muted-foreground">
          {hasSelection ? `已选 ${selectedIds.length} 项` : ''}
        </div>
      </div>

      {/* Tidy folder Dialog — lifted wholesale from Toolbar.tsx with
          one functional tweak: sampleId (first selected row id) drives
          the select_data payload instead of the old `folderName`
          derived from filePath (the latter was a substring split that
          conflates ids across the new Worklist shape). */}
      <Dialog open={tidyOpen} onOpenChange={setTidyOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>整理文件夹</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div>
              <Label className="text-xs mb-1 block">整理后的根目录</Label>
              <Input
                value={tidyForm.root_path}
                onChange={(e) =>
                  setTidyForm({ ...tidyForm, root_path: e.target.value })
                }
                placeholder="/app/media/"
              />
            </div>
            <div>
              <Label className="text-xs mb-1 block">一级目录</Label>
              <Input
                value={tidyForm.first_dir}
                onChange={(e) =>
                  setTidyForm({ ...tidyForm, first_dir: e.target.value })
                }
              />
            </div>
            <div>
              <Label className="text-xs mb-1 block">二级目录（可选）</Label>
              <Input
                value={tidyForm.second_dir}
                onChange={(e) =>
                  setTidyForm({ ...tidyForm, second_dir: e.target.value })
                }
              />
            </div>
          </div>
          <DialogFooter showCloseButton>
            <Button
              onClick={submitTidy}
              disabled={running === 'tidy'}
            >
              {running === 'tidy' ? '提交中…' : '提交'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* C.2 Filename Parse modal — independent component to keep
          ScrapeTopBar focused on the toolbar-level affordances. The
          modal fetches its own preview token on mount, lets the user
          edit overrides per row, and re-submits a single apply call
          on confirmation. See `ParseFilenamesModal.tsx` for the
          preview/apply round-trip contract. */}
      <ParseFilenamesModal
        open={parseOpen}
        onOpenChange={setParseOpen}
        selectedPaths={selectedIds}
      />

    </>
  );
}
